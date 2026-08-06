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
	"github.com/accelbench/accelbench/internal/runtime"
	"github.com/accelbench/accelbench/internal/scenario"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
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

	// PRD-56: multi-node topology. Lives on RunConfig (the in-memory
	// execution config), NOT on database.RunRequest — persisting it is
	// PRD-57's job. Zero values mean a single-node run (the default,
	// byte-for-byte the existing path). Set only when the run selects the
	// "llm-d" framework; a hand-built RunConfig exercises this in PRD-56.
	//
	// NodeCount is the LeaderWorkerSet group size (number of GPU nodes).
	// PipelineParallelDegree is pipeline shards across nodes; TP within a
	// node comes from Request.TensorParallelDegree. GPUsPerNode defaults to
	// the instance's accelerator count when zero.
	NodeCount              int
	PipelineParallelDegree int
	GPUsPerNode            int

	// NodePoolOverride pins the run to a specific static multinode NodePool
	// (e.g. "multinode-us-east-2a"). Empty = auto-select, preferring pools
	// backed by a capacity reservation / Capacity Block (see
	// selectMultinodePool). PRD-57's UI can surface this so a user picks a
	// pool/AZ explicitly.
	NodePoolOverride string

	// NetworkMode selects the cross-node collective fabric for a distributed
	// run: NetworkModeEFA (default, preferred — EFA/RDMA) or NetworkModeTCP
	// (NCCL over plain sockets, no EFA devices claimed). TCP lets users
	// benchmark multi-node on GPU instances that lack EFA (or when EFA
	// capacity is unavailable), at a throughput cost. Empty ⇒ EFA.
	NetworkMode string

	// PRD-58: prefill/decode disaggregation. Set only when the run is
	// disaggregated (Request.DeploymentMode == "disaggregated"). Per-role
	// replica counts (the xPyD ratio) + within-node TP. Each pod occupies one
	// node (TP is within-node, per-role PP is fixed at 1), so the run's total
	// node count is PrefillReplicas + DecodeReplicas — set into NodeCount by the
	// caller so the shared pool-acquire / scale / teardown / cost / DCGM paths
	// (which key on NodeCount) work unchanged.
	PrefillReplicas int
	PrefillTP       int
	DecodeReplicas  int
	DecodeTP        int
	// PRD-63: optional co-located "both" pool (prefill+decode fused). 0 ⇒ no
	// both pool (today's PD behavior). NodeCount includes BothReplicas so the
	// shared pool-acquire / scale / teardown / cost paths key on the total.
	BothReplicas int
	BothTP       int
	// PRD-64: optional per-role scheduler override (0 ⇒ inherit the shared
	// Request.MaxNumBatchedTokens). Prefill compute-bound wants a larger budget;
	// decode memory-bound. Only consulted for disaggregated runs. PRD-63 adds
	// the symmetric knob for the "both" role.
	PrefillMaxNumBatchedTokens int
	DecodeMaxNumBatchedTokens  int
	BothMaxNumBatchedTokens    int

	// PRD-61: run-tunable EPP routing config (disaggregated only). Each nil/0 ⇒
	// the orchestrator applies the shipped default, so a run that sets nothing is
	// byte-identical to pre-PRD-61. NonCachedTokens is a POINTER because 0 is a
	// MEANINGFUL value (disable disaggregation) that must be distinguishable from
	// "unset" — the others use 0 = unset since 0 is not a valid weight/size.
	PDNonCachedTokens        *int
	PDPrefixCacheScorerWeight int
	PDQueueScorerWeight       int
	PDMaxPrefixBlocks         int
	PDLRUCapacityPerServer    int
	// PDDeciderStrategy selects the disaggregation decider; only "threshold"
	// (default) is rendered today. "always" is gated on peakPrefillThroughput
	// calibration (out of scope) — persisted for forward-compat but not rendered.
	PDDeciderStrategy string
}

// Deployment sub-modes (PRD-57/58). Request.DeploymentMode carries these.
const (
	DeploymentModeDistributed   = "distributed"   // co-located multi-node (PRD-56/57)
	DeploymentModeDisaggregated = "disaggregated" // prefill/decode split (PRD-58)
)

// Cross-node fabric modes for distributed runs (PRD-56).
const (
	NetworkModeEFA = "efa" // EFA/RDMA via libfabric — preferred, default.
	NetworkModeTCP = "tcp" // NCCL over TCP sockets — no EFA required.
)

// networkMode returns the effective fabric mode, defaulting to EFA.
func (c RunConfig) networkMode() string {
	if c.NetworkMode == NetworkModeTCP {
		return NetworkModeTCP
	}
	return NetworkModeEFA
}

// IsDistributed reports whether this run uses the multi-node deploy path:
// the framework is a multi-node runtime AND a node count > 1 was requested.
// True for BOTH co-located distributed and disaggregated runs — both need the
// pool-acquire / scale / teardown / multi-node-DCGM machinery.
func (c RunConfig) IsDistributed() bool {
	rt, err := runtime.Get(c.Request.Framework)
	if err != nil {
		return false
	}
	return runtime.IsMultiNode(rt) && c.NodeCount > 1
}

// IsDisaggregated reports whether this run splits prefill and decode into
// separate pod groups (PRD-58). A subset of IsDistributed: it additionally
// routes deployModel/waitForReady to the PD object graph (two Deployments +
// InferencePool + EPP) instead of the co-located LeaderWorkerSet.
func (c RunConfig) IsDisaggregated() bool {
	return c.IsDistributed() && c.Request.DeploymentMode == DeploymentModeDisaggregated
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
	// PRD-56: dynamic client for applying/deleting CRDs (LeaderWorkerSet,
	// InferencePool, HTTPRoute, ResourceClaimTemplate) that the typed
	// clientset can't create, plus patching the static Karpenter NodePool's
	// replica count. nil = single-node only (multi-node runs are rejected).
	// Injected via SetDynamicClient after construction, mirroring how the
	// API server receives its dynamic client (PRD-33).
	dynClient dynamic.Interface
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc // runID → cancel
	// PRD-56: per-run distributed state (applied CRD graph + scaled NodePool),
	// so teardown deletes exactly what was created and returns the pool to 0.
	// Guarded by mu. Empty/absent for single-node runs.
	distributed map[string]*distributedState
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
		distributed: make(map[string]*distributedState),
		hostname:    hostname,
	}
}

// distributedState records what a multi-node run allocated so teardown can
// undo it precisely: the applied CRD/Service graph and the scaled NodePool.
// Keyed in o.distributed by the run's modelName ("bench-<runID[:8]>"), which
// both Execute and cleanupResources derive identically — so teardown reaches
// the state without threading the full runID through its signature.
type distributedState struct {
	poolName string          // the multinode NodePool scaled out (empty = none)
	applied  []appliedObject // CRDs + Service applied via the dynamic client
}

// SetSecretsStore enables HF token auto-injection. Called from the API server
// after construction; leaving it unset falls back to per-request tokens only.
func (o *Orchestrator) SetSecretsStore(s HFTokenResolver) {
	o.secrets = s
}

// SetDynamicClient injects the client-go dynamic client (PRD-56). Called from
// the API server after construction with the same in-cluster client the
// reservations handlers use. Leaving it nil disables the multi-node deploy
// path — distributed runs are rejected at Execute time with a clear error.
func (o *Orchestrator) SetDynamicClient(dc dynamic.Interface) {
	o.dynClient = dc
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

	// PRD-56 Layer 3: for a distributed run, claim the shared multi-node pool
	// (serialized — one at a time) and scale it out before deploying. The
	// pool is scaled back to 0 by teardown (registered above). Runs before
	// deployModel so nodes exist when the LeaderWorkerSet schedules.
	if cfg.IsDistributed() {
		log.Printf("[%s] distributed run: %d nodes, TP=%d PP=%d",
			cfg.RunID[:8], cfg.NodeCount, cfg.Request.TensorParallelDegree, cfg.PipelineParallelDegree)
		if err := o.acquireDistributedPool(ctx, ns, modelName, cfg); err != nil {
			o.markFailed(ctx, cfg.RunID, fmt.Sprintf("acquire distributed pool: %v", err))
			return fmt.Errorf("acquire distributed pool: %w", err)
		}
	}

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
	rtForMem, _ := runtime.Get(cfg.Request.Framework)
	hostMemScraper := NewHostMemScraper(o.client, ns,
		fmt.Sprintf("app.kubernetes.io/name=%s", modelName), rtForMem.ContainerName())
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
		if cfg.IsDistributed() {
			// PRD-56/58: GPUs live on every group node — fan DCGM out across all.
			// vLLM metrics come from the serving Service: "<name>-svc" (co-located
			// LWS leader) or, for a disaggregated run, "<name>-decode" (decode does
			// the token generation, so its serving metrics are the relevant ones;
			// per-role metric attribution is PRD-59). The DCGM node fan-out below
			// covers BOTH roles' nodes via the shared app.kubernetes.io/name label.
			metricsSvc := modelName + "-svc"
			if cfg.IsDisaggregated() {
				// Decode does the token generation, so its serving metrics are the
				// relevant ones. PRD-63: a "both"-only run has no decode Service —
				// fall back to the both (then prefill) Service so the vLLM-metrics
				// target still resolves. (DCGM still fans out across ALL role nodes
				// via the shared app.kubernetes.io/name label, independent of this.)
				switch {
				case cfg.DecodeReplicas > 0:
					metricsSvc = modelName + "-decode"
				case cfg.BothReplicas > 0:
					metricsSvc = modelName + "-both"
				default:
					metricsSvc = modelName + "-prefill"
				}
			}
			// PRD-59: keyed scraper — bucket DCGM samples per {node, role} so the
			// distributed report can show per-node/per-role GPU telemetry and an
			// honest group memory total. Role comes from the pod's llm-d.ai/role
			// label (prefill/decode; empty for co-located).
			nodes := o.llmdServingNodes(ctx, ns, modelName)
			log.Printf("[%s] DCGM scraping enabled across %d serving node(s) (keyed)", cfg.RunID[:8], len(nodes))
			// Total memory scales with the group: per-instance accel memory × nodes.
			gpuScraper = NewGPUScraperKeyed(metricsSvc, 8000, totalMemGiB*float64(cfg.NodeCount), nodes)
		} else {
			// Try to get node IP for DCGM metrics
			nodeIP := o.getModelPodNodeIP(ctx, ns, modelName)
			if nodeIP != "" {
				log.Printf("[%s] DCGM scraping enabled (node %s)", cfg.RunID[:8], nodeIP)
			}
			gpuScraper = NewGPUScraperWithDCGM(modelName, 8000, totalMemGiB, nodeIP)
		}
		gpuScraper.Start(ctx)
		log.Printf("[%s] started GPU metrics scraper", cfg.RunID[:8])
	}

	// PRD-62: for a DISAGGREGATED run, additionally scrape the per-role vLLM
	// PD/KV counters + the EPP's disaggregation-decision metrics. Started only
	// here (never for single-node or co-located runs), non-fatal, stopped with
	// the GPU scraper below.
	var pdScraper *PDScraper
	if cfg.IsDisaggregated() {
		vllmTargets, eppURL := o.pdMetricsTargets(ctx, ns, modelName)
		if len(vllmTargets) > 0 || eppURL != "" {
			pdScraper = NewPDScraper(vllmTargets, eppURL)
			pdScraper.Start(ctx)
			log.Printf("[%s] started PD/KV metrics scraper (%d vLLM target(s), epp=%t)",
				cfg.RunID[:8], len(vllmTargets), eppURL != "")
		}
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
		if pdScraper != nil {
			pdScraper.Stop()
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

	// PRD-62: stop the PD/KV scraper and collect the run-level disaggregation
	// summary. Nil when nothing was collected (non-disaggregated, or the series
	// never populated — e.g. older NIXL / unreachable EPP).
	var pdMetrics *PDMetrics
	if pdScraper != nil {
		pdMetrics = pdScraper.Stop()
		if pdMetrics != nil && pdMetrics.DisaggEngagedRatePct != nil {
			log.Printf("[%s] PD metrics: disagg-engaged=%.0f%% kv-xfer-avg=%.2fms",
				cfg.RunID[:8], *pdMetrics.DisaggEngagedRatePct,
				derefF(pdMetrics.KVTransferTimeAvgMs))
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

		// PRD-59: distributed runs carry the honest group memory total + the
		// per-node/per-role breakdown. Both are zero/empty on the single-node
		// (flat) path, so single-instance rows are unchanged.
		if cfg.IsDistributed() {
			memTotal := gpuMetrics.MemoryTotalGiB
			computed.AcceleratorMemoryTotalGiB = &memTotal
			computed.Shards = shardMetricsToDB(cfg.RunID, gpuMetrics.Shards)
		}

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

	// PRD-62: merge the run-level disaggregation summary (nil-safe; all pointers,
	// so absent series stay NULL). Only set for disaggregated runs.
	if pdMetrics != nil {
		computed.KVTransferTimeAvgMs = pdMetrics.KVTransferTimeAvgMs
		computed.KVTransferBytesTotal = pdMetrics.KVTransferBytesTotal
		computed.KVTransferFailures = pdMetrics.KVTransferFailures
		computed.PrefillTimeServerAvgMs = pdMetrics.PrefillTimeAvgMs
		computed.DecodeTimeServerAvgMs = pdMetrics.DecodeTimeAvgMs
		computed.ExternalPrefixCacheHitRate = pdMetrics.ExternalPrefixCacheHitRate
		computed.DisaggPrefillDecodeCount = pdMetrics.DisaggPrefillDecodeCount
		computed.DisaggDecodeOnlyCount = pdMetrics.DisaggDecodeOnlyCount
		computed.DisaggEngagedRatePct = pdMetrics.DisaggEngagedRatePct
		computed.PoolKVCacheUtilPct = pdMetrics.PoolKVCacheUtilPct
		computed.PoolQueueSizeAvg = pdMetrics.PoolQueueSizeAvg
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

// resolveS3Model decides how a run loads its model weights (PRD-65 Layer 2).
// Precedence, matching the single-node reference:
//  1. an explicit Request.ModelS3URI wins (stream from that S3 path);
//  2. else, if the HF model is cached in S3 (GetModelCacheByHfID, status
//     "cached"), auto-detect it and stream;
//  3. else HF download, no streamer.
// Returns (s3URI, useRunai). Path-agnostic so single-node, D/P, and (later) PP
// all share one cached-model policy — the multi-node paths previously only
// honored an explicit URI and never consulted the cache.
func (o *Orchestrator) resolveS3Model(ctx context.Context, cfg RunConfig) (string, bool) {
	if cfg.Request.ModelS3URI != "" {
		log.Printf("[%s] using S3 model: %s", cfg.RunID[:8], cfg.Request.ModelS3URI)
		return cfg.Request.ModelS3URI, true
	}
	if cfg.Request.ModelHfID != "" {
		revision := cfg.Request.ModelHfRevision
		if revision == "" {
			revision = "main"
		}
		cached, _ := o.repo.GetModelCacheByHfID(ctx, cfg.Request.ModelHfID, revision)
		if cached != nil && cached.Status == "cached" {
			log.Printf("[%s] auto-detected cached model: %s", cfg.RunID[:8], cached.S3URI)
			return cached.S3URI, true
		}
	}
	return "", false
}

func (o *Orchestrator) deployModel(ctx context.Context, ns, name string, cfg RunConfig) error {
	// PRD-58: disaggregated runs render the prefill/decode object graph
	// (two Deployments + InferencePool + EPP). PRD-56: co-located multi-node
	// runs render the llm-d LWS object graph. Both apply via the dynamic
	// client. Single-node runs take the unchanged typed-Deployment path below.
	if cfg.IsDisaggregated() {
		return o.deployLLMDDisaggregated(ctx, ns, name, cfg)
	}
	if cfg.IsDistributed() {
		return o.deployLLMD(ctx, ns, name, cfg)
	}

	// Reserve headroom for kubelet, kube-proxy, and OS overhead.
	// Request ~75% of instance vCPUs and ~85% of memory.
	vcpus := cfg.InstanceType.VCPUs
	memGiB := cfg.InstanceType.MemoryGiB
	cpuReq := fmt.Sprintf("%d", max(1, vcpus*3/4))
	memReq := fmt.Sprintf("%dGi", max(1, memGiB*85/100))

	modelS3URI, useRunai := o.resolveS3Model(ctx, cfg)

	// PRD-50 follow-up: the streamer is always used for S3-backed
	// models. vLLM's default loader against an S3 URI fails in
	// maybe_pull_model_tokenizer_for_runai before weights even load
	// (runai-model-streamer-s3's boto3 client doesn't resolve EKS
	// Pod Identity credentials cleanly). The user-facing streamer_mode
	// toggle was removed; memory_limit + concurrency remain as knobs
	// that tune the streamer itself.

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

	// Resolve container image, command, and args via the runtime interface.
	rt, err := runtime.Get(cfg.Request.Framework)
	if err != nil {
		return err
	}
	rtImage := rt.ResolveImageOverride()
	if rtImage == "" {
		rtImage = rt.DefaultImage(cfg.Request.FrameworkVersion, os.Getenv("PULL_THROUGH_REGISTRY"))
	}
	rtCommand, rtArgs := rt.BuildArgs(runtime.ContainerParams{
		ModelHfID:              cfg.Request.ModelHfID,
		ModelS3URI:             modelS3URI,
		UseRunaiStreamer:       useRunai,
		TensorParallelDegree:   cfg.Request.TensorParallelDegree,
		MaxModelLen:            cfg.Request.MaxModelLen,
		MaxNumBatchedTokens:    cfg.Request.MaxNumBatchedTokens,
		MaxNumSeqs:             0, // PRD-51
		KVCacheDtype:           cfg.Request.KVCacheDtype,
		Quantization:           derefStr(cfg.Request.Quantization),
		StreamerConcurrency:    streamerConcurrency,
		StreamerMemoryLimitGiB: streamerMemLimitGiB,
		ChunkedPrefillSize:     cfg.Request.ChunkedPrefillSize,
		MemFractionStatic:      cfg.Request.MemFractionStatic,
		AcceleratorName:        cfg.InstanceType.AcceleratorName,
	})

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
		MaxNumSeqs:           0,
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
		SGLangImageOverride:     ResolveSGLangImageOverride(),
		RuntimeContainerName:    rt.ContainerName(),
		RuntimeImage:            rtImage,
		RuntimeCommand:          rtCommand,
		RuntimeArgs:             rtArgs,
	})
	if err != nil {
		return err
	}

	return o.applyYAML(ctx, ns, yamlStr)
}

func (o *Orchestrator) waitForReady(ctx context.Context, ns, name string, cfg RunConfig) error {
	// PRD-58: disaggregated runs wait on BOTH the prefill and decode
	// Deployments + the EPP before the loadgen can route.
	if cfg.IsDisaggregated() {
		return o.waitForDisaggregatedReady(ctx, ns, name, cfg)
	}
	// PRD-56: co-located multi-node runs poll the LeaderWorkerSet group status
	// instead of a Deployment's ReadyReplicas.
	if cfg.IsDistributed() {
		return o.waitForLWSReady(ctx, ns, name, cfg)
	}
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
	// PRD-56: distributed runs target the shared Envoy AI Gateway (ClusterIP)
	// instead of the model Service. inference-perf is unchanged — it's a
	// drop-in swap of the target host/port.
	targetHost, targetPort := modelSvc, 8000
	if cfg.IsDistributed() {
		targetHost, targetPort = o.gatewayLoadgenTarget(ctx)
		log.Printf("[%s] loadgen targeting gateway %s:%d", cfg.RunID[:8], targetHost, targetPort)
	}
	inferencePerfConfig := s.ToInferencePerfConfig(cfg.Request.ModelHfID, targetHost, targetPort)
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

	// PRD-56: for a distributed run, delete the llm-d object graph (LWS +
	// gateway route + DRA claims) and scale the NodePool back to 0. No-op for
	// single-node runs (nothing recorded under modelName). Deleting the LWS's
	// pods first (above/here) lets nodes drain before scale-in.
	o.teardownDistributed(ctx, ns, modelName)
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
			// The single-node path only ever renders the three typed kinds
			// above. Custom resources (the multi-node llm-d object graph) are
			// applied via the dynamic client through applyManifestSet, which
			// also tracks them for teardown — not through this typed path.
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

// derefF dereferences a *float64, returning 0 for nil (log/display convenience).
func derefF(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// valOrDefault returns v when it's a positive "set" value, else def. Used by the
// PRD-61 routing knobs where 0 means "unset → use the shipped default" (weights
// and sizes are never legitimately 0; nonCachedTokens, where 0 IS meaningful,
// uses a pointer instead).
func valOrDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
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
