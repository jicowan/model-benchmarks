package runtime

import (
	"fmt"
	"os"
	"strconv"
)

// LLMD implements Runtime for the "llm-d" framework: a multi-node vLLM
// deployment spread across N GPU nodes via a LeaderWorkerSet, fronted by the
// Gateway API Inference Extension (see PRD-56). The AWS-optimized image
// bundles vLLM + EFA + libfabric, so the container command is the same
// `vllm serve` invocation a single-node run uses, plus the pipeline-parallel
// dimension across nodes.
//
// Unlike the single-container runtimes, llm-d is a *topology of pods*. The
// orchestrator detects the multi-node path via the MultiNode capability
// (IsMultiNode) rather than by branching on the framework string, and renders
// a manifest set (LWS + InferencePool + route + DRA/EFA ResourceClaimTemplates)
// instead of a single Deployment.
type LLMD struct{}

// defaultLLMDImage is the AWS-optimized llm-d image (vLLM + EFA + libfabric).
// Overridable via LLMD_IMAGE. NOTE: llm-d releases version INDEPENDENTLY of
// the vLLM engine bundled inside — so the tag is NOT derived from the run's
// vLLM FrameworkVersion (that would ask GHCR for e.g. v0.19.0, which doesn't
// exist). Pinned to a known-good llm-d-aws release; bump deliberately.
const defaultLLMDImage = "ghcr.io/llm-d/llm-d-aws:v0.8.1"

func (l *LLMD) Name() string                    { return "llm-d" }
func (l *LLMD) ContainerName() string           { return "vllm" }
func (l *LLMD) SupportedAccelerators() []string { return []string{"gpu"} }

// IsMultiNode marks this runtime as requiring the multi-node deploy path.
// The orchestrator checks for this via a type assertion so the single-node
// runtimes stay on the exact typed-client Deployment path they use today.
func (l *LLMD) IsMultiNode() bool { return true }

func (l *LLMD) ResolveImageOverride() string {
	if v := os.Getenv("LLMD_IMAGE"); v != "" {
		return v
	}
	// Fall back to VLLM_IMAGE for operators who pin one runtime image.
	return os.Getenv("VLLM_IMAGE")
}

func (l *LLMD) DefaultImage(version, pullThroughRegistry string) string {
	// `version` is the run's vLLM FrameworkVersion — deliberately IGNORED here:
	// llm-d-aws has its own release cadence and GHCR has no tag matching a vLLM
	// version. Use LLMD_IMAGE to pin a specific llm-d release; otherwise the
	// known-good default. (llm-d ships from GHCR, so the Docker Hub
	// pull-through cache doesn't apply either.)
	return defaultLLMDImage
}

func (l *LLMD) ResolveVersion(tv ToolVersions) string {
	return tv.FrameworkVersion
}

// BuildArgs returns the MODEL positional arg plus static tuning flags for the
// vLLM serve line. It deliberately does NOT emit the multi-node coordination
// flags (--data-parallel-*, --tensor-parallel-size): those depend on the LWS
// runtime env (LWS_LEADER_ADDRESS / LWS_WORKER_INDEX / LWS_GROUP_SIZE) and are
// assembled by the deployment template's shell script, mirroring the canonical
// llm-d launch (guides/wide-ep-lws — vLLM's data-parallel supervisor, NOT Ray).
// `command` is nil here; the template wraps everything in /bin/bash -c.
func (l *LLMD) BuildArgs(p ContainerParams) (command []string, args []string) {
	if p.UseRunaiStreamer {
		args = append(args, p.ModelS3URI)
		args = append(args, "--load-format", "runai_streamer")
		args = append(args, "--model-loader-extra-config",
			fmt.Sprintf(`{"concurrency":%d}`, streamerConcurrencyOrDefault(p.StreamerConcurrency)))
	} else {
		args = append(args, p.ModelHfID)
	}

	args = append(args, "--trust-remote-code")

	if !p.UseRunaiStreamer {
		args = append(args, l.MapQuantization(p.Quantization, p.UseRunaiStreamer)...)
	}
	if p.MaxModelLen > 0 {
		args = append(args, "--max-model-len", strconv.Itoa(p.MaxModelLen))
	}
	if p.MaxNumBatchedTokens > 0 {
		args = append(args, "--max-num-batched-tokens", strconv.Itoa(p.MaxNumBatchedTokens))
	}
	if p.MaxNumSeqs > 0 {
		args = append(args, "--max-num-seqs", strconv.Itoa(p.MaxNumSeqs))
	}
	if p.KVCacheDtype != "" {
		args = append(args, "--kv-cache-dtype", p.KVCacheDtype)
	}

	return command, args
}

// MapQuantization mirrors the vLLM GPU runtime — llm-d runs vLLM underneath.
func (l *LLMD) MapQuantization(quant string, useRunaiStreamer bool) []string {
	if useRunaiStreamer {
		return nil
	}
	switch quant {
	case "fp16":
		return []string{"--dtype", "float16"}
	case "int8", "int4":
		return []string{"--quantization", "bitsandbytes", "--load-format", "bitsandbytes"}
	default:
		return nil
	}
}
