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
// HFTokenResolver is satisfied by any store that can return the platform
// HuggingFace token (empty string = not configured).
type HFTokenResolver interface {
	GetHFToken(ctx context.Context) (string, error)
}

type Orchestrator struct {
	client      kubernetes.Interface
	repo        database.Repo
	oomDetector *oom.Detector
	secrets     HFTokenResolver // optional; nil = no auto-injection
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc // runID → cancel
	// PRD-40: this pod's hostname. Written into benchmark_runs.owner_pod +
	// test_suite_runs.owner_pod when Execute starts so orphan recovery on
	// sibling pods can attribute ownership.
	hostname string
}

// New creates a new Orchestrator.
func New(client kubernetes.Interface, repo database.Repo, hostname string) *Orchestrator {
	return &Orchestrator{
		client:      client,
		repo:        repo,
		oomDetector: oom.NewDetector(client, defaultNamespace),
		cancels:     make(map[string]context.CancelFunc),
		hostname:    hostname,
	}
}

// SetSecretsStore enables HF token auto-injection. Called from the API server
// after construction; leaving it unset falls back to per-request tokens only.
func (o *Orchestrator) SetSecretsStore(s HFTokenResolver) {
	o.secrets = s
}

// resolveHFToken returns the per-request token when set, otherwise the
// platform token from Secrets Manager, otherwise "". Errors fetching the
// platform token are logged and swallowed — gated models will fail at HF
// with a clearer 401 than a Secrets Manager error.
func (o *Orchestrator) resolveHFToken(ctx context.Context, perRequest string) string {
	if perRequest != "" {
		return perRequest
	}
	if o.secrets == nil {
		return ""
	}
	tok, err := o.secrets.GetHFToken(ctx)
	if err != nil {
		log.Printf("resolve platform HF token: %v (proceeding without token)", err)
		return ""
	}
	return tok
}

// resolveScenario returns the code-defined scenario with any DB-stored
// per-scenario overrides (PRD-32) merged in. Returns nil if the scenario
// ID is unknown.
func (o *Orchestrator) resolveScenario(ctx context.Context, id string) *scenario.Scenario {
	code := scenario.Get(id)
	if code == nil {
		return nil
	}
	ov, err := o.repo.GetScenarioOverride(ctx, id)
	if err != nil {
		log.Printf("load scenario override for %s: %v (using code defaults)", id, err)
		return code
	}
	if ov == nil {
		return code
	}
	return code.Merge(&scenario.Override{
		NumWorkers: ov.NumWorkers,
		Streaming:  ov.Streaming,
		InputMean:  ov.InputMean,
		OutputMean: ov.OutputMean,
	})
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

	// PRD-40: claim ownership so orphan recovery on sibling pods leaves this
	// run alone, and start a background poller that watches for cross-pod
	// cancel requests via the cancel_requested DB flag.
	if err := o.repo.ClaimRun(ctx, cfg.RunID, o.hostname); err != nil {
		log.Printf("[%s] claim run: %v", cfg.RunID[:8], err)
	}
	o.startCancelPoller(ctx, cfg.RunID, cancel)

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
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("deploy model: %v", err))
		return fmt.Errorf("deploy model: %w", err)
	}

	// PRD-47: capture peak host memory during the load phase so PR #5
	// can calibrate per-family multipliers. Starts now, stops on
	// readiness (success or failure). Best-effort: a failed scrape
	// leaves host_memory_peak_gib NULL and the run continues.
	hostMemScraper := NewHostMemScraper(o.client, ns,
		fmt.Sprintf("app.kubernetes.io/name=%s", modelName), "vllm")
	hostMemScraper.Start(ctx)

	// Phase 3: Wait for readiness.
	log.Printf("[%s] waiting for model readiness", cfg.RunID[:8])
	readyErr := o.waitForReady(ctx, ns, modelName, cfg)

	// Stop the scraper now (load phase is over). Persist whatever peak
	// we captured even on readiness failure — OOMs produce the most
	// useful calibration signal.
	if peakGiB := hostMemScraper.Stop(); peakGiB > 0 {
		if err := o.repo.SetRunHostMemoryPeak(context.Background(), cfg.RunID, peakGiB); err != nil {
			log.Printf("[%s] warning: persist host memory peak: %v", cfg.RunID[:8], err)
		} else {
			log.Printf("[%s] load-phase host memory peak: %.2f GiB", cfg.RunID[:8], peakGiB)
		}
	}

	if readyErr != nil {
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("model not ready: %v", readyErr))
		return fmt.Errorf("wait for readiness: %w", readyErr)
	}

	// Start GPU scraper for GPU instances (non-fatal if it fails).
	var gpuScraper *GPUScraper
	if strings.EqualFold(cfg.InstanceType.AcceleratorType, "gpu") {
		totalMemGiB := float64(cfg.InstanceType.AcceleratorMemoryGiB)
		// Try to get node IP for DCGM metrics
		nodeIP := o.getModelPodNodeIP(ctx, ns, modelName)
		if nodeIP != "" {
			log.Printf("[%s] DCGM scraping enabled (node %s)", cfg.RunID[:8], nodeIP)
		}
		gpuScraper = NewGPUScraperWithDCGM(modelName, 8000, totalMemGiB, nodeIP)
		gpuScraper.Start(ctx)
		log.Printf("[%s] started GPU metrics scraper", cfg.RunID[:8])
	}

	// Phase 4: Launch load generator Job.
	log.Printf("[%s] launching load generator", cfg.RunID[:8])
	if err := o.repo.SetLoadgenStartedAt(ctx, cfg.RunID); err != nil {
		log.Printf("[%s] warning: failed to set loadgen_started_at: %v", cfg.RunID[:8], err)
	}
	if err := o.launchLoadgen(ctx, ns, loadgenName, modelName, cfg); err != nil {
		if gpuScraper != nil {
			gpuScraper.Stop()
		}
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("launch loadgen: %v", err))
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
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("collect results: %v", err))
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
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("parse loadgen output: %v", err))
		return fmt.Errorf("parse loadgen output: %w", err)
	}

	computed := metrics.ComputeMetrics(output)
	computed.RunID = cfg.RunID

	// Merge GPU scraper metrics into computed metrics.
	if gpuMetrics != nil {
		computed.AcceleratorUtilizationPct = &gpuMetrics.UtilizationPeakPct
		computed.AcceleratorUtilizationAvgPct = &gpuMetrics.UtilizationAvgPct
		computed.AcceleratorMemoryPeakGiB = &gpuMetrics.MemoryPeakGiB
		computed.AcceleratorMemoryAvgGiB = &gpuMetrics.MemoryAvgGiB
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

		// PRD-22: DCP metrics pass through as pointers. Nil → stored as NULL
		// → UI renders "—". Non-nil carries a real reading.
		computed.SMActiveAvgPct = gpuMetrics.SMActiveAvgPct
		computed.SMActivePeakPct = gpuMetrics.SMActivePeakPct
		computed.TensorActiveAvgPct = gpuMetrics.TensorActiveAvgPct
		computed.TensorActivePeakPct = gpuMetrics.TensorActivePeakPct
		computed.DRAMActiveAvgPct = gpuMetrics.DRAMActiveAvgPct
		computed.DRAMActivePeakPct = gpuMetrics.DRAMActivePeakPct
	}

	if err := o.repo.PersistMetrics(ctx, cfg.RunID, computed); err != nil {
		o.markFailed(ctx, cfg.RunID, fmt.Sprintf("persist metrics: %v", err))
		return fmt.Errorf("persist metrics: %w", err)
	}

	if err := o.repo.UpdateRunStatus(ctx, cfg.RunID, "completed"); err != nil {
		return fmt.Errorf("update status to completed: %w", err)
	}

	// PRD-35: freeze cost now that completed_at is set. Failure here never
	// blocks the run — the cost columns just stay NULL and the run is still
	// displayed, just without a cost overline.
	totalUSD, loadgenUSD := o.computeRunCost(ctx, cfg.RunID)
	if err := o.repo.UpdateRunCost(ctx, cfg.RunID, totalUSD, loadgenUSD); err != nil {
		log.Printf("[%s] update run cost: %v", cfg.RunID[:8], err)
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

	var modelS3URI string
	var useRunai bool
	if cfg.Request.ModelS3URI != "" {
		modelS3URI = cfg.Request.ModelS3URI
		useRunai = true
		log.Printf("[%s] using S3 model: %s", cfg.RunID[:8], modelS3URI)
	}

	if modelS3URI == "" && cfg.Request.ModelHfID != "" {
		revision := cfg.Request.ModelHfRevision
		if revision == "" {
			revision = "main"
		}
		cached, _ := o.repo.GetModelCacheByHfID(ctx, cfg.Request.ModelHfID, revision)
		if cached != nil && cached.Status == "cached" {
			modelS3URI = cached.S3URI
			useRunai = true
			log.Printf("[%s] auto-detected cached model: %s", cfg.RunID[:8], modelS3URI)
		}
	}

	// PRD-50: streamer_mode=off forces the default loader even for S3
	// models, so vLLM pulls weights through its own loader without the
	// Run:ai streamer's shared CPU buffer. Useful when host RAM is too
	// tight for the streamer's buffer to fit alongside vLLM.
	if cfg.Request.StreamerMode == "off" {
		useRunai = false
		log.Printf("[%s] streamer_mode=off; skipping Run:ai streamer even for S3-backed model", cfg.RunID[:8])
	}

	// PRD-50: concurrency knob. 0 = default (16, matching the upstream
	// RUNAI_STREAMER_CONCURRENCY default on filesystem / was our
	// hardcode before this PRD).
	streamerConcurrency := cfg.Request.StreamerConcurrency
	if streamerConcurrency == 0 {
		streamerConcurrency = 16
	}

	// PRD-50: memory-limit knob. 0 = auto-size at half the node RAM.
	// min(weight, instance_mem/2) isn't computed here — we let the
	// streamer itself cap against the weight file size by passing the
	// instance-based cap as RUNAI_STREAMER_MEMORY_LIMIT. Zero on the
	// rendered env means "emit no env var; use the upstream default".
	streamerMemLimitGiB := cfg.Request.StreamerMemoryLimitGiB
	if streamerMemLimitGiB == 0 {
		streamerMemLimitGiB = max(1, cfg.InstanceType.MemoryGiB/2)
	}

	var modelServiceAccount string
	if useRunai {
		modelServiceAccount = "accelbench-model"
	}

	yamlStr, err := manifest.RenderModelDeployment(manifest.ModelDeploymentParams{
		Name:                 name,
		Namespace:            ns,
		ModelHfID:            cfg.Request.ModelHfID,
		HfToken:              o.resolveHFToken(ctx, cfg.Request.HfToken),
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
		MaxNumBatchedTokens:  cfg.Request.MaxNumBatchedTokens,
		// PRD-51: don't emit --max-num-seqs. Wiring it to the form's
		// concurrency field was wrong for open-loop scenarios where
		// steady-state in-flight count is `rate × latency`, not a
		// closed-loop worker count. Letting vLLM use its upstream
		// default of 256 works for both open- and closed-loop loads;
		// KV cache stays the binding constraint via PRD-47's math.
		MaxNumSeqs:   0,
		KVCacheDtype: cfg.Request.KVCacheDtype,
		CPURequest:           cpuReq,
		MemoryRequest:        memReq,
		ModelS3URI:           modelS3URI,
		UseRunaiStreamer:        useRunai,
		ModelServiceAccount:     modelServiceAccount,
		StreamerConcurrency:     streamerConcurrency,
		StreamerMemoryLimitGiB:  streamerMemLimitGiB,
		PullThroughRegistry:     os.Getenv("PULL_THROUGH_REGISTRY"),
		VLLMImageOverride:       ResolveVLLMImageOverride(),
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

	// PRD-42: every run must reference a scenario. The API rejects
	// scenario-less submissions at create time, so this is the only
	// code path.
	if cfg.Request.ScenarioID == "" {
		return fmt.Errorf("scenario_id is required")
	}
	s := o.resolveScenario(ctx, cfg.Request.ScenarioID)
	if s == nil {
		return fmt.Errorf("unknown scenario: %s", cfg.Request.ScenarioID)
	}
	inferencePerfConfig := s.ToInferencePerfConfig(cfg.Request.ModelHfID, modelSvc, 8000)
	log.Printf("[%s] using scenario %q: %s", cfg.RunID[:8], s.ID, s.Name)

	// Allow dataset override from request
	if cfg.Request.DatasetName != "" {
		inferencePerfConfig.DatasetType = cfg.Request.DatasetName
		log.Printf("[%s] dataset override: %s", cfg.RunID[:8], cfg.Request.DatasetName)
	}

	// Set API type: explicit override from request > infer from final dataset
	// (Re-inferring here ensures dataset overrides on scenarios also update the API type)
	if cfg.Request.APIType != "" {
		inferencePerfConfig.APIType = cfg.Request.APIType
	} else {
		switch inferencePerfConfig.DatasetType {
		case "synthetic", "random":
			inferencePerfConfig.APIType = "completion"
		default:
			inferencePerfConfig.APIType = "chat"
		}
	}

	// When loading from S3, vLLM registers the model with the S3 URI as its name
	if cfg.Request.ModelS3URI != "" {
		inferencePerfConfig.ModelName = cfg.Request.ModelS3URI
	} else if cfg.Request.ModelHfID != "" {
		revision := cfg.Request.ModelHfRevision
		if revision == "" {
			revision = "main"
		}
		if cached, _ := o.repo.GetModelCacheByHfID(ctx, cfg.Request.ModelHfID, revision); cached != nil && cached.Status == "cached" {
			inferencePerfConfig.ModelName = cached.S3URI
		}
	}

	// Results storage. inference-perf writes directly to S3 via boto3 when
	// storage.simple_storage_service is configured. Layout:
	//   s3://<bucket>/results/<run_id>/<run_id>_summary.json
	// The orchestrator reads back the summary file from the same prefix
	// in waitAndCollect.
	resultsBucket := os.Getenv("RESULTS_S3_BUCKET")
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = "us-east-2"
	}
	if resultsBucket != "" {
		inferencePerfConfig.StorageBucket = resultsBucket
		inferencePerfConfig.StoragePath = fmt.Sprintf("results/%s/", cfg.RunID)
		inferencePerfConfig.StorageReportPrefix = cfg.RunID
		inferencePerfConfig.StorageRegion = awsRegion
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

	inferencePerfImage, err := o.resolveInferencePerfImage(ctx)
	if err != nil {
		return fmt.Errorf("resolve inference-perf image: %w", err)
	}

	// Scale the loadgen container's CPU/memory with num_workers so large
	// worker counts don't strangle themselves at the historical 4-CPU
	// limit. Karpenter's general-purpose NodePool will auto-provision a
	// larger m6i instance if the request exceeds existing system nodes'
	// free capacity.
	cpuReq, cpuLim, memReq, memLim := loadgenResources(inferencePerfConfig.NumWorkers)

	yamlStr, err := manifest.RenderLoadgenJob(manifest.LoadgenJobParams{
		Name:               name,
		Namespace:          ns,
		InferencePerfImage: inferencePerfImage,
		ConfigMapName:      configMapName,
		AWSRegion:          awsRegion,
		HfToken:            o.resolveHFToken(ctx, cfg.Request.HfToken),
		CPURequest:         cpuReq,
		CPULimit:           cpuLim,
		MemoryRequest:      memReq,
		MemoryLimit:        memLim,
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
				// Try S3 first, fall back to logs. inference-perf writes
				// several files under results/<run_id>/; we want the
				// *_summary.json file (or any .json if that doesn't match,
				// as a defensive fallback for naming drift).
				if bucket := os.Getenv("RESULTS_S3_BUCKET"); bucket != "" {
					prefix := fmt.Sprintf("results/%s/", runID)
					data, err := o.readResultsFromS3Prefix(ctx, bucket, prefix, runID)
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

// readResultsFromS3Prefix lists objects under the given prefix and returns
// the contents of the summary report file. inference-perf (v0.2.0) names
// uploaded files as `<path>/<report_file_prefix><filename>`, where
// `filename` is chosen by the report writer (e.g. "summary.json",
// "request_stats.json"). With our config the summary lands at
// `results/<runID>/<runID>summary.json`. We scan for any file containing
// "summary" with a .json suffix (robust to upstream naming changes), and
// fall back to the first .json file found if nothing matches.
func (o *Orchestrator) readResultsFromS3Prefix(ctx context.Context, bucket, prefix, runID string) ([]byte, error) {
	_ = runID // reserved for future exact-match probing
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(cfg)

	listOut, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})
	if err != nil {
		return nil, fmt.Errorf("list s3://%s/%s: %w", bucket, prefix, err)
	}
	if len(listOut.Contents) == 0 {
		return nil, fmt.Errorf("no objects under s3://%s/%s", bucket, prefix)
	}

	var pickedKey string
	for _, obj := range listOut.Contents {
		if obj.Key == nil {
			continue
		}
		if strings.Contains(*obj.Key, "summary") && strings.HasSuffix(*obj.Key, ".json") {
			pickedKey = *obj.Key
			break
		}
	}
	if pickedKey == "" {
		for _, obj := range listOut.Contents {
			if obj.Key != nil && strings.HasSuffix(*obj.Key, ".json") {
				pickedKey = *obj.Key
				break
			}
		}
	}
	if pickedKey == "" {
		return nil, fmt.Errorf("no .json result objects under s3://%s/%s", bucket, prefix)
	}

	result, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &pickedKey})
	if err != nil {
		return nil, fmt.Errorf("get s3://%s/%s: %w", bucket, pickedKey, err)
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

func (o *Orchestrator) markFailed(ctx context.Context, runID, reason string) {
	// PRD-40: the caller's ctx is frequently the run's context, which may
	// be cancelled by the time we arrive here (cross-pod cancel, client
	// disconnect, etc.). Terminal-state writes must succeed anyway — use a
	// detached context with a short timeout so they don't hang.
	bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := o.repo.UpdateRunFailed(bgCtx, runID, reason); err != nil {
		log.Printf("failed to mark run %s as failed: %v", runID, err)
		return
	}
	// PRD-35: freeze cost on failure too. The node existed from started_at
	// until markFailed set completed_at, so the time is billable.
	totalUSD, loadgenUSD := o.computeRunCost(bgCtx, runID)
	if err := o.repo.UpdateRunCost(bgCtx, runID, totalUSD, loadgenUSD); err != nil {
		log.Printf("update failed run cost %s: %v", runID, err)
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
			o.markFailed(ctx, run.ID, "orphaned run: API restarted and no S3 bucket configured for recovery")
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

	// Try to fetch results from S3. inference-perf now writes to
	// s3://<bucket>/results/<runID>/<runID>_summary.json (a prefix with
	// several files); the helper lists the prefix and picks the summary.
	prefix := fmt.Sprintf("results/%s/", runID)
	data, err := o.readResultsFromS3Prefix(ctx, bucket, prefix, runID)
	if err != nil {
		log.Printf("[recovery] %s: no S3 results found, marking as failed: %v", shortID, err)
		o.markFailed(ctx, runID, fmt.Sprintf("orphaned run: no S3 results found (%v)", err))
		o.cleanupResources(ctx, runID)
		return
	}

	log.Printf("[recovery] %s: found S3 results (%d bytes), processing", shortID, len(data))

	// Parse and persist metrics
	output, err := metrics.ParseLoadgenOutput(data)
	if err != nil {
		log.Printf("[recovery] %s: failed to parse results: %v", shortID, err)
		o.markFailed(ctx, runID, fmt.Sprintf("orphaned run: failed to parse S3 results (%v)", err))
		o.cleanupResources(ctx, runID)
		return
	}

	computed := metrics.ComputeMetrics(output)
	computed.RunID = runID
	// Note: GPU metrics are lost since the scraper was killed

	if err := o.repo.PersistMetrics(ctx, runID, computed); err != nil {
		log.Printf("[recovery] %s: failed to persist metrics: %v", shortID, err)
		o.markFailed(ctx, runID, fmt.Sprintf("orphaned run: failed to persist recovered metrics (%v)", err))
		o.cleanupResources(ctx, runID)
		return
	}

	log.Printf("[recovery] %s: successfully recovered and completed", shortID)
	o.cleanupResources(ctx, runID)
}

func (o *Orchestrator) cleanupResources(ctx context.Context, runID string) {
	// PRD-40: if the caller's ctx is cancelled, Kubernetes Delete calls
	// bail out before hitting the API server and resources leak. Use a
	// detached context with a generous timeout instead; teardown is best-
	// effort and idempotent.
	_ = ctx // kept for compatibility with callers that expect a ctx param
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ns := defaultNamespace
	modelName := fmt.Sprintf("bench-%s", runID[:8])
	loadgenName := fmt.Sprintf("loadgen-%s", runID[:8])
	configMapName := fmt.Sprintf("loadgen-config-%s", runID[:8])
	o.teardown(bgCtx, ns, modelName, loadgenName, configMapName)
}

// getModelPodNodeIP returns the node IP where the model pod is running.
// Returns empty string if the pod is not found or node IP cannot be determined.
func (o *Orchestrator) getModelPodNodeIP(ctx context.Context, ns, deploymentName string) string {
	// List pods for this deployment
	pods, err := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", deploymentName),
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}

	// Get the first running pod's node IP
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.Status.HostIP != "" {
			return pod.Status.HostIP
		}
	}
	return ""
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
