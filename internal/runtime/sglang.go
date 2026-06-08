package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SGLang implements Runtime for the "sglang" framework on GPU instances.
type SGLang struct{}

func (s *SGLang) Name() string          { return "sglang" }
func (s *SGLang) ContainerName() string  { return "sglang" }
func (s *SGLang) SupportedAccelerators() []string { return []string{"gpu"} }

func (s *SGLang) ResolveImageOverride() string {
	return os.Getenv("SGLANG_IMAGE")
}

func (s *SGLang) DefaultImage(version, pullThroughRegistry string) string {
	return fmt.Sprintf("lmsysorg/sglang:%s", version)
}

func (s *SGLang) ResolveVersion(tv ToolVersions) string {
	return tv.SGLangVersion
}

func (s *SGLang) BuildArgs(p ContainerParams) (command []string, args []string) {
	command = []string{"python3"}
	args = append(args, "-m", "sglang.launch_server")

	// SGLang's S3 connector (--load-format remote) requires boto3 which
	// is not bundled in the upstream lmsysorg/sglang image. Always use HF
	// download path for now. When a custom image with boto3 is available,
	// this can be extended to support S3 loading.
	args = append(args, "--model-path", p.ModelHfID)

	args = append(args, "--host", "0.0.0.0")
	args = append(args, "--port", "8000")
	args = append(args, "--tp-size", strconv.Itoa(p.TensorParallelDegree))
	args = append(args, "--trust-remote-code")
	args = append(args, "--enable-metrics")
	// --enable-mixed-chunk fuses prefill and decode in a single batch so
	// large prefills don't stall in-flight decodes. Without it, SGLang's
	// default scheduler causes severe TTFT tail latency under continuous
	// streaming load (measured p90 7.3s vs 54ms with the flag on a 1.5B
	// model at 5 req/s). vLLM enables equivalent chunked-prefill behavior
	// by default; this brings SGLang to parity for latency-sensitive
	// workloads. See the SGLang tuning investigation (PRD-XX).
	args = append(args, "--enable-mixed-chunk")
	// On non-Hopper GPUs (Ampere/Ada — A10G, L4, L40S, A100), SGLang's
	// FlashAttention3 default doesn't apply (FA3 is Hopper-only), so it
	// auto-selects FlashInfer. FlashInfer's prefill path stalls in-flight
	// decode under heavy load, spiking TTFT (measured p90 6.3s on
	// Llama-3.2-3B at 5 req/s with 256-token outputs). Forcing the Triton
	// backend — SGLang's documented FlashInfer fallback — fixes it (p90
	// dropped to 113ms, same throughput). Hopper GPUs keep the FA3 default.
	if isNonHopperGPU(p.AcceleratorName) {
		args = append(args, "--attention-backend", "triton")
	}

	args = append(args, s.MapQuantization(p.Quantization, p.UseRunaiStreamer)...)

	if p.MaxModelLen > 0 {
		args = append(args, "--context-length", strconv.Itoa(p.MaxModelLen))
	}
	if p.MaxNumSeqs > 0 {
		args = append(args, "--max-running-requests", strconv.Itoa(p.MaxNumSeqs))
	}
	if p.ChunkedPrefillSize > 0 {
		args = append(args, "--chunked-prefill-size", strconv.Itoa(p.ChunkedPrefillSize))
	}
	if p.MemFractionStatic > 0 {
		args = append(args, "--mem-fraction-static", fmt.Sprintf("%.2f", p.MemFractionStatic))
	}

	// Experimental escape hatch: SGLANG_EXTRA_ARGS appends space-separated
	// flags to the launch command, letting operators A/B-test tuning flags
	// (e.g. --enable-mixed-chunk, --schedule-conservativeness) without a
	// rebuild. Empty when unset.
	if extra := strings.Fields(os.Getenv("SGLANG_EXTRA_ARGS")); len(extra) > 0 {
		args = append(args, extra...)
	}

	return command, args
}


// isNonHopperGPU reports whether the accelerator is a non-Hopper NVIDIA GPU
// (Ampere/Ada). SGLang's FlashAttention3 backend requires Hopper (H100/H200);
// on everything else SGLang falls back to FlashInfer, which we override with
// Triton for latency-sensitive serving. Hopper GPUs (and an empty/unknown
// name) keep SGLang's own default backend selection.
func isNonHopperGPU(acceleratorName string) bool {
	switch acceleratorName {
	case "A10G", "A10", "L4", "L40S", "L40", "A100":
		return true
	default:
		// H100, H200, B200, unknown, or empty: leave SGLang's default.
		return false
	}
}

func (s *SGLang) MapQuantization(quant string, useRunaiStreamer bool) []string {
	switch quant {
	case "fp8":
		return []string{"--quantization", "fp8"}
	case "int8":
		return []string{"--quantization", "w8a8_int8"}
	case "int4":
		return []string{"--quantization", "awq"}
	default:
		return nil
	}
}
