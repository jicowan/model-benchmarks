package recommend

import (
	"fmt"
	"math"
	"strings"
)

// CPU (ARM/Graviton) recommender — PRD-67 §5. A SEPARATE path from the GPU
// recommender because the memory model genuinely differs: CPU has no VRAM and
// no gpu_memory_utilization; host DRAM *is* the serving memory, and the KV cache
// is a fixed absolute allocation (VLLM_CPU_KVCACHE_SPACE), not "whatever fraction
// of VRAM is left." So we reuse the HOST-memory map (hostAllocatableFrac) rather
// than CalculateMemoryBreakdown (the VRAM map), and recompose the fit as:
//
//	weights(quantized) + kv_cache_space + activation_overhead
//	    <= node_RAM * hostAllocatableFrac(node_RAM)
//
// CPU is memory-bandwidth bound, so fit-success != good throughput; the
// recommendation carries a loud bandwidth advisory and steers to quantized
// (W8A8/W4A8) variants.
const (
	// cpuKVCacheSpaceGiB is the default VLLM_CPU_KVCACHE_SPACE (absolute GiB, not
	// a fraction). Sized as a fraction of node RAM, clamped. This mirrors the env
	// the manifest sets; the recommender must budget for it in the fit.
	cpuKVCacheFracOfHost = 0.35 // fraction of allocatable host RAM given to KV cache
	cpuKVCacheMaxGiB     = 128  // absolute cap so a huge box doesn't over-reserve
	cpuKVCacheMinGiB     = 4    // vLLM CPU floor (docs)

	// cpuUnquantizedCeilingParams is the conservative hard ceiling for DENSE
	// UNQUANTIZED models on CPU (§11c). Above this, refuse and steer to W8A8/W4A8.
	// ~13B params (start conservative; refine from live runs).
	cpuUnquantizedCeilingParams = 13_000_000_000
	// cpuUnquantizedWarnParams is the soft warning threshold (8B); 8-13B dense
	// bf16 runs but throughput is poor.
	cpuUnquantizedWarnParams = 8_000_000_000
)

// gravitonGeneration maps an instance family prefix to the Graviton generation
// label for messaging. Mirrors the seeded accelerator_name.
func gravitonGeneration(instanceName string) string {
	// family is the substring before the first '.', e.g. "r8g" from "r8g.16xlarge".
	fam := instanceName
	if i := strings.IndexByte(instanceName, '.'); i > 0 {
		fam = instanceName[:i]
	}
	if len(fam) < 3 {
		return "Graviton"
	}
	switch fam[len(fam)-2:] { // last two chars: "8g", "7g", "9g"
	case "9g":
		return "Graviton5"
	case "8g":
		return "Graviton4"
	case "7g":
		return "Graviton3"
	default:
		return "Graviton"
	}
}

// cpuNumaNodes returns the NUMA-node count for a CPU instance. AWS does not
// publish this and Graviton is single-socket, so we conservatively treat every
// seeded size as ONE NUMA node → TP=1. Intra-node parallelism comes from
// VLLM_CPU_OMP_THREADS_BIND (thread binding), not TP ranks. If a multi-socket
// Graviton ever appears, this is the one place to teach the real topology.
func cpuNumaNodes(inst InstanceSpec) int {
	return 1
}

// cpuKVCacheSpaceGiB resolves the VLLM_CPU_KVCACHE_SPACE value (absolute GiB) for
// a host of the given size. Exported form used by the manifest env sizing too.
func cpuKVCacheSpaceGiB(hostGiB int) int {
	usable := float64(hostGiB) * hostAllocatableFrac(hostGiB)
	kv := int(usable * cpuKVCacheFracOfHost)
	if kv > cpuKVCacheMaxGiB {
		kv = cpuKVCacheMaxGiB
	}
	if kv < cpuKVCacheMinGiB {
		kv = cpuKVCacheMinGiB
	}
	return kv
}

// CPUKVCacheSpaceGiB is the exported resolver so the manifest/orchestrator set the
// VLLM_CPU_KVCACHE_SPACE env to the same value the recommender budgeted for.
func CPUKVCacheSpaceGiB(hostGiB int) int { return cpuKVCacheSpaceGiB(hostGiB) }

// RecommendCPU computes a configuration recommendation for an ARM/Graviton CPU
// instance. Fit is against host DRAM; TP is the NUMA-node count (≈1 on
// single-socket Graviton); quantization is steered to W8A8/W4A8 and dense
// unquantized models above a ceiling are refused (§11c). float16 is not offered
// (unstable on torch CPU) — dtype is always bfloat16.
func RecommendCPU(cfg ModelConfig, inst InstanceSpec) *Recommendation {
	generation := gravitonGeneration(inst.Name)
	hostGiB := inst.MemoryGiB
	usableHostBytes := float64(hostGiB) * hostAllocatableFrac(hostGiB) * gibBytes

	rec := &Recommendation{
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		ModelInfo: ModelInfo{
			ParameterCount:        cfg.ParameterCount,
			NativeDtype:           "bfloat16", // float16 unstable on torch CPU
			MaxPositionEmbeddings: cfg.MaxPositionEmbeddings,
			Architecture:          cfg.ModelType,
			SlidingWindow:         cfg.SlidingWindow,
		},
		InstanceInfo: InstanceInfo{
			AcceleratorCount:     0, // no discrete accelerator
			AcceleratorMemoryGiB: 0,
			AcceleratorName:      generation,
		},
	}

	// Reject embedding / diffusion / classifier models — same reason as the
	// GPU/Neuron paths: inference-perf only drives completions/chat.
	if reason := isUnsupportedModelKind(cfg); reason != "" {
		rec.Explanation.Feasible = false
		rec.Explanation.Reason = reason
		return rec
	}

	// Decide the effective quantization. A pre-quantized checkpoint keeps its
	// format; otherwise the run's own quantization knob (mapped elsewhere) drives
	// the weight footprint. We compute the weight bytes at the effective precision.
	quant := ""
	if cfg.PreQuantized {
		if cfg.PreQuantBits == 4 {
			quant = "int4"
		} else if cfg.PreQuantBits == 8 {
			quant = "int8"
		}
	}
	weightBytes := modelMemoryBytes(cfg.ParameterCount, quant)
	if cfg.ActualMemoryBytes > 0 {
		weightBytes = float64(cfg.ActualMemoryBytes)
	}

	// §11c: refuse DENSE UNQUANTIZED models above the ceiling — steer to W8A8/W4A8.
	// (BF16 small models are allowed; the guard is about *oversized* unquantized.)
	if quant == "" && cfg.ParameterCount > cpuUnquantizedCeilingParams {
		rec.Explanation.Feasible = false
		rec.Explanation.Reason = fmt.Sprintf(
			"%.0fB-parameter model is too large to run UNQUANTIZED on CPU (ceiling ~%dB). "+
				"Use an INT8 W8A8 or INT4 W4A8 quantized checkpoint — CPU inference is "+
				"memory-bandwidth bound and quantization is what makes mid-size models viable.",
			float64(cfg.ParameterCount)/1e9, cpuUnquantizedCeilingParams/1_000_000_000)
		rec.Alternatives = &Alternatives{
			QuantizationOption: &QuantizationOption{
				Quantization:         "int8",
				EstimatedMemGiB:      modelMemoryBytes(cfg.ParameterCount, "int8") / gibBytes,
				RequiresPreQuantized: true,
			},
		}
		return rec
	}

	// KV cache is a FIXED absolute allocation on CPU (VLLM_CPU_KVCACHE_SPACE),
	// not a fraction of leftover VRAM. Budget it explicitly.
	kvSpaceGiB := cpuKVCacheSpaceGiB(hostGiB)
	kvSpaceBytes := float64(kvSpaceGiB) * gibBytes

	// Activation / framework overhead. Reuse the shared estimate but strip the
	// CUDA-specific terms conceptually — inferenceOverheadBytes bundles a fixed
	// context + activation; on CPU there's no CUDA context but PyTorch activation
	// + allocator overhead is comparable, so it's a reasonable conservative proxy.
	activationBytes := inferenceOverheadBytes(cfg)

	need := weightBytes + kvSpaceBytes + activationBytes
	if need > usableHostBytes {
		rec.Explanation.Feasible = false
		rec.Explanation.Reason = fmt.Sprintf(
			"Model needs ~%.1f GiB (weights %.1f + KV %.0f + overhead %.1f) but %s has only "+
				"~%.0f GiB allocatable host RAM. Use a larger-memory Graviton (e.g. r8g.48xlarge, ~1.5 TiB) "+
				"or a more aggressively quantized checkpoint.",
			need/gibBytes, weightBytes/gibBytes, kvSpaceBytes/gibBytes, activationBytes/gibBytes,
			inst.Name, usableHostBytes/gibBytes)
		return rec
	}

	// TP = NUMA-node count (≈1 on single-socket Graviton). NOT AcceleratorCount (0).
	tp := cpuNumaNodes(inst)
	rec.TensorParallelDegree = tp
	rec.Explanation.TensorParallelDegree = fmt.Sprintf(
		"TP=%d (NUMA nodes). Graviton is single-socket, so intra-node parallelism comes from "+
			"OpenMP thread binding (VLLM_CPU_OMP_THREADS_BIND), not tensor-parallel ranks.", tp)

	// Quantization guidance.
	if quant != "" {
		q := quant
		rec.Quantization = &q
		rec.Explanation.Quantization = fmt.Sprintf(
			"Pre-quantized %s checkpoint — ideal for CPU (Arm W8A8/W4A8 / KleidiAI kernels).", quant)
	} else {
		rec.Explanation.Quantization = "Unquantized (bfloat16). Runs, but a W8A8/W4A8 checkpoint is markedly faster on Arm."
	}

	// Max model length from the KV-cache-space budget (fp16 KV; CPU has no fp8 KV).
	kvPerToken := kvCachePerTokenBytes(cfg, "")
	maxTokensKV := int(kvSpaceBytes / kvPerToken)
	maxModelLen := cfg.MaxPositionEmbeddings
	if maxTokensKV < maxModelLen {
		maxModelLen = maxTokensKV
	}
	maxModelLen = roundDownContext(maxModelLen)
	if maxModelLen < 1 {
		maxModelLen = 512
	}
	rec.MaxModelLen = maxModelLen
	rec.Explanation.MaxModelLen = fmt.Sprintf(
		"VLLM_CPU_KVCACHE_SPACE=%d GiB budgets up to ~%d tokens of KV cache.", kvSpaceGiB, maxModelLen)

	// Adjust I/O if the context is tight.
	if maxModelLen < rec.InputSequenceLength+rec.OutputSequenceLength {
		rec.InputSequenceLength = maxModelLen * 2 / 3
		rec.OutputSequenceLength = maxModelLen / 3
	}

	// Concurrency from KV budget.
	avgSeqLen := float64(rec.InputSequenceLength + rec.OutputSequenceLength)
	memPerSeq := kvPerToken * avgSeqLen
	conc := 1
	if memPerSeq > 0 {
		conc = int(kvSpaceBytes / memPerSeq)
		if conc > 64 {
			conc = 64
		}
		if conc < 1 {
			conc = 1
		}
	}
	rec.Concurrency = conc
	rec.Explanation.Concurrency = fmt.Sprintf(
		"Based on the %d GiB KV cache budget with a %d-token average sequence.", kvSpaceGiB, int(avgSeqLen))

	// Loud memory-bandwidth advisory — fit-success != good throughput on CPU.
	rec.Warnings = append(rec.Warnings,
		"CPU inference is memory-bandwidth bound: expect far lower throughput than GPU. "+
			"This tier is for cost/availability benchmarking of small/quantized models, not peak performance.")
	if quant == "" && cfg.ParameterCount >= cpuUnquantizedWarnParams {
		rec.Warnings = append(rec.Warnings, fmt.Sprintf(
			"%.0fB unquantized on CPU will be slow — prefer an INT8 W8A8 / INT4 W4A8 checkpoint.",
			float64(cfg.ParameterCount)/1e9))
	}

	rec.OverheadGiB = math.Round(activationBytes/gibBytes*10) / 10
	rec.Explanation.Feasible = true
	return rec
}
