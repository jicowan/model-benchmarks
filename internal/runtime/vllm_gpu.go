package runtime

import (
	"fmt"
	"os"
	"strconv"
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
		cfg := fmt.Sprintf(`{"concurrency":%d}`, streamerConcurrencyOrDefault(p.StreamerConcurrency))
		args = append(args, cfg)
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

func streamerConcurrencyOrDefault(c int) int {
	if c > 0 {
		return c
	}
	return 16
}
