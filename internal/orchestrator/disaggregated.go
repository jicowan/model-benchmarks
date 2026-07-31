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
	defaultPDModelImage     = "vllm/vllm-openai:v0.25.0"
	defaultPDSidecarImage   = "ghcr.io/llm-d/llm-d-router-disagg-sidecar:v0.9.0"
	defaultPDEPPImage       = "ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0"
	defaultPDNixlModuleDir  = "/usr/local/lib/python3.12/dist-packages/nixl_cu13.libs/ucx"
	defaultPDNonCachedToken = 16 // EPP disaggregation trigger (uncached prompt-suffix tokens)
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

	// S3-backed models load via the Run:ai streamer, same as elsewhere.
	var modelS3URI string
	useRunai := false
	if cfg.Request.ModelS3URI != "" {
		modelS3URI = cfg.Request.ModelS3URI
		useRunai = true
	}

	prefillTP := cfg.PrefillTP
	if prefillTP < 1 {
		prefillTP = 1
	}
	decodeTP := cfg.DecodeTP
	if decodeTP < 1 {
		decodeTP = 1
	}
	prefillReplicas := cfg.PrefillReplicas
	if prefillReplicas < 1 {
		prefillReplicas = 1
	}
	decodeReplicas := cfg.DecodeReplicas
	if decodeReplicas < 1 {
		decodeReplicas = 1
	}

	// ServeArgs = model positional + static tuning flags. The per-role TP,
	// port, and KV-transfer config are appended by the template.
	_, serveArgs := rt.BuildArgs(runtime.ContainerParams{
		ModelHfID:           cfg.Request.ModelHfID,
		ModelS3URI:          modelS3URI,
		UseRunaiStreamer:    useRunai,
		MaxModelLen:         cfg.Request.MaxModelLen,
		MaxNumBatchedTokens: cfg.Request.MaxNumBatchedTokens,
		KVCacheDtype:        cfg.Request.KVCacheDtype,
		Quantization:        derefStr(cfg.Request.Quantization),
		StreamerConcurrency: cfg.Request.StreamerConcurrency,
		AcceleratorName:     cfg.InstanceType.AcceleratorName,
	})

	var modelServiceAccount string
	if useRunai {
		modelServiceAccount = "accelbench-model"
	}

	// The PD path uses the upstream vLLM image (cu13 NIXL modules), not
	// llm-d-aws. Honor a runtime image override if the operator set one.
	image := rt.ResolveImageOverride()
	if image == "" {
		image = envOr("PD_MODEL_IMAGE", defaultPDModelImage)
	}

	cpuReq := fmt.Sprintf("%d", max(1, cfg.InstanceType.VCPUs*3/4))
	memReq := fmt.Sprintf("%dGi", max(1, cfg.InstanceType.MemoryGiB*85/100))

	yamlStr, err := manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name:                name,
		Namespace:           ns,
		Image:               image,
		ServeArgs:           serveArgs,
		ContainerName:       rt.ContainerName(),
		ModelHfID:           cfg.Request.ModelHfID,
		ModelLabel:          modelLabelValue(cfg.Request.ModelHfID),
		HfToken:             o.resolveHFToken(ctx, cfg.Request.HfToken),
		ModelServiceAccount: modelServiceAccount,
		PrefillReplicas:     prefillReplicas,
		PrefillTP:           prefillTP,
		DecodeReplicas:      decodeReplicas,
		DecodeTP:            decodeTP,
		CPURequest:          cpuReq,
		MemoryRequest:       memReq,
		NetworkMode:         cfg.networkMode(),
		NixlModuleDir:       envOr("PD_NIXL_MODULE_DIR", defaultPDNixlModuleDir),
		EPPImage:            envOr("PD_EPP_IMAGE", defaultPDEPPImage),
		SidecarImage:        envOr("PD_SIDECAR_IMAGE", defaultPDSidecarImage),
		NonCachedTokens:     defaultPDNonCachedToken,
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
	log.Printf("[%s] applied disaggregated object graph: %d objects (%dP%dD, prefill TP=%d, decode TP=%d)",
		cfg.RunID[:8], len(applied), prefillReplicas, decodeReplicas, prefillTP, decodeTP)
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
	for time.Now().Before(deadline) {
		prefillReady := o.deploymentFullyReady(ctx, ns, prefillDep)
		decodeReady := o.deploymentFullyReady(ctx, ns, decodeDep)

		if prefillReady && decodeReady {
			// Both groups report Ready; confirm each role's Service actually has
			// a ready endpoint before declaring success (same race the co-located
			// path hit — group-ready can precede a servable endpoint).
			if o.serviceHasReadyEndpoint(ctx, ns, prefillDep) && o.serviceHasReadyEndpoint(ctx, ns, decodeDep) {
				log.Printf("[%s] prefill + decode groups ready and serving endpoints live", cfg.RunID[:8])
				return nil
			}
			log.Printf("[%s] both groups ready but a serving endpoint not populated yet; waiting", cfg.RunID[:8])
		} else {
			log.Printf("[%s] waiting for disaggregated groups: prefill=%v decode=%v", cfg.RunID[:8], prefillReady, decodeReady)
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
