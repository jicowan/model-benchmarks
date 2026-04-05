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
	"github.com/accelbench/accelbench/internal/oom"
	"github.com/accelbench/accelbench/internal/scenario"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

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
	client      kubernetes.Interface
	repo        database.Repo
	oomDetector *oom.Detector
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc // runID → cancel
}

// New creates a new Orchestrator.
func New(client kubernetes.Interface, repo database.Repo) *Orchestrator {
	return &Orchestrator{
		client:      client,
		repo:        repo,
		oomDetector: oom.NewDetector(client, defaultNamespace),
		cancels:     make(map[string]context.CancelFunc),
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
	configMapName := fmt.Sprintf("loadgen-config-%s", cfg.RunID[:8])

	// Phase 1: Mark run as running.
	if err := o.repo.UpdateRunStatus(ctx, cfg.RunID, "running"); err != nil {
		return fmt.Errorf("update status to running: %w", err)
	}

	// Ensure teardown happens regardless of outcome.
	defer o.teardown(context.Background(), ns, modelName, loadgenName, configMapName)

	// Phase 2: Deploy model Deployment + Service.
	log.Printf("[%s] deploying model %s on %s", cfg.RunID[:8], cfg.Request.ModelHfID, cfg.Request.InstanceTypeName)
	if err := o.deployModel(ctx, ns, modelName, cfg); err != nil {
		o.markFailed(ctx, cfg.RunID)
		return fmt.Errorf("deploy model: %w", err)
	}

	// Phase 3: Wait for readiness.
	log.Printf("[%s] waiting for model readiness", cfg.RunID[:8])
	if err := o.waitForReady(ctx, ns, modelName, cfg); err != nil {
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
	logData, err := o.waitAndCollect(ctx, ns, loadgenName, cfg.RunID)

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

		// Extended metrics (PRD-14)
		computed.PromptThroughputTPS = &gpuMetrics.PromptThroughputTPS
		computed.GenerationThroughputTPS = &gpuMetrics.GenerationThroughputTPS
		computed.KVCacheUtilizationAvgPct = &gpuMetrics.KVCacheUtilizationAvgPct
		computed.KVCacheUtilizationPeakPct = &gpuMetrics.KVCacheUtilizationPeakPct
		computed.PrefixCacheHitRate = &gpuMetrics.PrefixCacheHitRate
		computed.PreemptionCount = &gpuMetrics.PreemptionCount
		computed.RunningRequestsAvg = &gpuMetrics.RunningRequestsAvg
		computed.RunningRequestsMax = &gpuMetrics.RunningRequestsMax
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

func (o *Orchestrator) waitForReady(ctx context.Context, ns, name string, cfg RunConfig) error {
	deadline := time.Now().Add(readinessTimeout)
	for time.Now().Before(deadline) {
		dep, err := o.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if dep.Status.ReadyReplicas >= 1 {
			return nil
		}

		// Check for OOM events on pods belonging to this deployment
		pods, _ := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		for _, pod := range pods.Items {
			events, err := o.oomDetector.CheckPod(ctx, pod.Name)
			if err == nil && len(events) > 0 {
				// Record OOM event and fail immediately
				for _, ev := range events {
					o.recordOOMEvent(ctx, cfg, ev)
				}
				return fmt.Errorf("OOM detected: %s", events[0].Message)
			}
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
	configMapName := fmt.Sprintf("loadgen-config-%s", cfg.RunID[:8])

	// Build inference-perf config from scenario or compute defaults
	var inferencePerfConfig manifest.InferencePerfConfigParams

	if cfg.Request.ScenarioID != "" {
		// Use predefined scenario
		s := scenario.Get(cfg.Request.ScenarioID)
		if s == nil {
			return fmt.Errorf("unknown scenario: %s", cfg.Request.ScenarioID)
		}
		inferencePerfConfig = s.ToInferencePerfConfig(cfg.Request.ModelHfID, modelSvc, 8000)
		log.Printf("[%s] using scenario %q: %s", cfg.RunID[:8], s.ID, s.Name)
	} else {
		// Fall back to computed defaults based on request parameters
		inputMean := cfg.Request.InputSequenceLength
		if inputMean == 0 {
			inputMean = 256
		}
		outputMean := cfg.Request.OutputSequenceLength
		if outputMean == 0 {
			outputMean = 128
		}

		// Calculate distribution bounds (std_dev = mean/4, min = mean/2, max = mean*2)
		inputStdDev := inputMean / 4
		inputMin := inputMean / 2
		inputMax := inputMean * 2
		outputStdDev := outputMean / 4
		outputMin := outputMean / 2
		outputMax := outputMean * 2

		// Duration from request or default 120 seconds
		duration := cfg.Request.MinDurationSeconds
		if duration == 0 {
			duration = 120
		}

		// Workers based on concurrency
		numWorkers := cfg.Request.Concurrency
		if numWorkers < 4 {
			numWorkers = 4
		}
		if numWorkers > 8 {
			numWorkers = 8
		}

		// Calculate QPS from concurrency
		qps := cfg.Request.Concurrency / 2
		if qps < 1 {
			qps = 1
		}
		if qps > 50 {
			qps = 50
		}

		inferencePerfConfig = manifest.InferencePerfConfigParams{
			ModelHfID:    cfg.Request.ModelHfID,
			TargetHost:   modelSvc,
			TargetPort:   8000,
			Streaming:    true,
			DatasetType:  "synthetic",
			InputMean:    inputMean,
			InputStdDev:  inputStdDev,
			InputMin:     inputMin,
			InputMax:     inputMax,
			OutputMean:   outputMean,
			OutputStdDev: outputStdDev,
			OutputMin:    outputMin,
			OutputMax:    outputMax,
			LoadType:     "constant",
			Stages:       []manifest.LoadStage{{Rate: qps, Duration: duration}},
			NumWorkers:   numWorkers,
		}
	}

	configYAML, err := manifest.RenderInferencePerfConfig(inferencePerfConfig)
	if err != nil {
		return fmt.Errorf("render inference-perf config: %w", err)
	}

	// Store the config in the database for reproducibility
	if err := o.repo.UpdateLoadgenConfig(ctx, cfg.RunID, configYAML); err != nil {
		log.Printf("[%s] warning: failed to save loadgen config: %v", cfg.RunID[:8], err)
		// Non-fatal - continue with the benchmark
	}

	// Create ConfigMap with inference-perf config
	if err := o.createConfigMap(ctx, ns, configMapName, "config.yml", configYAML); err != nil {
		return fmt.Errorf("create configmap: %w", err)
	}

	inferencePerfImage := os.Getenv("INFERENCE_PERF_IMAGE")
	if inferencePerfImage == "" {
		inferencePerfImage = "quay.io/inference-perf/inference-perf:v0.2.0"
	}

	// Use S3 for results to avoid container log truncation
	resultsBucket := os.Getenv("RESULTS_S3_BUCKET")
	resultsKey := ""
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = "us-east-2"
	}
	if resultsBucket != "" {
		resultsKey = fmt.Sprintf("results/%s.json", cfg.RunID)
	}

	yamlStr, err := manifest.RenderLoadgenJob(manifest.LoadgenJobParams{
		Name:               name,
		Namespace:          ns,
		InferencePerfImage: inferencePerfImage,
		ConfigMapName:      configMapName,
		ResultsS3Bucket:    resultsBucket,
		ResultsS3Key:       resultsKey,
		AWSRegion:          awsRegion,
	})
	if err != nil {
		return err
	}

	return o.applyYAML(ctx, ns, yamlStr)
}

func (o *Orchestrator) waitAndCollect(ctx context.Context, ns, jobName, runID string) ([]byte, error) {
	deadline := time.Now().Add(jobTimeout)
	for time.Now().Before(deadline) {
		job, err := o.client.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				// Try S3 first, fall back to logs
				if bucket := os.Getenv("RESULTS_S3_BUCKET"); bucket != "" {
					key := fmt.Sprintf("results/%s.json", runID)
					data, err := o.readResultsFromS3(ctx, bucket, key)
					if err == nil {
						return data, nil
					}
					log.Printf("[%s] S3 read failed, falling back to logs: %v", runID[:8], err)
				}
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

func (o *Orchestrator) readResultsFromS3(ctx context.Context, bucket, key string) ([]byte, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg)
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	defer result.Body.Close()

	return io.ReadAll(result.Body)
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
		Container: "inference-perf",
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

func (o *Orchestrator) teardown(ctx context.Context, ns, modelName, loadgenName, configMapName string) {
	log.Printf("tearing down resources: %s, %s, %s", modelName, loadgenName, configMapName)
	propagation := metav1.DeletePropagationBackground

	_ = o.client.BatchV1().Jobs(ns).Delete(ctx, loadgenName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	_ = o.client.CoreV1().Services(ns).Delete(ctx, modelName, metav1.DeleteOptions{})
	_ = o.client.AppsV1().Deployments(ns).Delete(ctx, modelName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	// Delete the inference-perf config ConfigMap
	if configMapName != "" {
		_ = o.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
	}
}

// createConfigMap creates a ConfigMap with the given data.
func (o *Orchestrator) createConfigMap(ctx context.Context, ns, name, key, data string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/component": "loadgen-config",
				"accelbench/role":             "loadgen-config",
			},
		},
		Data: map[string]string{
			key: data,
		},
	}
	_, err := o.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	return err
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

// RecoverOrphanedRuns checks for runs stuck in "running" status and attempts
// to complete them by fetching results from S3. This handles cases where the
// API restarted while a benchmark was in progress.
func (o *Orchestrator) RecoverOrphanedRuns(ctx context.Context) {
	runs, err := o.repo.GetRunsByStatus(ctx, "running")
	if err != nil {
		log.Printf("[recovery] failed to query running runs: %v", err)
		return
	}

	if len(runs) == 0 {
		log.Printf("[recovery] no orphaned runs found")
		return
	}

	log.Printf("[recovery] found %d orphaned run(s)", len(runs))

	bucket := os.Getenv("RESULTS_S3_BUCKET")
	if bucket == "" {
		log.Printf("[recovery] RESULTS_S3_BUCKET not set, marking runs as failed")
		for _, run := range runs {
			o.markFailed(ctx, run.ID)
			o.cleanupResources(ctx, run.ID)
		}
		return
	}

	for _, run := range runs {
		o.recoverRun(ctx, bucket, run.ID)
	}
}

func (o *Orchestrator) recoverRun(ctx context.Context, bucket, runID string) {
	shortID := runID[:8]
	log.Printf("[recovery] attempting to recover run %s", shortID)

	// Try to fetch results from S3
	key := fmt.Sprintf("results/%s.json", runID)
	data, err := o.readResultsFromS3(ctx, bucket, key)
	if err != nil {
		log.Printf("[recovery] %s: no S3 results found, marking as failed: %v", shortID, err)
		o.markFailed(ctx, runID)
		o.cleanupResources(ctx, runID)
		return
	}

	log.Printf("[recovery] %s: found S3 results (%d bytes), processing", shortID, len(data))

	// Parse and persist metrics
	output, err := metrics.ParseLoadgenOutput(data)
	if err != nil {
		log.Printf("[recovery] %s: failed to parse results: %v", shortID, err)
		o.markFailed(ctx, runID)
		o.cleanupResources(ctx, runID)
		return
	}

	computed := metrics.ComputeMetrics(output)
	computed.RunID = runID
	// Note: GPU metrics are lost since the scraper was killed

	if err := o.repo.PersistMetrics(ctx, runID, computed); err != nil {
		log.Printf("[recovery] %s: failed to persist metrics: %v", shortID, err)
		o.markFailed(ctx, runID)
		o.cleanupResources(ctx, runID)
		return
	}

	log.Printf("[recovery] %s: successfully recovered and completed", shortID)
	o.cleanupResources(ctx, runID)
}

func (o *Orchestrator) cleanupResources(ctx context.Context, runID string) {
	ns := defaultNamespace
	modelName := fmt.Sprintf("bench-%s", runID[:8])
	loadgenName := fmt.Sprintf("loadgen-%s", runID[:8])
	configMapName := fmt.Sprintf("loadgen-config-%s", runID[:8])
	o.teardown(ctx, ns, modelName, loadgenName, configMapName)
}

// recordOOMEvent saves an OOM event to the database.
func (o *Orchestrator) recordOOMEvent(ctx context.Context, cfg RunConfig, ev oom.Event) {
	dbEvent := &database.OOMEvent{
		RunID:                cfg.RunID,
		ModelHfID:            cfg.Request.ModelHfID,
		InstanceType:         cfg.Request.InstanceTypeName,
		PodName:              ev.PodName,
		ContainerName:        ev.ContainerName,
		DetectionMethod:      ev.DetectionMethod,
		ExitCode:             ev.ExitCode,
		Message:              ev.Message,
		OccurredAt:           ev.OccurredAt,
		TensorParallelDegree: cfg.Request.TensorParallelDegree,
		Concurrency:          cfg.Request.Concurrency,
		MaxModelLen:          cfg.Request.MaxModelLen,
		Quantization:         derefStr(cfg.Request.Quantization),
	}

	if err := o.repo.CreateOOMEvent(ctx, dbEvent); err != nil {
		log.Printf("[%s] failed to record OOM event: %v", cfg.RunID[:8], err)
	} else {
		log.Printf("[%s] recorded OOM event: %s", cfg.RunID[:8], ev.Message)
	}
}
