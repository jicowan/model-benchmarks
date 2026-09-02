package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// VLLMgpu implements Runtime for the "vllm" framework on GPU instances.
type VLLMgpu struct{}

func (v *VLLMgpu) Name() string          { return "vllm" }
func (v *VLLMgpu) ContainerName() string  { return "vllm" }
func (v *VLLMgpu) SupportedAccelerators() []string { return []string{"gpu"} }

func (v *VLLMgpu) ResolveImageOverride() string {
	return os.Getenv("VLLM_IMAGE")
}

func (v *VLLMgpu) DefaultImage(version, pullThroughRegistry string) string {
	if pullThroughRegistry != "" {
		return fmt.Sprintf("%s/dockerhub/vllm/vllm-openai:%s", pullThroughRegistry, version)
	}
	return fmt.Sprintf("vllm/vllm-openai:%s", version)
}

func (v *VLLMgpu) ResolveVersion(tv ToolVersions) string {
	return tv.FrameworkVersion
}

func (v *VLLMgpu) BuildArgs(p ContainerParams) (command []string, args []string) {
	if p.UseRunaiStreamer {
		args = append(args, "--model", p.ModelS3URI)
		args = append(args, "--load-format", "runai_streamer")
		args = append(args, "--model-loader-extra-config")
		args = append(args, streamerExtraConfig(p))
	} else {
		args = append(args, "--model", p.ModelHfID)
	}

	args = append(args, "--port", "8000")
	args = append(args, "--tensor-parallel-size", strconv.Itoa(p.TensorParallelDegree))
	args = append(args, "--trust-remote-code")

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
	if p.KVCacheDtype != "" {
		args = append(args, "--kv-cache-dtype", p.KVCacheDtype)
	}

	return nil, args
}

func (v *VLLMgpu) MapQuantization(quant string, useRunaiStreamer bool) []string {
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

// Run:ai model streamer tuning profiles. Two published sources give two
// internally-consistent but different tuning strategies, because the optimum
// depends on the instance's network bandwidth:
//
//   - AWS EKS "fast model loading" guide (docs.aws.amazon.com/eks/latest/
//     userguide/ml-inference-fast-model-loading.html): a large 4 GiB chunk with
//     a low, size-derived thread count (concurrency = ceil(model_gb / 4)). Big
//     slabs amortize per-request S3 overhead and are explicitly recommended for
//     "high-bandwidth instances" — the ones with a NIC fat enough to fill them.
//
//   - Run:ai upstream (github.com/run-ai/runai-model-streamer): the shipped
//     object-storage default is an 8 MiB chunk with 8 threads; their S3 benchmark
//     bottoms out at concurrency 32 (plateaus at 64). Many small parallel reads.
//
// We pick per instance: high-bandwidth (>=50 Gbps, IsHighBandwidthInstance) →
// AWS profile; everything else → Run:ai profile. An explicit operator
// StreamerConcurrency overrides the concurrency in either profile.
const (
	// streamerChunkBytes is AWS's recommended 4 GiB chunk (RUNAI_STREAMER_CHUNK_BYTESIZE)
	// AND the divisor in its size-derived concurrency formula — only on high-BW instances.
	streamerChunkBytes int64 = 4 * 1024 * 1024 * 1024
	// streamerDefaultConcurrency is Run:ai's S3-benchmarked optimum, used as the
	// standard-profile default and as the high-BW cap.
	streamerDefaultConcurrency = 32
	// streamerMaxConcurrency caps the AWS size-derived value so a very large model
	// can't request an absurd number of parallel S3 connections.
	streamerMaxConcurrency = 64
	// AWS S3 retry envs (present in the run.ai C++ source; undocumented in its
	// markdown). Both profiles set them: they abort+retry a stalled request and
	// don't depend on bandwidth. Upstream defaults are 1000 ms / disabled(0).
	StreamerS3RequestTimeoutMS = "3000"    // per-request timeout
	StreamerS3LowSpeedLimit    = "1048576" // 1 MiB/s floor before abort+retry
)

// StreamerConcurrency resolves the Run:ai streamer read concurrency for a run:
// an explicit operator value always wins; else, on a high-bandwidth instance with
// a known model size, AWS's size-derived ceil(model_gb / 4gb) (capped); else the
// standard S3-benchmarked default (32). Exported so the orchestrator/export can
// compute the same value the template's inline extra-config uses.
func StreamerConcurrency(explicit int, modelSizeBytes int64, instanceTypeName string) int {
	if explicit > 0 {
		return explicit
	}
	if IsHighBandwidthInstance(instanceTypeName) && modelSizeBytes > 0 {
		c := int((modelSizeBytes + streamerChunkBytes - 1) / streamerChunkBytes) // ceil
		if c < 1 {
			c = 1
		}
		if c > streamerMaxConcurrency {
			c = streamerMaxConcurrency
		}
		return c
	}
	return streamerDefaultConcurrency
}

// StreamerChunkBytesize returns the RUNAI_STREAMER_CHUNK_BYTESIZE env value for a
// run, or "" to leave it unset (use the streamer's own object-storage default).
// High-bandwidth instances get AWS's 4 GiB chunk; standard instances inherit the
// 8 MiB default. Exported so the manifest/export set the env identically.
func StreamerChunkBytesize(instanceTypeName string) string {
	if IsHighBandwidthInstance(instanceTypeName) {
		return fmt.Sprintf("%d", streamerChunkBytes)
	}
	return ""
}

// StreamerExtraConfig is the exported form of streamerExtraConfig, so the export
// renderer (internal/api) builds the SAME --model-loader-extra-config JSON the
// orchestrator's BuildArgs produces — one source, no deploy/export drift.
func StreamerExtraConfig(p ContainerParams) string { return streamerExtraConfig(p) }

// streamerExtraConfig builds the --model-loader-extra-config JSON for a Run:ai
// streamed load. Keys, in fixed order for stable/testable output:
//   - concurrency (always): explicit / AWS size-derived / standard default,
//     resolved by StreamerConcurrency. Maps to RUNAI_STREAMER_CONCURRENCY.
//   - distributed:true (only when TensorParallelDegree > 1 AND not CPU): the
//     multiple vLLM rank processes reading the same file list divide the work and
//     broadcast their shard over torch-distributed instead of each reading the
//     full files — a no-op (omitted) at TP=1. Requires vLLM V1 + a torch-distributed
//     group, which is CUDA/ROCm-only, so it is suppressed on cpu runs where the
//     "TP" ranks are NUMA nodes with no NCCL group (PRD-67 §5b).
//   - memory_limit BYTES (only when StreamerMemoryLimitGiB > 0): caps the CPU
//     staging buffer (RUNAI_STREAMER_MEMORY_LIMIT); omitted ⇒ upstream default.
func streamerExtraConfig(p ContainerParams) string {
	parts := []string{fmt.Sprintf(`"concurrency":%d`, StreamerConcurrency(p.StreamerConcurrency, p.ModelSizeBytes, p.InstanceTypeName))}
	if p.TensorParallelDegree > 1 && p.Accelerator != "cpu" {
		parts = append(parts, `"distributed":true`)
	}
	if p.StreamerMemoryLimitGiB > 0 {
		parts = append(parts, fmt.Sprintf(`"memory_limit":%d`, int64(p.StreamerMemoryLimitGiB)*1024*1024*1024))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
