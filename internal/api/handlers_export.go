package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"
	"github.com/accelbench/accelbench/internal/orchestrator"
	"github.com/accelbench/accelbench/internal/report"
	"github.com/accelbench/accelbench/internal/runtime"
	"github.com/accelbench/accelbench/internal/scenario"
	"github.com/accelbench/accelbench/internal/testsuite"
)

// Small nil-deref helpers for building runtime ContainerParams from the
// pointer-typed RunExportDetails fields.
func derefStrExport(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}
func derefIntExport(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}
func derefFloatExport(p *float64) float64 {
	if p != nil {
		return *p
	}
	return 0
}

// PRD-59: export-side mirrors of the orchestrator's distributed deploy
// constants (unexported there). Kept in sync so an exported manifest matches
// what a fresh distributed run would deploy. If these drift, the export is
// still apply-able — it just may name a different gateway/pool.
const (
	exportGatewayName      = "accelbench-gateway"
	exportGatewayNamespace = "envoy-gateway-system"
	exportGPUDeviceClass   = "gpu.nvidia.com"
	exportEFADeviceClass   = "efa.networking.k8s.aws"
	exportMultiNodeTaintK  = "accelbench.io/multinode"
	exportMultiNodeTaintV  = "true"
	exportDRASelectorK     = "accelbench.io/dra"
	exportDRASelectorV     = "true"
	exportPDSidecarImage   = "ghcr.io/llm-d/llm-d-router-disagg-sidecar:v0.9.0"
	exportPDEPPImage       = "ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0"
	exportPDNixlModuleDir  = "/usr/local/lib/python3.12/dist-packages/nixl_cu13.libs/ucx"
	exportPDNonCachedToken = 16
	// PRD-61: EPP routing defaults, mirroring the orchestrator's defaultPD*
	// (internal/orchestrator/disaggregated.go). Applied when a run's pd_* column
	// is NULL, so the exported EPP config matches the shipped default the run used.
	exportPDPrefixCacheWeight = 2
	exportPDQueueScorerWeight = 1
	exportPDMaxPrefixBlocks   = 256
	exportPDLRUCapacity       = 31250
)

// derefPtr returns *p when p is non-nil (PRESERVING 0), else def. Unlike the
// local deref (which floors non-positive to def), this is for values where 0 is
// meaningful — e.g. pd_noncached_tokens=0 (disable disaggregation).
func derefPtr(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// sanitizeDNS1123 turns a model id into a DNS-1123-label-safe name (lowercase
// alnum + '-', no dots, no leading/trailing dashes, <=63 chars) for use as a
// Kubernetes resource name / label value. sanitizeFilename is NOT sufficient —
// it leaves dots (e.g. "qwen2.5"), which k8s object names reject.
func sanitizeDNS1123(modelID string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(modelID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 55 { // leave headroom for suffixes like "-prefill-devices"
		s = strings.Trim(s[:55], "-")
	}
	if s == "" {
		s = "model"
	}
	return s
}

// exportServeArgs builds the model positional + static tuning flags that both
// llm-d render paths append coordination/TP flags onto — mirroring the llm-d
// runtime's BuildArgs (model id + --trust-remote-code + optional knobs). Uses the
// run's shared --max-num-batched-tokens.
func exportServeArgs(d *database.RunExportDetails) []string {
	return exportServeArgsWithBatchTokens(d, d.MaxNumBatchedTokens)
}

// exportServeArgsWithBatchTokens is exportServeArgs with an explicit
// --max-num-batched-tokens override — used to build the PRD-64 per-role arg sets
// so the exported prefill/decode/both Deployments carry the exact per-role
// scheduler flag that was applied. maxNBT nil/<=0 ⇒ omit the flag (vLLM default).
func exportServeArgsWithBatchTokens(d *database.RunExportDetails, maxNBT *int) []string {
	// PRD-65 Layer 4: when the run streamed from S3 (Run:ai), the model arg is
	// the S3 URI + --load-format runai_streamer + --model-loader-extra-config
	// (concurrency, and memory_limit in bytes when set) — mirroring the runtime
	// BuildArgs so the exported manifest reproduces the deployed serve line.
	// Otherwise the HF model id (unchanged). resolveExportStreamer scopes
	// UseRunaiStreamer to D/P, so PP never takes this branch.
	var args []string
	if d.UseRunaiStreamer && d.ModelS3URI != nil && *d.ModelS3URI != "" {
		concurrency := 16
		if d.StreamerConcurrency != nil && *d.StreamerConcurrency > 0 {
			concurrency = *d.StreamerConcurrency
		}
		extra := fmt.Sprintf(`{"concurrency":%d}`, concurrency)
		if memGiB := exportStreamerMemLimitGiB(d); memGiB > 0 {
			extra = fmt.Sprintf(`{"concurrency":%d,"memory_limit":%d}`,
				concurrency, int64(memGiB)*1024*1024*1024)
		}
		args = []string{*d.ModelS3URI, "--load-format", "runai_streamer", "--model-loader-extra-config", extra, "--trust-remote-code"}
	} else {
		args = []string{d.ModelHfID, "--trust-remote-code"}
	}
	if d.MaxModelLen > 0 {
		args = append(args, "--max-model-len", fmt.Sprintf("%d", d.MaxModelLen))
	}
	if maxNBT != nil && *maxNBT > 0 {
		args = append(args, "--max-num-batched-tokens", fmt.Sprintf("%d", *maxNBT))
	}
	if d.KVCacheDtype != nil && *d.KVCacheDtype != "" {
		args = append(args, "--kv-cache-dtype", *d.KVCacheDtype)
	}
	return args
}

// exportStreamerMemLimitGiB resolves the D/P streamer memory-limit for export
// the same way deployLLMDDisaggregated does: the persisted value if set, else
// auto-size to half the node RAM. Keeps the exported memory_limit + env in sync
// with what deployed.
func exportStreamerMemLimitGiB(d *database.RunExportDetails) int {
	if d.StreamerMemoryLimitGiB != nil && *d.StreamerMemoryLimitGiB > 0 {
		return *d.StreamerMemoryLimitGiB
	}
	return max(d.MemoryGiB/2, 1)
}

// exportPDStreamerMemLimitGiB is the render-param form: the memory-limit only
// when the run streamed (else 0 → the template emits no env, byte-identical to
// an HF-load export).
func exportPDStreamerMemLimitGiB(d *database.RunExportDetails) int {
	if d.UseRunaiStreamer && d.ModelS3URI != nil && *d.ModelS3URI != "" {
		return exportStreamerMemLimitGiB(d)
	}
	return 0
}

// exportPDModelServiceAccount returns the S3-access service account for a
// streamed D/P run (matching the orchestrator's modelServiceAccount), else "".
func exportPDModelServiceAccount(d *database.RunExportDetails) string {
	if d.UseRunaiStreamer && d.ModelS3URI != nil && *d.ModelS3URI != "" {
		return "accelbench-model"
	}
	return ""
}

func exportNetworkMode(d *database.RunExportDetails) string {
	if d.NetworkMode != nil && *d.NetworkMode == "tcp" {
		return "tcp"
	}
	return "efa"
}

// exportLLMDImageFor resolves the co-located PP image the same way the
// orchestrator's deploy path does (PRD-66 Part 2): an LLMD_IMAGE / VLLM_IMAGE
// override wins verbatim; otherwise compose ghcr.io/llm-d/llm-d-aws from the
// configured LLMDVersion (empty ⇒ shipped default), routed through the GHCR
// pull-through cache when one is configured (PRD-66 Part 2a). One resolver so
// the export can't drift from what ran.
func exportLLMDImageFor(d *database.RunExportDetails) string {
	rt := &runtime.LLMD{}
	if ov := rt.ResolveImageOverride(); ov != "" {
		return ov
	}
	return runtime.LLMDImage(d.LLMDVersion, os.Getenv("PULL_THROUGH_REGISTRY"))
}

// exportPDModelImageFor resolves the D/P vLLM image the same way the
// orchestrator's deploy path does (PRD-66 Part 2): a VLLM_IMAGE / LLMD_IMAGE
// override wins verbatim; else a PD_MODEL_IMAGE env var is an exact ref; else
// compose vllm/vllm-openai from the configured PDVLLMVersion, routed through the
// Docker Hub pull-through cache when one is set. Shares orchestrator.PDModelImage
// so deploy + export can't drift.
func exportPDModelImageFor(d *database.RunExportDetails) string {
	rt := &runtime.LLMD{}
	if ov := rt.ResolveImageOverride(); ov != "" {
		return ov
	}
	if pd := os.Getenv("PD_MODEL_IMAGE"); pd != "" {
		return pd
	}
	return orchestrator.PDModelImage(d.PDVLLMVersion, os.Getenv("PULL_THROUGH_REGISTRY"))
}

// generateDistributedManifest renders the co-located multi-node llm-d object
// graph (LeaderWorkerSet + Service + HTTPRoute + DRA claims) for a distributed
// run (PRD-56 shape), reusing the orchestrator's renderer (PRD-59 fix — the old
// path wrongly emitted a single-node Deployment for these runs).
func generateDistributedManifest(d *database.RunExportDetails) (string, error) {
	name := "llmd-" + sanitizeDNS1123(d.ModelHfID)
	nodeCount := 2
	if d.NodeCount != nil && *d.NodeCount > 0 {
		nodeCount = *d.NodeCount
	}
	pp := nodeCount
	if d.PipelineParallelDegree != nil && *d.PipelineParallelDegree > 0 {
		pp = *d.PipelineParallelDegree
	}
	gpusPerNode := d.AcceleratorCount
	tp := d.TensorParallelDegree
	if tp < 1 {
		tp = 1
	}
	netMode := exportNetworkMode(d)
	efaPerNode := gpusPerNode
	if netMode == "tcp" {
		efaPerNode = 0
	}
	return manifest.RenderLLMDDeployment(manifest.LLMDDeploymentParams{
		Name:                   name,
		Namespace:              "accelbench",
		Image:                  exportLLMDImageFor(d),
		ServeArgs:              exportServeArgs(d),
		ContainerName:          "vllm",
		ModelHfID:              d.ModelHfID,
		HfToken:                "",
		InstanceTypeName:       d.InstanceTypeName,
		NodeCount:              nodeCount,
		TensorParallelDegree:   tp,
		PipelineParallelDegree: pp,
		GPUsPerNode:            gpusPerNode,
		CPURequest:             fmt.Sprintf("%d", max(d.VCPUs*3/4, 1)),
		MemoryRequest:          fmt.Sprintf("%dGi", max(d.MemoryGiB*85/100, 1)),
		NetworkMode:            netMode,
		GPUDeviceClass:         exportGPUDeviceClass,
		EFADeviceClass:         exportEFADeviceClass,
		EFAPerNode:             efaPerNode,
		GatewayName:            exportGatewayName,
		GatewayNamespace:       exportGatewayNamespace,
		MultiNodeTaintKey:      exportMultiNodeTaintK,
		MultiNodeTaintValue:    exportMultiNodeTaintV,
		DRANodeSelectorKey:     exportDRASelectorK,
		DRANodeSelectorVal:     exportDRASelectorV,
	})
}

// generateDisaggregatedManifest renders the prefill/decode object graph (two
// Deployments + InferencePool + EPP) for a disaggregated run (PRD-58 shape).
func generateDisaggregatedManifest(d *database.RunExportDetails) (string, error) {
	name := "pd-" + sanitizeDNS1123(d.ModelHfID)
	deref := func(p *int, def int) int {
		if p != nil && *p > 0 {
			return *p
		}
		return def
	}
	// PRD-63: a role's replica count may legitimately be 0 (a both-only run has
	// null prefill/decode). derefZero preserves 0 (vs. deref's floor-to-def),
	// so the exported graph matches the run's actual pool combination.
	derefZero := func(p *int) int {
		if p != nil && *p > 0 {
			return *p
		}
		return 0
	}
	// Replica counts preserve 0; but if the whole set is empty (all null, e.g. a
	// historical PD row that predates typed export), fall back to a 1P1D graph.
	prefillR := derefZero(d.PrefillReplicas)
	decodeR := derefZero(d.DecodeReplicas)
	bothR := derefZero(d.BothReplicas)
	if prefillR == 0 && decodeR == 0 && bothR == 0 {
		prefillR, decodeR = 1, 1
	}
	// PRD-64: reproduce the per-role scheduler override actually applied. The
	// shared ServeArgs carry the run's shared --max-num-batched-tokens; a role's
	// arg set is emitted only when that role had an override (matching the
	// orchestrator, which leaves the per-role sets nil otherwise → template falls
	// back to shared).
	var prefillArgs, decodeArgs, bothArgs []string
	if d.PrefillMaxNumBatchedTokens != nil && *d.PrefillMaxNumBatchedTokens > 0 {
		prefillArgs = exportServeArgsWithBatchTokens(d, d.PrefillMaxNumBatchedTokens)
	}
	if d.DecodeMaxNumBatchedTokens != nil && *d.DecodeMaxNumBatchedTokens > 0 {
		decodeArgs = exportServeArgsWithBatchTokens(d, d.DecodeMaxNumBatchedTokens)
	}
	if d.BothMaxNumBatchedTokens != nil && *d.BothMaxNumBatchedTokens > 0 {
		bothArgs = exportServeArgsWithBatchTokens(d, d.BothMaxNumBatchedTokens)
	}
	// Resolve the D/P vLLM image the same way the orchestrator's deploy path
	// does (PRD-66 Part 2): a VLLM_IMAGE / PD_MODEL_IMAGE override wins verbatim;
	// otherwise compose vllm/vllm-openai from the configured pd_vllm_version and
	// route through the Docker Hub ECR pull-through cache when one is set — so the
	// exported manifest reproduces what ran.
	pdImage := exportPDModelImageFor(d)
	// PRD-61: reproduce the run's EPP routing config. NULL ⇒ the run used the
	// shipped default, so the export applies the SAME default the orchestrator
	// would (deref-to-default), keeping the exported EPP config faithful to what
	// ran. nonCachedTokens=0 is meaningful (disable PD) and preserved by deref.
	return manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name:                name,
		Namespace:           "accelbench",
		Image:               pdImage,
		ServeArgs:           exportServeArgs(d),
		PrefillServeArgs:    prefillArgs,
		DecodeServeArgs:     decodeArgs,
		BothServeArgs:       bothArgs,
		ContainerName:       "vllm",
		ModelHfID:           d.ModelHfID,
		ModelLabel:          sanitizeDNS1123(d.ModelHfID),
		HfToken:             "",
		// PRD-65 Layer 4: reproduce the streamed-load wiring — the S3-access SA
		// + the memory-limit env — when the run streamed (D/P cached model).
		ModelServiceAccount:    exportPDModelServiceAccount(d),
		StreamerMemoryLimitGiB: exportPDStreamerMemLimitGiB(d),
		InstanceTypeName:    d.InstanceTypeName,
		PrefillReplicas:     prefillR,
		PrefillTP:           deref(d.PrefillTP, 1),
		DecodeReplicas:      decodeR,
		DecodeTP:            deref(d.DecodeTP, 1),
		BothReplicas:        bothR,
		BothTP:              deref(d.BothTP, 1),
		CPURequest:          fmt.Sprintf("%d", max(d.VCPUs*3/4, 1)),
		MemoryRequest:       fmt.Sprintf("%dGi", max(d.MemoryGiB*85/100, 1)),
		NetworkMode:         exportNetworkMode(d),
		NixlModuleDir:       exportPDNixlModuleDir,
		EPPImage:            exportPDEPPImage,
		SidecarImage:        exportPDSidecarImage,
		// nonCachedTokens uses a POINTER deref (0 = disable PD, distinct from unset).
		NonCachedTokens:         derefPtr(d.PDNonCachedTokens, exportPDNonCachedToken),
		PrefixCacheScorerWeight: deref(d.PDPrefixCacheWeight, exportPDPrefixCacheWeight),
		QueueScorerWeight:       deref(d.PDQueueScorerWeight, exportPDQueueScorerWeight),
		MaxPrefixBlocksToMatch:  deref(d.PDMaxPrefixBlocks, exportPDMaxPrefixBlocks),
		LRUCapacityPerServer:    deref(d.PDLRUCapacityPerServer, exportPDLRUCapacity),
		// EPPZone is intentionally left empty: the run's AZ is a runtime capacity
		// decision (never persisted), and a re-applied manifest picks its own AZ.
		// Pinning the EPP to this run's AZ would be wrong for a fresh re-apply, so
		// the exported EPP stays AZ-unconstrained (schedules on any system node).
		GPUDeviceClass:      exportGPUDeviceClass,
		GatewayName:         exportGatewayName,
		GatewayNamespace:    exportGatewayNamespace,
		MultiNodeTaintKey:   exportMultiNodeTaintK,
		MultiNodeTaintValue: exportMultiNodeTaintV,
		DRANodeSelectorKey:  exportDRASelectorK,
		DRANodeSelectorVal:  exportDRASelectorV,
	})
}

// handleExportManifest generates a Kubernetes manifest YAML for deploying
// the model configuration from a completed benchmark run.
func (s *Server) handleExportManifest(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")

	// First check if the run exists and is completed.
	run, err := s.repo.GetBenchmarkRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status != "completed" {
		writeError(w, http.StatusBadRequest, "can only export completed runs")
		return
	}

	// Get the full export details with joined model/instance info.
	details, err := s.repo.GetRunExportDetails(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query export details failed")
		return
	}
	if details == nil {
		writeError(w, http.StatusNotFound, "run details not found")
		return
	}
	// PRD-66 Part 2: inject the configured multi-node image tags so the exported
	// manifest names the image that would actually deploy (not a stale hardcode).
	s.injectMultinodeImageVersions(r.Context(), details)
	// PRD-65 Layer 4: reproduce the D/P cached-model streamer decision.
	s.resolveExportStreamer(r.Context(), details)

	// Generate the manifest.
	manifest, err := generateManifest(details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate manifest failed: %v", err))
		return
	}

	// Return as downloadable YAML file.
	filename := fmt.Sprintf("vllm-%s.yaml", sanitizeFilename(details.ModelHfID))
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(manifest))
}

// resolveExportStreamer makes the exported manifest reproduce the run's actual
// weight-load path (PRD-65 Layer 4). The orchestrator auto-detects a cached S3
// model at deploy time (resolveS3Model) WITHOUT persisting model_s3_uri, so a
// run that streamed a cached model would otherwise export as an HF load. Mirror
// that resolution here: if no explicit ModelS3URI but the HF model is cached,
// set ModelS3URI + UseRunaiStreamer so the exporter emits the streamer flags.
//
// SCOPED TO DISAGGREGATED (D/P): the upstream vllm/vllm-openai image D/P uses
// bundles runai-model-streamer. The co-located PP path (llm-d-aws) does NOT, so
// PP must NEVER get streamer flags — leave distributed/single-node exports to
// their persisted ModelS3URI (single-node already persists it; PP can't stream).
func (s *Server) resolveExportStreamer(ctx context.Context, d *database.RunExportDetails) {
	if d == nil || d.DeploymentMode == nil || *d.DeploymentMode != "disaggregated" {
		return
	}
	if d.ModelS3URI != nil && *d.ModelS3URI != "" {
		return // explicit URI already recorded → GetRunExportDetails set UseRunaiStreamer
	}
	if d.ModelHfID == "" {
		return
	}
	revision := "main" // export details don't carry revision; matches orchestrator default
	if cached, _ := s.repo.GetModelCacheByHfID(ctx, d.ModelHfID, revision); cached != nil && cached.Status == "cached" {
		uri := cached.S3URI
		d.ModelS3URI = &uri
		d.UseRunaiStreamer = true
	}
}

// injectMultinodeImageVersions fills the configured llm-d-aws + D/P vLLM image
// tags onto the export details from tool_versions (PRD-66 Part 2), so the
// generators compose the same image the orchestrator would deploy. Best-effort:
// on any lookup failure the fields stay empty and the generators fall back to
// the shipped defaults (byte-identical to pre-PRD-66 exports).
func (s *Server) injectMultinodeImageVersions(ctx context.Context, d *database.RunExportDetails) {
	if d == nil {
		return
	}
	if tv, err := s.repo.GetToolVersions(ctx); err == nil && tv != nil {
		d.LLMDVersion = tv.LLMDVersion
		d.PDVLLMVersion = tv.PDVLLMVersion
	}
}

// sanitizeFilename converts a model ID to a safe filename.
func sanitizeFilename(modelID string) string {
	// Replace slashes and other problematic characters.
	s := strings.ReplaceAll(modelID, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ToLower(s)
	return s
}

// manifestData holds the template data for generating the Kubernetes manifest.
type manifestData struct {
	Name                 string
	ModelHfID            string
	ModelS3URI           string // non-empty when the run loaded weights from S3
	InstanceType         string
	Framework            string // "vllm", "vllm-neuron", or "sglang"
	FrameworkVersion     string
	// SGLangImageOverride mirrors VLLMImageOverride for SGLang runs (SGLANG_IMAGE).
	SGLangImageOverride string
	// RuntimeCommand / RuntimeArgs are the container command + args computed via
	// the runtime interface (internal/runtime) for the SGLang path, so the export
	// reproduces the EXACT flags the orchestrator's BuildArgs produced —
	// including the accelerator-dependent backend + SGLang scheduler knobs —
	// without re-encoding that logic in the template. Empty for the vLLM/neuron
	// path (which keeps its existing inline template args).
	RuntimeCommand []string
	RuntimeArgs    []string
	TensorParallelDegree int
	Quantization         string
	MaxModelLen          int
	MaxNumBatchedTokens  int    // 0 = use vLLM default
	MaxNumSeqs           int    // 0 = use vLLM default
	KVCacheDtype         string // empty = use vLLM default
	AcceleratorType      string
	AcceleratorCount     int
	CPURequest           string
	MemoryRequest        string
	ShmSize              string
	PullThroughRegistry  string // ECR pull-through cache host (empty = direct Docker Hub)
	// PRD-49: full vLLM image URI override. When non-empty, the template
	// uses it verbatim and skips the PullThroughRegistry/FrameworkVersion
	// path. Sourced from the same VLLM_IMAGE env var the orchestrator
	// reads, so exports match what ran.
	VLLMImageOverride    string
	// PRD-50: Run:ai streamer knobs. UseRunaiStreamer is the resolved
	// decision (streamer_mode != "off" && model had an S3 URI). When
	// false, the template emits the HuggingFace-style loader even if
	// ModelS3URI is set — matching what streamer_mode=off produced at
	// runtime.
	UseRunaiStreamer       bool
	StreamerConcurrency    int   // 0 → template default of 16
	StreamerMemoryLimitGiB int   // 0 → emit no env var, inherit upstream 40 GB default
	StreamerMemoryLimitBytes int64 // derived for the env-var value
}

func generateManifest(d *database.RunExportDetails) (string, error) {
	// PRD-59: distributed / disaggregated runs export the llm-d object graph
	// (LeaderWorkerSet or prefill/decode Deployments + InferencePool/EPP), NOT a
	// single-node vLLM Deployment. Reuse the same renderers the orchestrator
	// uses so a re-apply matches what a fresh distributed run would deploy.
	if d.DeploymentMode != nil {
		switch *d.DeploymentMode {
		case "distributed":
			return generateDistributedManifest(d)
		case "disaggregated":
			return generateDisaggregatedManifest(d)
		}
	}

	namePrefix := "vllm-"
	if d.Framework == "sglang" {
		namePrefix = "sglang-"
	}
	data := manifestData{
		Name:                 namePrefix + sanitizeFilename(d.ModelHfID),
		ModelHfID:            d.ModelHfID,
		InstanceType:         d.InstanceTypeName,
		Framework:            d.Framework,
		FrameworkVersion:     d.FrameworkVersion,
		SGLangImageOverride:  os.Getenv("SGLANG_IMAGE"),
		TensorParallelDegree: d.TensorParallelDegree,
		MaxModelLen:          d.MaxModelLen,
		// PRD-51: PRD-46's --max-num-seqs=concurrency wiring starved
		// open-loop scenarios (steady-state in-flight is rate×latency,
		// not worker count). The live orchestrator now omits the flag
		// and lets vLLM pick 256. Exports mirror the new behavior so
		// a re-apply matches what a fresh run would deploy. Pre-PRD-51
		// historical exports will differ from what originally ran, but
		// that's the point — re-applying should use the corrected
		// scheduler config.
		MaxNumSeqs:      0,
		AcceleratorType: d.AcceleratorType,
		AcceleratorCount:     d.AcceleratorCount,
		CPURequest:           fmt.Sprintf("%d", max(d.VCPUs/2, 4)),
		MemoryRequest:        fmt.Sprintf("%dGi", max(d.MemoryGiB/2, 16)),
		ShmSize:              "16Gi",
		PullThroughRegistry:  os.Getenv("PULL_THROUGH_REGISTRY"),
		VLLMImageOverride:    os.Getenv("VLLM_IMAGE"),
		UseRunaiStreamer:     d.UseRunaiStreamer,
	}
	if d.MaxNumBatchedTokens != nil {
		data.MaxNumBatchedTokens = *d.MaxNumBatchedTokens
	}
	if d.KVCacheDtype != nil {
		data.KVCacheDtype = *d.KVCacheDtype
	}
	if d.ModelS3URI != nil && *d.ModelS3URI != "" {
		data.ModelS3URI = *d.ModelS3URI
	}

	// Handle quantization.
	if d.Quantization != nil {
		data.Quantization = *d.Quantization
	}

	// PRD-50: streamer knobs. Concurrency defaults to 16 when null.
	// Memory limit is rendered as bytes in the env var.
	if d.StreamerConcurrency != nil && *d.StreamerConcurrency > 0 {
		data.StreamerConcurrency = *d.StreamerConcurrency
	} else {
		data.StreamerConcurrency = 16
	}
	if d.StreamerMemoryLimitGiB != nil && *d.StreamerMemoryLimitGiB > 0 {
		data.StreamerMemoryLimitGiB = *d.StreamerMemoryLimitGiB
		data.StreamerMemoryLimitBytes = int64(*d.StreamerMemoryLimitGiB) * 1024 * 1024 * 1024
	}

	// SGLang single-node export: reproduce the EXACT flags the orchestrator would
	// pass by reusing the runtime's BuildArgs (rather than re-encoding SGLang's
	// accelerator-dependent backend + scheduler-knob logic in the template). The
	// vLLM/neuron path keeps its inline template args unchanged.
	if d.Framework == "sglang" {
		if rt, err := runtime.Get("sglang"); err == nil {
			cmd, args := rt.BuildArgs(runtime.ContainerParams{
				ModelHfID:            d.ModelHfID,
				TensorParallelDegree: d.TensorParallelDegree,
				MaxModelLen:          d.MaxModelLen,
				Quantization:         derefStrExport(d.Quantization),
				ChunkedPrefillSize:   derefIntExport(d.ChunkedPrefillSize),
				MemFractionStatic:    derefFloatExport(d.MemFractionStatic),
				AcceleratorName:      d.AcceleratorName,
			})
			data.RuntimeCommand = cmd
			data.RuntimeArgs = args
		}
	}

	var buf bytes.Buffer
	if err := manifestTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

var manifestFuncs = template.FuncMap{
	"div": func(a, b int) int { return a / b },
	// quote renders a container-arg string as a double-quoted YAML scalar,
	// matching the inline vLLM args style (e.g. "--tp-size"). Used for the
	// SGLang RuntimeCommand/RuntimeArgs path.
	"quote": func(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\"" },
}

var manifestTemplate = template.Must(template.New("manifest").Funcs(manifestFuncs).Parse(`# Kubernetes manifest for {{ if eq .Framework "sglang" }}SGLang{{ else }}vLLM{{ end }} model deployment
# Generated from AccelBench benchmark run
#
# Model: {{ .ModelHfID }}
{{- if .ModelS3URI }}
{{- if .UseRunaiStreamer }}
# Weights: {{ .ModelS3URI }} (loaded via Run:ai Model Streamer, concurrency={{ .StreamerConcurrency }}{{ if gt .StreamerMemoryLimitGiB 0 }}, memory_limit={{ .StreamerMemoryLimitGiB }} GiB{{ end }})
{{- else }}
# Weights: {{ .ModelS3URI }} (streamer disabled on original run)
{{- end }}
{{- end }}
# Instance: {{ .InstanceType }}
# Tensor Parallel: {{ .TensorParallelDegree }}
# Max Model Length: {{ .MaxModelLen }}
{{- if gt .MaxNumBatchedTokens 0 }}
# Max Num Batched Tokens: {{ .MaxNumBatchedTokens }}
{{- end }}
{{- if .KVCacheDtype }}
# KV Cache Dtype: {{ .KVCacheDtype }}
{{- end }}
{{- if .Quantization }}
# Quantization: {{ .Quantization }}
{{- end }}
{{- if eq .Framework "sglang" }}
# Framework: SGLang {{ .FrameworkVersion }}
{{- end }}
#
# Prerequisites:
{{- if .ModelS3URI }}
# 1. Pod must have read access to the S3 bucket holding the model weights.
#    The template uses a ServiceAccount named 'accelbench-model' that assumes
#    an IAM role via EKS Pod Identity. If you're deploying outside the
#    AccelBench cluster, replace this with your own SA + IAM binding.
{{- else }}
# 1. Create the HuggingFace token secret:
#    kubectl create secret generic hf-token --from-literal=token=<YOUR_HF_TOKEN>
{{- end }}
#
# 2. Ensure your cluster has nodes with the required instance type:
#    {{ .InstanceType }}
#
# Apply with:
#    kubectl apply -f <this-file>.yaml
{{- if not .ModelS3URI }}
---
apiVersion: v1
kind: Secret
metadata:
  name: hf-token
  labels:
    app.kubernetes.io/name: {{ .Name }}
type: Opaque
stringData:
  token: "<YOUR_HF_TOKEN>"
{{- end }}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
  labels:
    app.kubernetes.io/name: {{ .Name }}
    app.kubernetes.io/component: model-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Name }}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Name }}
        app.kubernetes.io/component: model-server
    spec:
      serviceAccountName: {{ if .ModelS3URI }}accelbench-model{{ else }}default{{ end }}
      terminationGracePeriodSeconds: 30
      tolerations:
{{- if eq .AcceleratorType "gpu" }}
        - key: nvidia.com/gpu
          operator: Exists
          effect: NoSchedule
{{- else }}
        - key: aws.amazon.com/neuron
          operator: Exists
          effect: NoSchedule
{{- end }}
      nodeSelector:
        node.kubernetes.io/instance-type: {{ .InstanceType }}
      containers:
        - name: {{ if eq .Framework "sglang" }}sglang{{ else }}vllm{{ end }}
{{- if eq .Framework "sglang" }}
{{- if .SGLangImageOverride }}
          image: {{ .SGLangImageOverride }}
{{- else }}
          image: {{ if .PullThroughRegistry }}{{ .PullThroughRegistry }}/dockerhub/{{ end }}lmsysorg/sglang:{{ .FrameworkVersion }}
{{- end }}
{{- else if eq .AcceleratorType "gpu" }}
{{- if .VLLMImageOverride }}
          image: {{ .VLLMImageOverride }}
{{- else }}
          image: {{ if .PullThroughRegistry }}{{ .PullThroughRegistry }}/dockerhub/{{ end }}vllm/vllm-openai:{{ .FrameworkVersion }}
{{- end }}
{{- else }}
          image: public.ecr.aws/neuron/pytorch-inference-vllm-neuronx:0.13.0-neuronx-py312-sdk2.28.0-ubuntu24.04
{{- end }}
          ports:
            - name: http
              containerPort: 8000
              protocol: TCP
          env:
{{- if .ModelS3URI }}
            - name: AWS_REGION
              value: "us-east-2"
{{- else }}
            - name: HF_TOKEN
              valueFrom:
                secretKeyRef:
                  name: hf-token
                  key: token
                  optional: true
{{- end }}
{{- if and (eq .AcceleratorType "gpu") (gt .TensorParallelDegree 1) }}
            - name: NCCL_DEBUG
              value: "INFO"
            - name: NCCL_P2P_LEVEL
              value: "NVL"
{{- end }}
{{- if and .UseRunaiStreamer (gt .StreamerMemoryLimitBytes 0) }}
            # PRD-50: cap the Run:ai streamer's shared CPU buffer.
            - name: RUNAI_STREAMER_MEMORY_LIMIT
              value: "{{ .StreamerMemoryLimitBytes }}"
{{- end }}
{{- if eq .Framework "sglang" }}
          command:
{{- range .RuntimeCommand }}
            - {{ . | quote }}
{{- end }}
          args:
{{- range .RuntimeArgs }}
            - {{ . | quote }}
{{- end }}
{{- else if eq .AcceleratorType "gpu" }}
          args:
{{- if .UseRunaiStreamer }}
            - "--model"
            - "{{ .ModelS3URI }}"
            - "--load-format"
            - "runai_streamer"
            - "--model-loader-extra-config"
            - '{"concurrency":{{ .StreamerConcurrency }}}'
{{- else if .ModelS3URI }}
            # Streamer disabled (streamer_mode=off on the original run).
            - "--model"
            - "{{ .ModelS3URI }}"
{{- else }}
            - "--model"
            - "{{ .ModelHfID }}"
{{- end }}
            - "--port"
            - "8000"
            - "--tensor-parallel-size"
            - "{{ .TensorParallelDegree }}"
            - "--trust-remote-code"
{{- if not .UseRunaiStreamer }}
{{- if eq .Quantization "fp16" }}
            - "--dtype"
            - "float16"
{{- else if eq .Quantization "int8" }}
            - "--quantization"
            - "bitsandbytes"
            - "--load-format"
            - "bitsandbytes"
{{- else if eq .Quantization "int4" }}
            - "--quantization"
            - "gptq"
{{- end }}
{{- end }}
{{- if gt .MaxModelLen 0 }}
            - "--max-model-len"
            - "{{ .MaxModelLen }}"
{{- end }}
{{- if gt .MaxNumBatchedTokens 0 }}
            - "--max-num-batched-tokens"
            - "{{ .MaxNumBatchedTokens }}"
{{- end }}
{{- if gt .MaxNumSeqs 0 }}
            - "--max-num-seqs"
            - "{{ .MaxNumSeqs }}"
{{- end }}
{{- if .KVCacheDtype }}
            - "--kv-cache-dtype"
            - "{{ .KVCacheDtype }}"
{{- end }}
{{- else }}
          command: ["vllm"]
          args:
            - "serve"
            - "{{ .ModelHfID }}"
            - "--port"
            - "8000"
            - "--tensor-parallel-size"
            - "{{ .TensorParallelDegree }}"
            - "--trust-remote-code"
            - "--block-size"
            - "32"
{{- if gt .MaxModelLen 0 }}
            - "--max-model-len"
            - "{{ .MaxModelLen }}"
{{- end }}
{{- if gt .MaxNumBatchedTokens 0 }}
            - "--max-num-batched-tokens"
            - "{{ .MaxNumBatchedTokens }}"
{{- end }}
{{- if gt .MaxNumSeqs 0 }}
            - "--max-num-seqs"
            - "{{ .MaxNumSeqs }}"
{{- end }}
{{- end }}
          resources:
            requests:
              cpu: {{ .CPURequest }}
              memory: {{ .MemoryRequest }}
{{- if eq .AcceleratorType "gpu" }}
              nvidia.com/gpu: "{{ .AcceleratorCount }}"
            limits:
              nvidia.com/gpu: "{{ .AcceleratorCount }}"
          volumeMounts:
            - name: shm
              mountPath: /dev/shm
{{- else }}
              aws.amazon.com/neuron: "{{ div .AcceleratorCount 2 }}"
            limits:
              aws.amazon.com/neuron: "{{ div .AcceleratorCount 2 }}"
{{- end }}
          readinessProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 60
          startupProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: 30
            periodSeconds: 10
{{- if eq .AcceleratorType "gpu" }}
            failureThreshold: 120
{{- else }}
            # Neuron compilation can take 30-60+ minutes
            failureThreshold: 540
{{- end }}
          livenessProbe:
            httpGet:
              path: /health
              port: http
            periodSeconds: 30
            timeoutSeconds: 5
            failureThreshold: 3
{{- if eq .AcceleratorType "gpu" }}
      volumes:
        - name: shm
          emptyDir:
            medium: Memory
            sizeLimit: {{ .ShmSize }}
{{- end }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .Name }}
  labels:
    app.kubernetes.io/name: {{ .Name }}
    app.kubernetes.io/component: model-server
spec:
  type: ClusterIP
  ports:
    - name: http
      port: 8000
      targetPort: http
      protocol: TCP
  selector:
    app.kubernetes.io/name: {{ .Name }}
`))

// handleExportRunCSV returns a CSV of a single benchmark run's metadata + metrics (PRD-41).
func (s *Server) handleExportRunCSV(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")

	run, err := s.repo.GetBenchmarkRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	metrics, _ := s.repo.GetMetricsByRunID(r.Context(), runID)     // nil-safe inside generator
	details, _ := s.repo.GetRunExportDetails(r.Context(), runID)    // nil-safe inside generator
	// PRD-59: attach the per-node/per-role breakdown so a distributed run's CSV
	// carries the shard rows. Empty for single-node runs → CSV byte-unchanged.
	if metrics != nil {
		if shards, serr := s.repo.GetShardMetrics(r.Context(), runID); serr == nil {
			metrics.Shards = shards
		}
	}

	// Best-effort pricing lookup.
	var hourlyRate *float64
	if details != nil {
		if rows, err := s.repo.ListPricing(r.Context(), "us-east-2"); err == nil {
			for _, row := range rows {
				if row.InstanceTypeName == details.InstanceTypeName {
					rate := row.OnDemandHourlyUSD
					hourlyRate = &rate
					break
				}
			}
		}
	}

	data, err := report.GenerateRunCSV(run, metrics, details, hourlyRate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate run csv: %v", err))
		return
	}

	model := "run"
	if details != nil {
		model = sanitizeFilename(details.ModelHfID)
	}
	shortID := runID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("accelbench-run-%s-%s.csv", model, shortID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleExportSuiteCSV returns a one-row-per-scenario CSV for a test suite run (PRD-41).
func (s *Server) handleExportSuiteCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	suite, err := s.repo.GetTestSuiteRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suite == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}

	results, err := s.repo.GetScenarioResults(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get scenario results: "+err.Error())
		return
	}

	model, _ := s.repo.GetModelByID(r.Context(), suite.ModelID)
	instance, _ := s.repo.GetInstanceTypeByID(r.Context(), suite.InstanceTypeID)

	data, err := report.GenerateSuiteCSV(suite, results, model, instance)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate suite csv: %v", err))
		return
	}

	name := "suite"
	if model != nil {
		name = sanitizeFilename(model.HfID)
	}
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("accelbench-suite-%s-%s.csv", name, shortID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleExportSuiteManifest returns the vLLM Deployment+Service YAML for the
// suite's model. Mirrors handleExportManifest but sources its data from
// test_suite_runs + joined model/instance rows (PRD-41).
func (s *Server) handleExportSuiteManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	suite, err := s.repo.GetTestSuiteRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suite == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}
	if suite.Status != "completed" && suite.Status != "failed" {
		writeError(w, http.StatusBadRequest, "can only export terminal suite runs")
		return
	}

	model, err := s.repo.GetModelByID(r.Context(), suite.ModelID)
	if err != nil || model == nil {
		writeError(w, http.StatusInternalServerError, "model lookup failed")
		return
	}
	instance, err := s.repo.GetInstanceTypeByID(r.Context(), suite.InstanceTypeID)
	if err != nil || instance == nil {
		writeError(w, http.StatusInternalServerError, "instance lookup failed")
		return
	}

	// Reconstruct a RunExportDetails so we can reuse generateManifest.
	details := &database.RunExportDetails{
		RunID:                  suite.ID,
		ModelHfID:              model.HfID,
		ModelS3URI:             suite.ModelS3URI,
		InstanceTypeName:       instance.Name,
		TensorParallelDegree:   suite.TensorParallelDegree,
		Quantization:           suite.Quantization,
		AcceleratorType:        instance.AcceleratorType,
		AcceleratorName:        instance.AcceleratorName,
		AcceleratorCount:       instance.AcceleratorCount,
		AcceleratorMemoryGiB:   instance.AcceleratorMemoryGiB,
		VCPUs:                  instance.VCPUs,
		MemoryGiB:              instance.MemoryGiB,
		StreamerMode:           suite.StreamerMode,
		StreamerConcurrency:    suite.StreamerConcurrency,
		StreamerMemoryLimitGiB: suite.StreamerMemoryLimitGiB,
	}
	details.MaxModelLen = suite.MaxModelLen
	details.MaxNumBatchedTokens = suite.MaxNumBatchedTokens
	details.KVCacheDtype = suite.KVCacheDtype
	// Resolve streamer-on decision the same way GetRunExportDetails does
	// for single-run exports.
	mode := ""
	if suite.StreamerMode != nil {
		mode = *suite.StreamerMode
	}
	details.UseRunaiStreamer = mode != "off" && suite.ModelS3URI != nil && *suite.ModelS3URI != ""
	// PRD-46: --max-num-seqs was sized at deploy time to the busiest
	// scenario in the suite; reproduce that value in the export by
	// walking the suite's scenarios and taking the max NumWorkers,
	// applying any DB-stored scenario overrides (PRD-32).
	if ts := testsuite.Get(suite.SuiteID); ts != nil {
		maxSeqs := 0
		for _, sid := range ts.Scenarios {
			sc := scenario.Get(sid)
			if sc == nil {
				continue
			}
			if ov, _ := s.repo.GetScenarioOverride(r.Context(), sid); ov != nil {
				sc = sc.Merge(&scenario.Override{
					NumWorkers: ov.NumWorkers,
					Streaming:  ov.Streaming,
					InputMean:  ov.InputMean,
					OutputMean: ov.OutputMean,
				})
			}
			if sc.NumWorkers > maxSeqs {
				maxSeqs = sc.NumWorkers
			}
		}
		details.Concurrency = maxSeqs
	}
	// Framework fields: use persisted values when available (suites
	// created after migration 026), else derive from accelerator type
	// as a safe fallback for historical rows.
	if suite.Framework != nil && *suite.Framework != "" {
		details.Framework = *suite.Framework
	} else if instance.AcceleratorType == "neuron" {
		details.Framework = "vllm-neuron"
	} else {
		details.Framework = "vllm"
	}
	if suite.FrameworkVersion != nil {
		details.FrameworkVersion = *suite.FrameworkVersion
	}
	s.injectMultinodeImageVersions(r.Context(), details)

	manifest, err := generateManifest(details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate manifest failed: %v", err))
		return
	}

	filename := fmt.Sprintf("vllm-%s-suite.yaml", sanitizeFilename(model.HfID))
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(manifest))
}

// --- Compare exports ---

// resolveCompareParams parses the shared query string format used by both
// compare export handlers and returns the fetched entries + pricing lookup.
func (s *Server) resolveCompareParams(
	r *http.Request,
) ([]database.CatalogEntry, func(string) *float64, string, string, error) {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	var cleaned []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) < 2 {
		return nil, nil, "", "", fmt.Errorf("at least two run ids are required")
	}

	region := r.URL.Query().Get("region")
	if region == "" {
		region = "us-east-2"
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "on_demand"
	}

	// Fetch only the catalog rows for the selected ids (PRD-36).
	entries, _, err := s.repo.ListCatalog(r.Context(), database.CatalogFilter{
		RunIDs: cleaned,
		Limit:  len(cleaned),
	})
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("list catalog: %w", err)
	}
	if len(entries) < 2 {
		return nil, nil, "", "", fmt.Errorf("need at least two resolvable run ids")
	}

	// Build pricing lookup for the requested region + tier.
	priceByInstance := map[string]*float64{}
	if rows, err := s.repo.ListPricing(r.Context(), region); err == nil {
		for _, row := range rows {
			var v *float64
			switch tier {
			case "reserved_1yr":
				v = row.Reserved1YrHourlyUSD
			case "reserved_3yr":
				v = row.Reserved3YrHourlyUSD
			default:
				x := row.OnDemandHourlyUSD
				v = &x
			}
			if v != nil {
				priceByInstance[row.InstanceTypeName] = v
			}
		}
	}
	lookup := func(name string) *float64 { return priceByInstance[name] }
	return entries, lookup, tier, region, nil
}

func tierLabel(tier string) string {
	switch tier {
	case "reserved_1yr":
		return "Reserved 1yr"
	case "reserved_3yr":
		return "Reserved 3yr"
	default:
		return "On-demand"
	}
}

// handleExportCompareCSV returns a CSV of the comparison data.
func (s *Server) handleExportCompareCSV(w http.ResponseWriter, r *http.Request) {
	entries, lookup, tier, region, err := s.resolveCompareParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := report.GenerateCompareCSV(entries, lookup, tierLabel(tier), region)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate compare csv: %v", err))
		return
	}
	filename := fmt.Sprintf("accelbench-compare-%d-runs.csv", len(entries))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
