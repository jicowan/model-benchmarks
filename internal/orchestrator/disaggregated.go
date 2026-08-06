package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/manifest"
	"github.com/accelbench/accelbench/internal/runtime"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PRD-58 prefill/decode disaggregation defaults. These are docs-grounded and
// validated live (terraform/manifests/pd-layer1-epp-reference.yaml); overridable
// via env for installs that pin different images.
//
// The PD path uses the UPSTREAM vLLM image (it ships the cu13 NIXL/UCX modules
// the KV transfer needs), NOT the llm-d-aws image the co-located path uses.
const (
	// defaultPDModelRepo is the upstream vLLM image repo; the tag comes from
	// tool_versions.pd_vllm_version (PRD-66 Part 2), defaulting to
	// DefaultPDVLLMVersion. DISTINCT from single-node vLLM (FrameworkVersion):
	// D/P pins a cu13/NIXL-specific vLLM that legitimately differs.
	defaultPDModelRepo = "vllm/vllm-openai"
	// DefaultPDVLLMVersion is the known-good D/P vLLM tag when tool_versions
	// hasn't set one. Kept in sync with migration 042's default + the export
	// path. Exported so the export handler shares the resolver.
	DefaultPDVLLMVersion    = "v0.25.0"
	defaultPDSidecarImage   = "ghcr.io/llm-d/llm-d-router-disagg-sidecar:v0.9.0"
	defaultPDEPPImage       = "ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0"
	defaultPDNixlModuleDir  = "/usr/local/lib/python3.12/dist-packages/nixl_cu13.libs/ucx"
	defaultPDNonCachedToken = 16 // EPP disaggregation trigger (uncached prompt-suffix tokens)

	// PRD-61: EPP EndpointPickerConfig defaults — the shipped values. A run that
	// supplies no routing overrides renders byte-identically to pre-PRD-61.
	defaultPDPrefixCacheScorerWeight = 2
	defaultPDQueueScorerWeight       = 1
	defaultPDMaxPrefixBlocks         = 256
	defaultPDLRUCapacity             = 31250
)

// modelLabelValue turns a HuggingFace model id into a DNS-1123-label-safe value
// for the llm-d.ai/model pod/pool selector (lowercase alnum + '-', <=63 chars).
// The exact string doesn't matter as long as pods and the InferencePool agree;
// it is NOT parsed back into a model id.
func modelLabelValue(hfID string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(hfID) {
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
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if s == "" {
		s = "model"
	}
	return s
}

// azFromPoolName extracts the AZ from a static multinode pool name of the form
// "multinode-<az>" (e.g. "multinode-us-east-2a" → "us-east-2a"). Returns "" for
// a name that doesn't encode an AZ (e.g. an override/scratch pool), so the
// caller leaves the EPP zone-unconstrained. The AZ suffix must look like a
// region+zone-letter (contains a digit and ends in a letter) to avoid
// mis-parsing an arbitrary suffix.
func azFromPoolName(pool string) string {
	const prefix = "multinode-"
	if !strings.HasPrefix(pool, prefix) {
		return ""
	}
	az := strings.TrimPrefix(pool, prefix)
	// A real AZ looks like "us-east-2a": has a digit and ends with a lowercase
	// zone letter. This rejects "tcp" and other non-AZ suffixes.
	if az == "" {
		return ""
	}
	last := az[len(az)-1]
	if last < 'a' || last > 'z' {
		return ""
	}
	if !strings.ContainsAny(az, "0123456789") {
		return ""
	}
	return az
}

// PDModelImage composes the D/P vLLM image (vllm/vllm-openai) from a version
// tag, optionally routed through the Docker Hub ECR pull-through cache when a
// registry host is given (PRD-66 Part 2). Exported so the export handler shares
// this exact resolver — no drifting hardcode. An empty version falls back to
// DefaultPDVLLMVersion.
func PDModelImage(version, pullThroughRegistry string) string {
	if version == "" {
		version = DefaultPDVLLMVersion
	}
	img := defaultPDModelRepo + ":" + version
	if pullThroughRegistry != "" {
		return fmt.Sprintf("%s/dockerhub/%s", pullThroughRegistry, img)
	}
	return img
}

// resolvePDVLLMVersion returns the configured D/P vLLM tag (PRD-66 Part 2),
// falling back to the shipped default when tool_versions is unavailable/unset.
// DISTINCT from the run's vLLM FrameworkVersion (single-node) — D/P pins a
// cu13/NIXL-specific vLLM.
func (o *Orchestrator) resolvePDVLLMVersion(ctx context.Context) string {
	if tv, err := o.repo.GetToolVersions(ctx); err == nil && tv != nil && tv.PDVLLMVersion != "" {
		return tv.PDVLLMVersion
	}
	return DefaultPDVLLMVersion
}

// deployLLMDDisaggregated renders the PD-disaggregated object graph (two pod
// groups + InferencePool + EPP) from the run's per-role topology and applies it
// via the dynamic client, tracking every object for teardown. Called from
// deployModel when cfg.IsDisaggregated(). Mirrors deployLLMD (PRD-56) but for
// the two-group shape.
func (o *Orchestrator) deployLLMDDisaggregated(ctx context.Context, ns, name string, cfg RunConfig) error {
	rt, err := runtime.Get(cfg.Request.Framework)
	if err != nil {
		return err
	}

	// S3-backed models load via the Run:ai streamer, same as single-node
	// (PRD-65 Layer 2): explicit URI wins, else auto-detect a cached model.
	// Safe on D/P because the upstream vllm/vllm-openai image bundles
	// runai-model-streamer. (PP/llm-d-aws does NOT bundle it — deferred.)
	modelS3URI, useRunai := o.resolveS3Model(ctx, cfg)

	prefillTP := cfg.PrefillTP
	if prefillTP < 1 {
		prefillTP = 1
	}
	decodeTP := cfg.DecodeTP
	if decodeTP < 1 {
		decodeTP = 1
	}
	// Prefill/decode clamp their replica floor to 1 UNLESS the other roles
	// cover the topology — a "both"-only run legitimately sets both to 0. The
	// API validates the combination (at least one decode-capable pool); here we
	// only avoid rewriting a deliberate 0 up to 1. bothReplicas defaults to 0
	// (no both pool) and clamps a negative to 0.
	bothReplicas := cfg.BothReplicas
	if bothReplicas < 0 {
		bothReplicas = 0
	}
	bothTP := cfg.BothTP
	if bothTP < 1 {
		bothTP = 1
	}
	prefillReplicas := cfg.PrefillReplicas
	if prefillReplicas < 0 {
		prefillReplicas = 0
	}
	decodeReplicas := cfg.DecodeReplicas
	if decodeReplicas < 0 {
		decodeReplicas = 0
	}
	// If no role is set at all (defensive — the API rejects this), fall back to
	// the historical 1P1D minimum so we never render an empty graph.
	if prefillReplicas == 0 && decodeReplicas == 0 && bothReplicas == 0 {
		prefillReplicas, decodeReplicas = 1, 1
	}

	// ServeArgs = model positional + static tuning flags. The per-role TP,
	// port, and KV-transfer config are appended by the template. All the
	// model-identity knobs (MaxModelLen, KVCacheDtype, Quantization) are shared
	// across roles — only MaxNumBatchedTokens (the scheduler knob) may differ
	// per role (PRD-64). buildServeArgs holds everything but that one knob fixed.
	// PRD-65 Layer 3: streamer memory-limit parity with single-node. 0 →
	// auto-size to half the node RAM (the streamer caps its shared CPU buffer
	// against the weight size at load). Only meaningful when the streamer is on.
	streamerMemLimitGiB := cfg.Request.StreamerMemoryLimitGiB
	if streamerMemLimitGiB == 0 {
		streamerMemLimitGiB = max(1, cfg.InstanceType.MemoryGiB/2)
	}
	buildServeArgs := func(maxNumBatchedTokens int) []string {
		_, args := rt.BuildArgs(runtime.ContainerParams{
			ModelHfID:              cfg.Request.ModelHfID,
			ModelS3URI:             modelS3URI,
			UseRunaiStreamer:       useRunai,
			MaxModelLen:            cfg.Request.MaxModelLen,
			MaxNumBatchedTokens:    maxNumBatchedTokens,
			KVCacheDtype:           cfg.Request.KVCacheDtype,
			Quantization:           derefStr(cfg.Request.Quantization),
			StreamerConcurrency:    cfg.Request.StreamerConcurrency,
			StreamerMemoryLimitGiB: streamerMemLimitGiB,
			AcceleratorName:        cfg.InstanceType.AcceleratorName,
		})
		return args
	}
	// Shared arg set (the default). Per-role sets are built ONLY when a role
	// override is set — otherwise they stay nil and the template falls back to
	// the shared set, keeping the render byte-identical to pre-PRD-64.
	serveArgs := buildServeArgs(cfg.Request.MaxNumBatchedTokens)
	var prefillServeArgs, decodeServeArgs, bothServeArgs []string
	if cfg.PrefillMaxNumBatchedTokens > 0 {
		prefillServeArgs = buildServeArgs(cfg.PrefillMaxNumBatchedTokens)
	}
	if cfg.DecodeMaxNumBatchedTokens > 0 {
		decodeServeArgs = buildServeArgs(cfg.DecodeMaxNumBatchedTokens)
	}
	if cfg.BothMaxNumBatchedTokens > 0 {
		bothServeArgs = buildServeArgs(cfg.BothMaxNumBatchedTokens)
	}

	var modelServiceAccount string
	if useRunai {
		modelServiceAccount = "accelbench-model"
	}

	// The PD path uses the upstream vLLM image (cu13 NIXL modules), not
	// llm-d-aws. Honor a runtime image override if the operator set one; an
	// explicit override is used verbatim (it's an exact image ref). Otherwise use
	// the default and, when a pull-through registry is configured, route it
	// through the Docker Hub ECR pull-through cache — same as the single-node
	// path (vllm_gpu.go) — to avoid a slow/rate-limited direct Docker Hub pull.
	image := rt.ResolveImageOverride()
	if image == "" {
		// No VLLM_IMAGE/LLMD_IMAGE override. A PD_MODEL_IMAGE env var is an
		// exact image ref used verbatim; otherwise compose vllm/vllm-openai from
		// the configured pd_vllm_version (PRD-66 Part 2) and, when a pull-through
		// registry is set, route it through the Docker Hub ECR pull-through cache
		// — same as the single-node path (vllm_gpu.go).
		if pd := envOr("PD_MODEL_IMAGE", ""); pd != "" {
			image = pd
		} else {
			image = PDModelImage(o.resolvePDVLLMVersion(ctx), envOr("PULL_THROUGH_REGISTRY", ""))
		}
	}

	cpuReq := fmt.Sprintf("%d", max(1, cfg.InstanceType.VCPUs*3/4))
	memReq := fmt.Sprintf("%dGi", max(1, cfg.InstanceType.MemoryGiB*85/100))

	// PRD-61: resolve run-tunable EPP routing knobs, falling back to the shipped
	// defaults so a run that supplies nothing renders byte-identically. The EPP
	// ConfigMap is part of the per-run object graph applied here and torn down at
	// the end, so each run pins its own routing config at EPP boot — the
	// startup-only model needs no extra restart machinery.
	// nonCachedTokens is a POINTER: 0 is a meaningful value (disable PD), so only
	// a nil pointer means "use the default 16".
	nonCachedTokens := defaultPDNonCachedToken
	if cfg.PDNonCachedTokens != nil {
		nonCachedTokens = *cfg.PDNonCachedTokens
	}
	prefixWeight := valOrDefault(cfg.PDPrefixCacheScorerWeight, defaultPDPrefixCacheScorerWeight)
	queueWeight := valOrDefault(cfg.PDQueueScorerWeight, defaultPDQueueScorerWeight)
	maxPrefixBlocks := valOrDefault(cfg.PDMaxPrefixBlocks, defaultPDMaxPrefixBlocks)
	lruCapacity := valOrDefault(cfg.PDLRUCapacityPerServer, defaultPDLRUCapacity)

	// Co-locate the EPP with the serving pods' AZ. The pool was selected +
	// recorded in distributedState by acquireDistributedPool (runs before this),
	// and its name encodes the AZ (multinode-<az> / multinode-tcp is name-only —
	// so azFromPoolName returns "" for a non-AZ pool and the EPP stays
	// unconstrained). Empty ⇒ no zone nodeSelector.
	o.mu.Lock()
	var selectedPool string
	if st := o.distributed[name]; st != nil {
		selectedPool = st.poolName
	}
	o.mu.Unlock()
	eppZone := azFromPoolName(selectedPool)

	yamlStr, err := manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name:                name,
		Namespace:           ns,
		Image:               image,
		ServeArgs:           serveArgs,
		PrefillServeArgs:    prefillServeArgs,
		DecodeServeArgs:     decodeServeArgs,
		BothServeArgs:       bothServeArgs,
		ContainerName:       rt.ContainerName(),
		ModelHfID:           cfg.Request.ModelHfID,
		ModelLabel:          modelLabelValue(cfg.Request.ModelHfID),
		HfToken:             o.resolveHFToken(ctx, cfg.Request.HfToken),
		ModelServiceAccount: modelServiceAccount,
		StreamerMemoryLimitGiB: func() int {
			if useRunai {
				return streamerMemLimitGiB
			}
			return 0
		}(),
		InstanceTypeName:    cfg.InstanceType.Name,
		PrefillReplicas:     prefillReplicas,
		PrefillTP:           prefillTP,
		DecodeReplicas:      decodeReplicas,
		DecodeTP:            decodeTP,
		BothReplicas:        bothReplicas,
		BothTP:              bothTP,
		CPURequest:          cpuReq,
		MemoryRequest:       memReq,
		NetworkMode:         cfg.networkMode(),
		NixlModuleDir:       envOr("PD_NIXL_MODULE_DIR", defaultPDNixlModuleDir),
		EPPImage:            envOr("PD_EPP_IMAGE", defaultPDEPPImage),
		SidecarImage:        envOr("PD_SIDECAR_IMAGE", defaultPDSidecarImage),
		EPPZone:                 eppZone,
		NonCachedTokens:         nonCachedTokens,
		PrefixCacheScorerWeight: prefixWeight,
		QueueScorerWeight:       queueWeight,
		MaxPrefixBlocksToMatch:  maxPrefixBlocks,
		LRUCapacityPerServer:    lruCapacity,
		GPUDeviceClass:      envOr("DRA_GPU_DEVICE_CLASS", defaultGPUDeviceClass),
		GatewayName:         envOr("LLMD_GATEWAY_NAME", defaultGatewayName),
		GatewayNamespace:    envOr("LLMD_GATEWAY_NAMESPACE", defaultGatewayNamespace),
		MultiNodeTaintKey:   multinodeTaintKey,
		MultiNodeTaintValue: multinodeTaintVal,
		DRANodeSelectorKey:  draNodeSelectorKey,
		DRANodeSelectorVal:  draNodeSelectorVal,
	})
	if err != nil {
		return fmt.Errorf("render disaggregated manifest set: %w", err)
	}

	applied, err := o.applyManifestSet(ctx, ns, yamlStr)
	o.mu.Lock()
	if st := o.distributed[name]; st != nil {
		st.applied = applied
	}
	o.mu.Unlock()
	if err != nil {
		return fmt.Errorf("apply disaggregated manifest set: %w", err)
	}
	log.Printf("[%s] applied disaggregated object graph: %d objects (%dP%dD%dB, prefill TP=%d, decode TP=%d, both TP=%d)",
		cfg.RunID[:8], len(applied), prefillReplicas, decodeReplicas, bothReplicas, prefillTP, decodeTP, bothTP)
	return nil
}

// waitForDisaggregatedReady blocks until BOTH the prefill and decode
// Deployments have their full replica count Ready AND each role's Service has a
// ready serving endpoint (the precondition the gateway/EPP needs to route),
// scanning for OOMs along the way. The EPP Deployment readiness is implied by
// the InferencePool having endpoints; we gate on the model roles, which are the
// slow part (image pull + weight load). Extends the PRD-56 LWS-readiness
// discipline to two Deployments.
func (o *Orchestrator) waitForDisaggregatedReady(ctx context.Context, ns, name string, cfg RunConfig) error {
	deadline := time.Now().Add(distributedReadinessTimeout)
	prefillDep := name + "-prefill"
	decodeDep := name + "-decode"
	bothDep := name + "-both"
	// PRD-63: a role is present only when it has replicas > 0. Gate each role's
	// readiness/endpoint check on presence so a "both"-only (or both+prefill)
	// run doesn't block on a Deployment that was never rendered. A normal PD run
	// has prefill+decode present and no both, so its wait is unchanged.
	havePrefill := cfg.PrefillReplicas > 0
	haveDecode := cfg.DecodeReplicas > 0
	haveBoth := cfg.BothReplicas > 0
	if !havePrefill && !haveDecode && !haveBoth {
		// Defensive: match the render fallback (1P1D) so we wait on what deployed.
		havePrefill, haveDecode = true, true
	}
	for time.Now().Before(deadline) {
		prefillReady := !havePrefill || o.deploymentFullyReady(ctx, ns, prefillDep)
		decodeReady := !haveDecode || o.deploymentFullyReady(ctx, ns, decodeDep)
		bothReady := !haveBoth || o.deploymentFullyReady(ctx, ns, bothDep)

		if prefillReady && decodeReady && bothReady {
			// All present groups report Ready; confirm each present role's Service
			// actually has a ready endpoint before declaring success (same race the
			// co-located path hit — group-ready can precede a servable endpoint).
			prefillEP := !havePrefill || o.serviceHasReadyEndpoint(ctx, ns, prefillDep)
			decodeEP := !haveDecode || o.serviceHasReadyEndpoint(ctx, ns, decodeDep)
			bothEP := !haveBoth || o.serviceHasReadyEndpoint(ctx, ns, bothDep)
			if prefillEP && decodeEP && bothEP {
				log.Printf("[%s] disaggregated groups ready and serving endpoints live", cfg.RunID[:8])
				return nil
			}
			log.Printf("[%s] groups ready but a serving endpoint not populated yet; waiting", cfg.RunID[:8])
		} else {
			log.Printf("[%s] waiting for disaggregated groups: prefill=%v decode=%v both=%v", cfg.RunID[:8], prefillReady, decodeReady, bothReady)
		}

		// OOM scan across all pods of the run (both roles).
		pods, _ := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", name),
		})
		for _, pod := range pods.Items {
			events, err := o.oomDetector.CheckPod(ctx, pod.Name)
			if err == nil && len(events) > 0 {
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
	return fmt.Errorf("disaggregated groups not ready after %v", distributedReadinessTimeout)
}

// deploymentFullyReady reports whether a Deployment has all desired replicas
// Ready. Missing/erroring lookups return false (keep waiting).
func (o *Orchestrator) deploymentFullyReady(ctx context.Context, ns, name string) bool {
	dep, err := o.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false
	}
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	return dep.Status.ReadyReplicas >= desired
}
