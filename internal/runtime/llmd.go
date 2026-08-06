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

// llmdImageRepo is the AWS-optimized llm-d image repo (vLLM + EFA + libfabric).
// The tag comes from ToolVersions.LLMDVersion (PRD-66 Part 2), defaulting to
// DefaultLLMDVersion. NOTE: llm-d releases version INDEPENDENTLY of the vLLM
// engine bundled inside — so the tag is NOT derived from the run's vLLM
// FrameworkVersion (that would ask GHCR for e.g. v0.19.0, which doesn't exist).
const llmdImageRepo = "ghcr.io/llm-d/llm-d-aws"

// llmdImagePath is the repo path WITHOUT the ghcr.io host — used when routing
// through the ECR pull-through cache, whose "ghcr" prefix already maps to
// ghcr.io (PRD-66 Part 2a). i.e. <registry>/ghcr/llm-d/llm-d-aws.
const llmdImagePath = "llm-d/llm-d-aws"

// DefaultLLMDVersion is the known-good llm-d-aws tag used when tool_versions
// hasn't set one; bump deliberately. Kept in sync with migration 042's default
// and the export path.
const DefaultLLMDVersion = "v0.8.1"

// LLMDImage composes the llm-d-aws image ref from a version tag, optionally
// routed through the GHCR ECR pull-through cache (PRD-66 Part 2a). Exported so
// the export handler renders the exact image the orchestrator deploys (one
// resolver, no drifting hardcode). An empty version falls back to
// DefaultLLMDVersion. When pullThroughRegistry is set, the image becomes
// <registry>/ghcr/llm-d/llm-d-aws:<ver> — the "ghcr" prefix is the ECR
// pull-through rule that maps to ghcr.io (mirrors the D/P dockerhub prefix).
// Empty pullThroughRegistry ⇒ direct GHCR pull (backwards-compatible).
func LLMDImage(version, pullThroughRegistry string) string {
	if version == "" {
		version = DefaultLLMDVersion
	}
	if pullThroughRegistry != "" {
		return fmt.Sprintf("%s/ghcr/%s:%s", pullThroughRegistry, llmdImagePath, version)
	}
	return llmdImageRepo + ":" + version
}

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
	// `version` here is the llm-d-aws tag (ToolVersions.LLMDVersion via
	// ResolveVersion), NOT the run's vLLM FrameworkVersion. Use LLMD_IMAGE to
	// pin a specific llm-d image; otherwise compose the repo + configured tag,
	// routed through the GHCR pull-through cache when one is configured
	// (PRD-66 Part 2a).
	return LLMDImage(version, pullThroughRegistry)
}

func (l *LLMD) ResolveVersion(tv ToolVersions) string {
	// The llm-d-aws image tag — its OWN release line, not the vLLM
	// FrameworkVersion (which for an llm-d run is the bundled vLLM engine
	// version and has no matching GHCR tag).
	return tv.LLMDVersion
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
		args = append(args, "--model-loader-extra-config", streamerExtraConfig(p))
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
