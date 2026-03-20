// Package recommend implements a deterministic benchmark configuration
// recommender based on model architecture metadata and instance type specs.
package recommend

import (
	"fmt"
	"math"
	"strings"
)

// ModelConfig holds architecture metadata fetched from HuggingFace.
type ModelConfig struct {
	ParameterCount        int64   `json:"parameter_count"`
	HiddenSize            int     `json:"hidden_size"`
	NumAttentionHeads     int     `json:"num_attention_heads"`
	NumKeyValueHeads      int     `json:"num_key_value_heads"`
	NumHiddenLayers       int     `json:"num_hidden_layers"`
	MaxPositionEmbeddings int     `json:"max_position_embeddings"`
	TorchDtype            string  `json:"torch_dtype"`
	ModelType             string  `json:"model_type"`
	Architecture          string  `json:"architecture"`
}

// InstanceSpec holds GPU specs from the instance_types DB table.
type InstanceSpec struct {
	Name                 string `json:"name"`
	AcceleratorType      string `json:"accelerator_type"`
	AcceleratorName      string `json:"accelerator_name"`
	AcceleratorCount     int    `json:"accelerator_count"`
	AcceleratorMemoryGiB int    `json:"accelerator_memory_gib"`
	MemoryGiB            int    `json:"memory_gib"` // host memory
}

// Recommendation holds the recommended configuration values.
type Recommendation struct {
	TensorParallelDegree int     `json:"tensor_parallel_degree"`
	Quantization         *string `json:"quantization"`
	MaxModelLen          int     `json:"max_model_len"`
	Concurrency          int     `json:"concurrency"`
	InputSequenceLength  int     `json:"input_sequence_length"`
	OutputSequenceLength int     `json:"output_sequence_length"`

	Explanation Explanation `json:"explanation"`
	ModelInfo   ModelInfo   `json:"model_info"`
	InstanceInfo InstanceInfo `json:"instance_info"`

	// Alternatives is non-nil when the model doesn't fit at native precision.
	Alternatives *Alternatives `json:"alternatives,omitempty"`
}

// Explanation provides human-readable reasoning for each recommendation.
type Explanation struct {
	TensorParallelDegree string `json:"tensor_parallel_degree"`
	Quantization         string `json:"quantization"`
	MaxModelLen          string `json:"max_model_len"`
	Concurrency          string `json:"concurrency"`
	Feasible             bool   `json:"feasible"`
	Reason               string `json:"reason,omitempty"`
	SuggestedInstance    string `json:"suggested_instance,omitempty"`
}

// ModelInfo summarizes the model metadata in the response.
type ModelInfo struct {
	ParameterCount        int64  `json:"parameter_count"`
	NativeDtype           string `json:"native_dtype"`
	MaxPositionEmbeddings int    `json:"max_position_embeddings"`
	Architecture          string `json:"architecture"`
}

// InstanceInfo summarizes the instance specs in the response.
type InstanceInfo struct {
	AcceleratorCount     int    `json:"accelerator_count"`
	AcceleratorMemoryGiB int    `json:"accelerator_memory_gib"`
	AcceleratorName      string `json:"accelerator_name"`
}

// Alternatives presents options when the model doesn't fit at native precision.
type Alternatives struct {
	QuantizationOption *QuantizationOption `json:"quantization_option,omitempty"`
	LargerInstance     string              `json:"larger_instance,omitempty"`
}

// QuantizationOption describes a quantization configuration that makes the model fit.
type QuantizationOption struct {
	Quantization    string  `json:"quantization"`
	EstimatedMemGiB float64 `json:"estimated_mem_gib"`
}

const (
	overheadFraction = 0.10 // 10% reserved for CUDA context, activations, etc.
	gibBytes         = 1024 * 1024 * 1024
	// kvCacheSafetyFactor accounts for vLLM's additional memory consumers beyond
	// weights and explicit overhead: CUDA graphs, block tables, output logits
	// buffers, speculative decoding, chunked prefill, etc. Real-world testing
	// shows vLLM uses significantly more memory than theoretical calculations
	// predict. A factor of 0.15 means only 15% of theoretical KV cache space
	// is assumed usable, which aligns with empirical observations.
	kvCacheSafetyFactor = 0.15
)

// bytesPerParam returns the bytes per parameter for a given dtype/quantization.
func bytesPerParam(quant string) float64 {
	switch quant {
	case "fp32":
		return 4
	case "", "fp16", "bfloat16":
		return 2
	case "fp8", "int8":
		return 1
	case "int4":
		return 0.5
	default:
		return 2 // assume FP16
	}
}

// supportsFP8 returns true if the GPU supports FP8 quantization.
func supportsFP8(acceleratorName string) bool {
	switch acceleratorName {
	case "H100", "H200", "L40S":
		return true
	}
	return false
}

// modelMemoryBytes returns the model weight memory in bytes for a given quantization.
func modelMemoryBytes(params int64, quant string) float64 {
	return float64(params) * bytesPerParam(quant)
}

// kvCachePerTokenBytes returns KV cache memory per token in bytes.
// Formula: 2 (K+V) × num_layers × num_kv_heads × head_dim × 2 (FP16 bytes)
func kvCachePerTokenBytes(cfg ModelConfig) float64 {
	headDim := float64(cfg.HiddenSize) / float64(cfg.NumAttentionHeads)
	return 2 * float64(cfg.NumHiddenLayers) * float64(cfg.NumKeyValueHeads) * headDim * 2
}

// inferenceOverheadBytes estimates non-weight, non-KV GPU memory used by vLLM.
// This covers the CUDA context/runtime (~0.5 GiB), vLLM worker and tokenizer
// (~0.2 GiB), CUDA graph captures (~1-2 GiB), and activation memory during
// inference (~1-3 GiB depending on batch size and model architecture).
// vLLM's block-based KV cache also has per-block overhead.
// Empirically, vLLM uses 3-5 GiB overhead on 7B-8B models with default settings.
func inferenceOverheadBytes(cfg ModelConfig) float64 {
	// Base overhead for CUDA context, vLLM runtime, tokenizer
	baseOverhead := 1.0 * gibBytes
	// Activation memory scales with hidden_size and batch
	activationOverhead := float64(cfg.HiddenSize) * 4096 * 4 // rough estimate
	if activationOverhead < 1.5*gibBytes {
		activationOverhead = 1.5 * gibBytes
	}
	if activationOverhead > 3*gibBytes {
		activationOverhead = 3 * gibBytes
	}
	// CUDA graph captures
	graphOverhead := 1.5 * gibBytes
	return baseOverhead + activationOverhead + graphOverhead
}

// nativeDtype returns the native dtype string, defaulting to "bfloat16".
func nativeDtype(cfg ModelConfig) string {
	if cfg.TorchDtype != "" {
		return cfg.TorchDtype
	}
	return "bfloat16"
}

// validTPDegree finds the smallest TP ≥ minTP that evenly divides both
// num_attention_heads and num_key_value_heads, and is ≤ maxGPUs.
func validTPDegree(minTP, numHeads, numKVHeads, maxGPUs int) int {
	for tp := minTP; tp <= maxGPUs; tp++ {
		if numHeads%tp == 0 && numKVHeads%tp == 0 {
			return tp
		}
	}
	// Fallback: return maxGPUs even if it doesn't divide evenly.
	return maxGPUs
}

// maxValidTPDegree returns the largest valid TP degree that fits the model and
// divides attention heads evenly. This maximizes GPU utilization.
func maxValidTPDegree(minTP, numHeads, numKVHeads, maxGPUs int) int {
	best := minTP
	for tp := minTP; tp <= maxGPUs; tp++ {
		if numHeads%tp == 0 && numKVHeads%tp == 0 {
			best = tp
		}
	}
	return best
}

// ValidTPOptions returns all valid TP degrees for the given model and instance.
// Used by the UI to populate a dropdown for user override.
func ValidTPOptions(numHeads, numKVHeads, maxGPUs int) []int {
	var options []int
	for tp := 1; tp <= maxGPUs; tp++ {
		if numHeads%tp == 0 && numKVHeads%tp == 0 {
			options = append(options, tp)
		}
	}
	if len(options) == 0 {
		options = []int{1}
	}
	return options
}

// roundDownContext rounds a token count down to the nearest common context length.
func roundDownContext(tokens int) int {
	common := []int{131072, 65536, 32768, 16384, 8192, 4096, 2048, 1024, 512}
	for _, c := range common {
		if tokens >= c {
			return c
		}
	}
	return 512
}

// Recommend computes configuration recommendations given model and instance specs.
// allInstances is used to suggest a larger instance when the model doesn't fit.
// tpOverride, if > 0, forces a specific tensor parallel degree instead of the default.
func Recommend(cfg ModelConfig, inst InstanceSpec, allInstances []InstanceSpec, tpOverride int) *Recommendation {
	dtype := nativeDtype(cfg)
	perDeviceGiB := float64(inst.AcceleratorMemoryGiB) / float64(inst.AcceleratorCount)
	perDeviceBytes := perDeviceGiB * gibBytes
	usablePerDevice := perDeviceBytes * (1 - overheadFraction)

	modelMemNative := modelMemoryBytes(cfg.ParameterCount, dtype)
	minGPUs := int(math.Ceil(modelMemNative / usablePerDevice))
	if minGPUs < 1 {
		minGPUs = 1
	}

	rec := &Recommendation{
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		ModelInfo: ModelInfo{
			ParameterCount:        cfg.ParameterCount,
			NativeDtype:           dtype,
			MaxPositionEmbeddings: cfg.MaxPositionEmbeddings,
			Architecture:          cfg.ModelType,
		},
		InstanceInfo: InstanceInfo{
			AcceleratorCount:     inst.AcceleratorCount,
			AcceleratorMemoryGiB: inst.AcceleratorMemoryGiB,
			AcceleratorName:      inst.AcceleratorName,
		},
	}

	// Determine quantization and TP.
	var chosenQuant string // "" means native precision
	totalUsableBytes := usablePerDevice * float64(inst.AcceleratorCount)

	if modelMemNative <= totalUsableBytes {
		// Fits at native precision.
		// Default to max valid TP to use all GPUs; allow user override.
		tp := maxValidTPDegree(minGPUs, cfg.NumAttentionHeads, cfg.NumKeyValueHeads, inst.AcceleratorCount)
		if tpOverride > 0 && tpOverride >= minGPUs && tpOverride <= inst.AcceleratorCount {
			// Validate override divides attention heads
			if cfg.NumAttentionHeads%tpOverride == 0 && cfg.NumKeyValueHeads%tpOverride == 0 {
				tp = tpOverride
			}
		}
		rec.TensorParallelDegree = tp
		rec.Quantization = nil
		chosenQuant = dtype
		rec.Explanation.Quantization = fmt.Sprintf("Model fits in native %s precision (%.1f GiB weights, %.0f GiB available).",
			dtype, modelMemNative/gibBytes, totalUsableBytes/gibBytes)
		rec.Explanation.TensorParallelDegree = fmt.Sprintf("TP=%d: model requires %.1f GiB, each %s has %.0f GiB.",
			tp, modelMemNative/gibBytes, inst.AcceleratorName, perDeviceGiB)
	} else {
		// Doesn't fit at native precision — try quantization options.
		rec.Alternatives = &Alternatives{}

		// Try quantization levels in order of preference.
		quantOptions := []struct {
			name string
			ok   bool
		}{
			{"fp8", supportsFP8(inst.AcceleratorName)},
			{"int8", true},
			{"int4", true},
		}

		var fitsWithQuant bool
		for _, qo := range quantOptions {
			if !qo.ok {
				continue
			}
			qMem := modelMemoryBytes(cfg.ParameterCount, qo.name)
			if qMem <= totalUsableBytes {
				chosenQuant = qo.name
				fitsWithQuant = true
				rec.Alternatives.QuantizationOption = &QuantizationOption{
					Quantization:    qo.name,
					EstimatedMemGiB: qMem / gibBytes,
				}
				break
			}
		}

		// Find a larger instance that fits at native precision.
		if len(allInstances) > 0 {
			for _, alt := range allInstances {
				if !strings.EqualFold(alt.AcceleratorType, "gpu") {
					continue
				}
				altTotal := float64(alt.AcceleratorMemoryGiB) * gibBytes * (1 - overheadFraction)
				if modelMemNative <= altTotal && alt.AcceleratorMemoryGiB > inst.AcceleratorMemoryGiB {
					rec.Alternatives.LargerInstance = alt.Name
					break
				}
			}
		}

		if fitsWithQuant {
			q := chosenQuant
			rec.Quantization = &q
			qMem := modelMemoryBytes(cfg.ParameterCount, chosenQuant)
			minGPUsQ := int(math.Ceil(qMem / usablePerDevice))
			if minGPUsQ < 1 {
				minGPUsQ = 1
			}
			tp := validTPDegree(minGPUsQ, cfg.NumAttentionHeads, cfg.NumKeyValueHeads, inst.AcceleratorCount)
			rec.TensorParallelDegree = tp
			rec.Explanation.Quantization = fmt.Sprintf("Model requires %.1f GiB in %s but only %.0f GiB available. Using %s quantization (%.1f GiB).",
				modelMemNative/gibBytes, dtype, totalUsableBytes/gibBytes, chosenQuant, qMem/gibBytes)
			rec.Explanation.TensorParallelDegree = fmt.Sprintf("TP=%d with %s quantization: %.1f GiB model across %d × %s.",
				tp, chosenQuant, qMem/gibBytes, inst.AcceleratorCount, inst.AcceleratorName)
		} else {
			// Nothing fits — infeasible on this instance.
			rec.Explanation.Feasible = false
			rec.Explanation.Reason = fmt.Sprintf("Model requires %.1f GiB in %s. Even INT4 (%.1f GiB) exceeds %.0f GiB available on %s.",
				modelMemNative/gibBytes, dtype, modelMemoryBytes(cfg.ParameterCount, "int4")/gibBytes,
				totalUsableBytes/gibBytes, inst.Name)
			if rec.Alternatives.LargerInstance != "" {
				rec.Explanation.SuggestedInstance = rec.Alternatives.LargerInstance
			}
			return rec
		}
	}

	rec.Explanation.Feasible = true

	// Calculate max model length.
	// Beyond raw weights, vLLM consumes GPU memory for:
	//   1. CUDA context + runtime: ~0.5 GiB (fixed)
	//   2. CUDA graph captures: vLLM pre-captures ~35 graphs for different
	//      batch sizes (sum ≈ 3400 tokens). Each graph allocates per-layer
	//      activation buffers proportional to hidden_size and FFN width.
	//      FFN intermediate_size ≈ 3.5 × hidden_size for gated architectures.
	kvPerToken := kvCachePerTokenBytes(cfg)
	effectiveModelMem := modelMemoryBytes(cfg.ParameterCount, chosenQuant)
	perGPUOverhead := inferenceOverheadBytes(cfg)
	// Use memory from the GPUs actually being used (TP), not total instance memory.
	// With TP=1 on a 4-GPU instance, only 1 GPU's memory is available for KV cache.
	// Each GPU has its own CUDA context, graphs, and activation buffers, so overhead scales with TP.
	tpUsableBytes := usablePerDevice * float64(rec.TensorParallelDegree)
	totalOverhead := perGPUOverhead * float64(rec.TensorParallelDegree)
	theoreticalRemaining := tpUsableBytes - effectiveModelMem - totalOverhead
	// Apply safety factor to account for vLLM's hidden memory consumers.
	remainingBytes := theoreticalRemaining * kvCacheSafetyFactor
	if remainingBytes < 0 {
		remainingBytes = 0
	}

	maxTokensKV := int(remainingBytes / kvPerToken)
	maxModelLen := cfg.MaxPositionEmbeddings
	if maxTokensKV < maxModelLen {
		maxModelLen = maxTokensKV
	}
	maxModelLen = roundDownContext(maxModelLen)
	rec.MaxModelLen = maxModelLen
	rec.Explanation.MaxModelLen = fmt.Sprintf("%.1f GiB available for KV cache (TP=%d × %.0f GiB per GPU). Supports up to %d tokens.",
		remainingBytes/gibBytes, rec.TensorParallelDegree, perDeviceGiB, maxModelLen)

	// Adjust input/output if model context is too small.
	if maxModelLen < rec.InputSequenceLength+rec.OutputSequenceLength {
		rec.InputSequenceLength = maxModelLen * 2 / 3
		rec.OutputSequenceLength = maxModelLen / 3
	}

	// Calculate concurrency.
	avgSeqLen := float64(rec.InputSequenceLength + rec.OutputSequenceLength)
	memPerSeq := kvPerToken * avgSeqLen
	if memPerSeq > 0 {
		maxConcurrent := int(remainingBytes / memPerSeq)
		if maxConcurrent > 64 {
			maxConcurrent = 64
		}
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		rec.Concurrency = maxConcurrent
	} else {
		rec.Concurrency = 1
	}
	rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory with %d-token average sequence length.",
		remainingBytes/gibBytes, int(avgSeqLen))

	return rec
}
