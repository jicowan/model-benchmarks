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

// defaultLLMDImage is the AWS-optimized llm-d image (vLLM + EFA + libfabric),
// pinned to the version PRD-55 validated by hand. Overridable via LLMD_IMAGE
// (and, for parity with the vLLM runtime's PRD-49 override, VLLM_IMAGE as a
// fallback).
const defaultLLMDImage = "ghcr.io/llm-d/llm-d-aws:0.2.0"

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
	// llm-d ships from GHCR, not Docker Hub, so the Docker Hub pull-through
	// cache doesn't apply. Honor an explicit version tag when provided.
	if version != "" {
		return fmt.Sprintf("ghcr.io/llm-d/llm-d-aws:%s", version)
	}
	return defaultLLMDImage
}

func (l *LLMD) ResolveVersion(tv ToolVersions) string {
	return tv.FrameworkVersion
}

// BuildArgs returns the vLLM serve args for one llm-d worker/leader pod. Both
// the tensor-parallel degree (within a node) and the pipeline-parallel degree
// (across nodes) are emitted; the LWS leader coordinates the workers over the
// EFA fabric using the NCCL/libfabric env the manifest wires in (see
// internal/manifest/templates/llmd-deployment.yaml.tmpl).
func (l *LLMD) BuildArgs(p ContainerParams) (command []string, args []string) {
	tp := p.TensorParallelDegree
	if tp < 1 {
		tp = 1
	}
	pp := p.PipelineParallelDegree
	if pp < 1 {
		pp = 1
	}

	if p.UseRunaiStreamer {
		args = append(args, "--model", p.ModelS3URI)
		args = append(args, "--load-format", "runai_streamer")
		args = append(args, "--model-loader-extra-config",
			fmt.Sprintf(`{"concurrency":%d}`, streamerConcurrencyOrDefault(p.StreamerConcurrency)))
	} else {
		args = append(args, "--model", p.ModelHfID)
	}

	args = append(args, "--port", "8000")
	args = append(args, "--tensor-parallel-size", strconv.Itoa(tp))
	args = append(args, "--pipeline-parallel-size", strconv.Itoa(pp))
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

	return nil, args
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
