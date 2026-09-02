package runtime

import (
	"fmt"
	"os"
	"strconv"
)

// DefaultVLLMCPUVersion is the known-good vllm-openai-cpu arm64 tag used when
// tool_versions.vllm_cpu_version is unset. DISTINCT from FrameworkVersion (the
// GPU vllm/vllm-openai tag): the CPU image is a different repo (`-cpu`) with its
// own arm64 release cadence (PRD-67 §10a). Verified against Docker Hub
// (vllm/vllm-openai-cpu tags): arm64 images exist from v0.18.0 onward; pin a
// recent stable tag. (The "since v0.11.2" figure is for prebuilt Arm *wheels*,
// which predate the published -cpu images — do NOT use it as an image tag.)
const DefaultVLLMCPUVersion = "v0.27.0"

// VLLMcpu implements Runtime for the "vllm-cpu" framework on ARM/Graviton CPU
// instances. It mirrors VLLMneuron as a third accelerator tier: single-node,
// no discrete accelerator device, quantization-forward (PRD-67 §1).
//
// The container image is vLLM's prebuilt ARM CPU image
// (vllm/vllm-openai-cpu:v<ver>-arm64). The `-arm64` suffix is load-bearing —
// there is also an amd64 CPU image we do NOT use. Weights load via the same S3 +
// Run:ai streamer path as VLLMgpu (arm64 streamer wheels exist), with
// distributed streaming gated off for CPU (§5b).
type VLLMcpu struct{}

func (v *VLLMcpu) Name() string                    { return "vllm-cpu" }
func (v *VLLMcpu) ContainerName() string           { return "vllm" }
func (v *VLLMcpu) SupportedAccelerators() []string { return []string{"cpu"} }

// ResolveImageOverride honors VLLM_CPU_IMAGE (mirrors VLLM_IMAGE / PRD-49),
// distinct from the GPU override so an operator can pin the CPU image alone.
func (v *VLLMcpu) ResolveImageOverride() string {
	return os.Getenv("VLLM_CPU_IMAGE")
}

// DefaultImage composes vllm/vllm-openai-cpu:v<ver>-arm64, routed through the
// existing Docker Hub ECR pull-through cache when a registry is set (identical
// composition to VLLMgpu — the CPU image is on Docker Hub too, so it reuses the
// dockerhub rule; no new Terraform, PRD-67 §10b).
func (v *VLLMcpu) DefaultImage(version, pullThroughRegistry string) string {
	tag := fmt.Sprintf("%s-arm64", version)
	if pullThroughRegistry != "" {
		return fmt.Sprintf("%s/dockerhub/vllm/vllm-openai-cpu:%s", pullThroughRegistry, tag)
	}
	return fmt.Sprintf("vllm/vllm-openai-cpu:%s", tag)
}

// ResolveVersion reads the CPU-specific tag; it is NOT FrameworkVersion (that's
// the GPU vllm/vllm-openai tag). Falls back to DefaultVLLMCPUVersion.
func (v *VLLMcpu) ResolveVersion(tv ToolVersions) string {
	if tv.VLLMCPUVersion == "" {
		return DefaultVLLMCPUVersion
	}
	return tv.VLLMCPUVersion
}

// BuildArgs renders the CPU serve command. Differences from VLLMgpu (PRD-67 §1,
// §5 knob table):
//   - --dtype bfloat16 always: torch CPU float16 is unstable (vLLM CPU docs);
//     set explicitly rather than relying on auto.
//   - --tensor-parallel-size = NUMA-node count, supplied by the caller in
//     TensorParallelDegree (the recommender derives it from the instance's NUMA
//     topology, NOT AcceleratorCount which is 0 for CPU rows).
//   - NO --gpu-memory-utilization / --kv-cache-dtype (GPU-only; see the CPU
//     guards). KV cache is sized out-of-band via VLLM_CPU_KVCACHE_SPACE (env,
//     set in the manifest, not a serve arg).
//   - Same S3 + Run:ai streamer path as GPU; Accelerator="cpu" suppresses
//     distributed:true in the shared streamerExtraConfig.
func (v *VLLMcpu) BuildArgs(p ContainerParams) (command []string, args []string) {
	// Force the accelerator tag so the shared streamer helper gates distributed
	// off even if the caller didn't set it.
	p.Accelerator = "cpu"

	if p.UseRunaiStreamer {
		args = append(args, "--model", p.ModelS3URI)
		args = append(args, "--load-format", "runai_streamer")
		args = append(args, "--model-loader-extra-config")
		args = append(args, streamerExtraConfig(p))
	} else {
		args = append(args, "--model", p.ModelHfID)
	}

	args = append(args, "--port", "8000")
	// TP = NUMA-node count on CPU (each NUMA node is a TP rank per vLLM CPU docs).
	args = append(args, "--tensor-parallel-size", strconv.Itoa(p.TensorParallelDegree))
	args = append(args, "--trust-remote-code")
	// bfloat16 required on CPU (float16 unstable on torch CPU).
	args = append(args, "--dtype", "bfloat16")

	// Quantization: CPU wants W8A8 / W4A8 (Arm KleidiAI kernels). Suppressed when
	// streaming (same rule as GPU — the streamed checkpoint carries its own format).
	if !p.UseRunaiStreamer {
		args = append(args, v.MapQuantization(p.Quantization, p.UseRunaiStreamer)...)
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

	return nil, args
}

// MapQuantization maps the run's quantization knob to CPU-appropriate flags.
// CPU uses INT8 W8A8 / INT4 W4A8 (compressed-tensors / Arm KleidiAI), NOT the
// GPU's bitsandbytes. Suppressed when streaming (the checkpoint is pre-quantized).
//
// NOTE (PRD-67 grounding): ARM W8A8/W4A8 support is documented in the vLLM Arm
// blog but not yet in the stable CPU support matrix — validate on the real
// arm64 image before relying on it; if unsupported the tier degrades to bf16.
func (v *VLLMcpu) MapQuantization(quant string, useRunaiStreamer bool) []string {
	if useRunaiStreamer {
		return nil
	}
	switch quant {
	case "int8":
		return []string{"--quantization", "compressed-tensors"}
	case "int4":
		return []string{"--quantization", "compressed-tensors"}
	default:
		return nil
	}
}
