package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"
	"github.com/accelbench/accelbench/internal/metrics"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
)

const (
	readinessTimeout = 25 * time.Minute
	readinessPoll    = 10 * time.Second
	jobTimeout       = 2 * time.Hour
	jobPoll          = 15 * time.Second
	defaultNamespace = "accelbench"
)

// RunConfig holds everything needed to execute a benchmark run.
type RunConfig struct {
	RunID        string
	Model        *database.Model
	InstanceType *database.InstanceType
	Request      *database.RunRequest
}

// Orchestrator manages the benchmark lifecycle.
type Orchestrator struct {
	client  kubernetes.Interface
	repo    database.Repo
	mu      sync.Mutex
	cancels map[string]context.CancelFunc // runID → cancel
}

// New creates a new Orchestrator.
func New(client kubernetes.Interface, repo database.Repo) *Orchestrator {
	return &Orchestrator{
		client:  client,
		repo:    repo,
		cancels: make(map[string]context.CancelFunc),
	}
}

// CancelRun cancels a running benchmark by its run ID. Returns true if
// a cancel function was found and invoked.
func (o *Orchestrator) CancelRun(runID string) bool {
	o.mu.Lock()
	cancel, ok := o.cancels[runID]
	o.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Execute runs the full benchmark lifecycle: deploy → ready → loadgen → collect → persist → teardown.
func (o *Orchestrator) Execute(ctx context.Context, cfg RunConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register the cancel function so CancelRun can stop this goroutine.
	o.mu.Lock()
	o.cancels[cfg.RunID] = cancel
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.cancels, cfg.RunID)
		o.mu.Unlock()
	}()

	ns := defaultNamespace
	modelName := fmt.Sprintf("bench-%s", cfg.RunID[:8])
	loadgenName := fmt.Sprintf("loadgen-%s", cfg.RunID[:8])

	// Phase 1: Mark run as running.
	if err := o.repo.UpdateRunStatus(ctx, cfg.RunID, "running"); err != nil {
		return fmt.Errorf("update status to running: %w", err)
	}

	// Ensure teardown happens regardless of outcome.
	defer o.teardown(context.Background(), ns, modelName, loadgenName)

	// Phase 2: Deploy model Deployment + Service.
	log.Printf("[%s] deploying model %s on %s", cfg.RunID[:8], cfg.Request.ModelHfID, cfg.Request.InstanceTypeName)
	if err := o.deployModel(ctx, ns, modelName, cfg); err != nil {
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("deploy model: %w", err)
	}

	// Phase 3: Wait for readiness.
	log.Printf("[%s] waiting for model readiness", cfg.RunID[:8])
	if err := o.waitForReady(ctx, ns, modelName); err != nil {
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("wait for readiness: %w", err)
	}

	// Start GPU scraper for GPU instances (non-fatal if it fails).
	var gpuScraper *GPUScraper
	if strings.EqualFold(cfg.InstanceType.AcceleratorType, "gpu") {
		totalMemGiB := float64(cfg.InstanceType.AcceleratorMemoryGiB)
		gpuScraper = NewGPUScraper(modelName, 8000, totalMemGiB)
		gpuScraper.Start(ctx)
		log.Printf("[%s] started GPU metrics scraper", cfg.RunID[:8])
	}

	// Phase 4: Launch load generator Job.
	log.Printf("[%s] launching load generator", cfg.RunID[:8])
	if err := o.launchLoadgen(ctx, ns, loadgenName, modelName, cfg); err != nil {
		if gpuScraper != nil {
			gpuScraper.Stop()
		}
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("launch loadgen: %w", err)
	}

	// Phase 5: Wait for Job completion and collect results.
	log.Printf("[%s] waiting for load generator completion", cfg.RunID[:8])
	logData, err := o.waitAndCollect(ctx, ns, loadgenName)

	// Stop GPU scraper and collect metrics (before checking loadgen error).
	var gpuMetrics *GPUMetrics
	if gpuScraper != nil {
		gpuMetrics = gpuScraper.Stop()
		if gpuMetrics != nil {
			log.Printf("[%s] GPU metrics: utilization_peak=%.1f%% avg=%.1f%% mem_peak=%.1fGiB waiting_max=%d",
				cfg.RunID[:8], gpuMetrics.UtilizationPeakPct, gpuMetrics.UtilizationAvgPct,
				gpuMetrics.MemoryPeakGiB, gpuMetrics.WaitingRequestsMax)
		} else {
			log.Printf("[%s] GPU scraper collected no samples", cfg.RunID[:8])
		}
	}

	if err != nil {
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("collect results: %w", err)
	}

	// Phase 6: Parse metrics and persist.
	log.Printf("[%s] collected %d bytes of loadgen output", cfg.RunID[:8], len(logData))
	output, err := metrics.ParseLoadgenOutput(logData)
	if err != nil {
		// Log a snippet of the raw data for debugging.
		snippet := logData
		if len(snippet) > 500 {
			snippet = append(logData[:250], []byte("\n...[truncated]...\n")...)
			snippet = append(snippet, logData[len(logData)-250:]...)
		}
		log.Printf("[%s] parse failed: %v\nlog snippet:\n%s", cfg.RunID[:8], err, snippet)
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("parse loadgen output: %w", err)
	}

	computed := metrics.ComputeMetrics(output)
	computed.RunID = cfg.RunID

	// Merge GPU scraper metrics into computed metrics.
	if gpuMetrics != nil {
		computed.AcceleratorUtilizationPct = &gpuMetrics.UtilizationPeakPct
		computed.AcceleratorUtilizationAvgPct = &gpuMetrics.UtilizationAvgPct
		computed.AcceleratorMemoryPeakGiB = &gpuMetrics.MemoryPeakGiB
		computed.WaitingRequestsMax = &gpuMetrics.WaitingRequestsMax
	}

	if err := o.repo.PersistMetrics(ctx, cfg.RunID, computed); err != nil {
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("persist metrics: %w", err)
	}

	if err := o.repo.UpdateRunStatus(ctx, cfg.RunID, "completed"); err != nil {
		return fmt.Errorf("update status to completed: %w", err)
	}

	log.Printf("[%s] benchmark completed successfully", cfg.RunID[:8])
	return nil
}

func (o *Orchestrator) deployModel(ctx context.Context, ns, name string, cfg RunConfig) error {
	// Reserve headroom for kubelet, kube-proxy, and OS overhead.
	// Request ~75% of instance vCPUs and ~85% of memory.
	vcpus := cfg.InstanceType.VCPUs
	memGiB := cfg.InstanceType.MemoryGiB
	cpuReq := fmt.Sprintf("%d", max(1, vcpus*3/4))
	memReq := fmt.Sprintf("%dGi", max(1, memGiB*85/100))

	yamlStr, err := manifest.RenderModelDeployment(manifest.ModelDeploymentParams{
		Name:                 name,
		Namespace:            ns,
		ModelHfID:            cfg.Request.ModelHfID,
		HfToken:              cfg.Request.HfToken,
		Framework:            cfg.Request.Framework,
		FrameworkVersion:     cfg.Request.FrameworkVersion,
		TensorParallelDegree: cfg.Request.TensorParallelDegree,
		Quantization:         derefStr(cfg.Request.Quantization),
		AcceleratorType:      cfg.InstanceType.AcceleratorType,
		AcceleratorCount:     cfg.InstanceType.AcceleratorCount,
		AcceleratorMemoryGiB: cfg.InstanceType.AcceleratorMemoryGiB,
		InstanceTypeName:     cfg.InstanceType.Name,
		InstanceFamily:       cfg.InstanceType.Family,
		MaxModelLen:          cfg.Request.MaxModelLen,
		CPURequest:           cpuReq,
		MemoryRequest:        memReq,
	})
	if err != nil {
		return err
	}

	return o.applyYAML(ctx, ns, yamlStr)
}

func (o *Orchestrator) waitForReady(ctx context.Context, ns, name string) error {
	deadline := time.Now().Add(readinessTimeout)
	for time.Now().Before(deadline) {
		dep, err := o.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if dep.Status.ReadyReplicas >= 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessPoll):
		}
	}
	return fmt.Errorf("model deployment %s not ready after %v", name, readinessTimeout)
}

func (o *Orchestrator) launchLoadgen(ctx context.Context, ns, name, modelSvc string, cfg RunConfig) error {
	numRequests := 200
	if cfg.Request.Concurrency > 32 {
		numRequests = cfg.Request.Concurrency * 10
	}

	loadgenImage := os.Getenv("LOADGEN_IMAGE")
	if loadgenImage == "" {
		loadgenImage = "ghcr.io/accelbench/loadgen:latest"
	}

	yamlStr, err := manifest.RenderLoadgenJob(manifest.LoadgenJobParams{
		Name:                 name,
		Namespace:            ns,
		LoadgenImage:         loadgenImage,
		TargetHost:           modelSvc,
		TargetPort:           8000,
		ModelHfID:            cfg.Request.ModelHfID,
		Concurrency:          cfg.Request.Concurrency,
		InputSequenceLength:  cfg.Request.InputSequenceLength,
		OutputSequenceLength: cfg.Request.OutputSequenceLength,
		DatasetName:          cfg.Request.DatasetName,
		NumRequests:          numRequests,
		WarmupRequests:       10,
		MinDurationSeconds:   cfg.Request.MinDurationSeconds,
	})
	if err != nil {
		return err
	}

	return o.applyYAML(ctx, ns, yamlStr)
}

func (o *Orchestrator) waitAndCollect(ctx context.Context, ns, jobName string) ([]byte, error) {
	deadline := time.Now().Add(jobTimeout)
	for time.Now().Before(deadline) {
		job, err := o.client.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return o.readJobLogs(ctx, ns, jobName)
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return nil, fmt.Errorf("loadgen job failed: %s", cond.Message)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jobPoll):
		}
	}
	return nil, fmt.Errorf("loadgen job %s timed out after %v", jobName, jobTimeout)
}

func (o *Orchestrator) readJobLogs(ctx context.Context, ns, jobName string) ([]byte, error) {
	pods, err := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return nil, fmt.Errorf("list job pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods found for job %s", jobName)
	}

	req := o.client.CoreV1().Pods(ns).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		Container: "loadgen",
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("stream pod logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return nil, fmt.Errorf("read pod logs: %w", err)
	}
	return buf.Bytes(), nil
}

func (o *Orchestrator) teardown(ctx context.Context, ns, modelName, loadgenName string) {
	log.Printf("tearing down resources: %s, %s", modelName, loadgenName)
	propagation := metav1.DeletePropagationBackground

	_ = o.client.BatchV1().Jobs(ns).Delete(ctx, loadgenName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	_ = o.client.CoreV1().Services(ns).Delete(ctx, modelName, metav1.DeleteOptions{})
	_ = o.client.AppsV1().Deployments(ns).Delete(ctx, modelName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
}

func (o *Orchestrator) markFailed(ctx context.Context, runID string) {
	if err := o.repo.UpdateRunStatus(ctx, runID, "failed"); err != nil {
		log.Printf("failed to mark run %s as failed: %v", runID, err)
	}
}

// applyYAML parses multi-document YAML and creates each resource.
func (o *Orchestrator) applyYAML(ctx context.Context, ns, yamlStr string) error {
	decoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(strings.NewReader(yamlStr)), 4096)
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode YAML: %w", err)
		}
		if len(raw) == 0 {
			continue
		}

		// Peek at kind to determine resource type.
		var meta struct{ Kind string }
		if err := json.Unmarshal(raw, &meta); err != nil {
			return fmt.Errorf("unmarshal kind: %w", err)
		}

		docJSON := string(raw)
		switch meta.Kind {
		case "Deployment":
			if err := o.createDeployment(ctx, ns, docJSON); err != nil {
				return err
			}
		case "Service":
			if err := o.createService(ctx, ns, docJSON); err != nil {
				return err
			}
		case "Job":
			if err := o.createJob(ctx, ns, docJSON); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported resource kind: %s", meta.Kind)
		}
	}
	return nil
}

func (o *Orchestrator) createDeployment(ctx context.Context, ns, docJSON string) error {
	var dep appsv1.Deployment
	if err := json.Unmarshal([]byte(docJSON), &dep); err != nil {
		return fmt.Errorf("decode deployment: %w", err)
	}
	_, err := o.client.AppsV1().Deployments(ns).Create(ctx, &dep, metav1.CreateOptions{})
	return err
}

func (o *Orchestrator) createService(ctx context.Context, ns, docJSON string) error {
	var svc corev1.Service
	if err := json.Unmarshal([]byte(docJSON), &svc); err != nil {
		return fmt.Errorf("decode service: %w", err)
	}
	_, err := o.client.CoreV1().Services(ns).Create(ctx, &svc, metav1.CreateOptions{})
	return err
}

func (o *Orchestrator) createJob(ctx context.Context, ns, docJSON string) error {
	var job batchv1.Job
	if err := json.Unmarshal([]byte(docJSON), &job); err != nil {
		return fmt.Errorf("decode job: %w", err)
	}
	_, err := o.client.BatchV1().Jobs(ns).Create(ctx, &job, metav1.CreateOptions{})
	return err
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
