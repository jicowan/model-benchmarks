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
	VocabSize             int     `json:"vocab_size"`
	IntermediateSize      int     `json:"intermediate_size"` // FFN hidden dim
	TorchDtype            string  `json:"torch_dtype"`
	ModelType             string  `json:"model_type"`
	Architecture          string  `json:"architecture"`
	SlidingWindow         int     `json:"sliding_window"` // 0 = no sliding window (full attention)

	// Pre-quantization info (from quantization_config in HuggingFace config.json)
	// If set, the model is already quantized and we should not apply additional quantization.
	PreQuantized   bool   `json:"pre_quantized"`    // true if model has quantization_config
	PreQuantMethod string `json:"pre_quant_method"` // "gptq", "awq", "modelopt", "bitsandbytes"
	PreQuantBits   int    `json:"pre_quant_bits"`   // 4 or 8

	// Actual model memory calculated from safetensors dtype breakdown.
	// More accurate than ParameterCount * bytesPerDtype for mixed-precision models.
	// If 0, fall back to calculating from ParameterCount.
	ActualMemoryBytes int64 `json:"actual_memory_bytes"`

	// TransformersVersion required by this model (from config.json).
	// Used to detect models that require a newer transformers version than vLLM bundles.
	TransformersVersion string `json:"transformers_version,omitempty"`
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
	OverheadGiB          float64 `json:"overhead_gib"` // Runtime overhead used (for display/tuning)

	Explanation  Explanation  `json:"explanation"`
	ModelInfo    ModelInfo    `json:"model_info"`
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
	ProductionNote       string `json:"production_note,omitempty"`
}

// ModelInfo summarizes the model metadata in the response.
type ModelInfo struct {
	ParameterCount        int64  `json:"parameter_count"`
	NativeDtype           string `json:"native_dtype"`
	MaxPositionEmbeddings int    `json:"max_position_embeddings"`
	Architecture          string `json:"architecture"`
	SlidingWindow         int    `json:"sliding_window,omitempty"` // 0 = full attention
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
	Quantization        string  `json:"quantization"`
	EstimatedMemGiB     float64 `json:"estimated_mem_gib"`
	RequiresPreQuantized bool   `json:"requires_pre_quantized"` // True if a pre-quantized model (GPTQ/AWQ) is needed
}

const (
	// gpuMemoryUtilization matches vLLM's default (0.90).
	// vLLM reserves 10% for CUDA context and PyTorch allocator overhead.
	gpuMemoryUtilization = 0.90
	gibBytes             = 1024 * 1024 * 1024

	// maxSupportedTransformersMajor is the highest major version of transformers
	// that vLLM 0.19.0 supports. Models requiring 5.x or higher won't work.
	maxSupportedTransformersMajor = 4
)

// isTransformersVersionUnsupported checks if the model requires a transformers
// version that's too new for the current vLLM version.
// Returns (true, reason) if unsupported, (false, "") if supported or unknown.
// vllmVersion is the configured vLLM image tag; empty string produces a
// generic message without a specific version.
func isTransformersVersionUnsupported(version, vllmVersion string) (bool, string) {
	if version == "" {
		return false, "" // Unknown version, assume compatible
	}

	// Parse major version from strings like "4.45.0", "5.3.0", "5.5.0.dev0"
	var major int
	if _, err := fmt.Sscanf(version, "%d.", &major); err != nil {
		return false, "" // Can't parse, assume compatible
	}

	if major > maxSupportedTransformersMajor {
		vllmDesc := "the configured vLLM"
		if vllmVersion != "" {
			vllmDesc = "vLLM " + vllmVersion
		}
		return true, fmt.Sprintf(
			"Model requires transformers %s but %s only supports transformers %d.x. "+
				"This model architecture is too new. Wait for a newer vLLM release or use a different model.",
			version, vllmDesc, maxSupportedTransformersMajor)
	}

	return false, ""
}

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

// effectiveKVCacheLength returns the effective context length for KV cache sizing.
// For models with sliding window attention (e.g., Mistral), KV cache is capped
// at the window size, dramatically reducing memory requirements for long contexts.
func effectiveKVCacheLength(maxModelLen int, slidingWindow int) int {
	if slidingWindow > 0 && slidingWindow < maxModelLen {
		return slidingWindow
	}
	return maxModelLen
}

// inferenceOverheadBytes estimates vLLM-specific overhead beyond the 10% reserved
// by gpu_memory_utilization. This covers CUDA graphs, activation memory, and
// PyTorch allocator fragmentation.
//
// vLLM pre-captures ~35 CUDA graphs for different batch sizes (total ~3400 tokens).
// Each graph allocates activation buffers proportional to:
//   - Attention: 4 × hidden_size (Q, K, V, output projections)
//   - FFN: intermediate_size (typically 3.5 × hidden_size for gated architectures)
//
// Formula: cuda_context + cuda_graphs_activation + fragmentation_margin
//   = 0.5 GiB + (3400 × (4 × hidden + intermediate) × 2 bytes) + 1.0 GiB
func inferenceOverheadBytes(cfg ModelConfig) float64 {
	const (
		cudaContextGiB       = 0.5  // Fixed CUDA runtime overhead
		fragmentationGiB     = 1.0  // PyTorch allocator fragmentation margin
		cudaGraphTokens      = 3400 // Sum of tokens across ~35 captured batch sizes
		bytesPerActivation   = 2    // FP16 activations
	)

	// Use intermediate_size if available, otherwise estimate as 3.5 × hidden_size
	intermediateSize := cfg.IntermediateSize
	if intermediateSize == 0 {
		intermediateSize = int(float64(cfg.HiddenSize) * 3.5)
	}

	// Activation memory per token: attention (4 × hidden) + FFN (intermediate)
	activationPerToken := float64(4*cfg.HiddenSize+intermediateSize) * bytesPerActivation

	// CUDA graphs activation memory
	cudaGraphsBytes := float64(cudaGraphTokens) * activationPerToken

	// Total overhead
	overhead := (cudaContextGiB+fragmentationGiB)*gibBytes + cudaGraphsBytes

	return overhead
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

// RecommendOptions holds optional overrides for the recommendation algorithm.
type RecommendOptions struct {
	TPOverride          int     // Force specific tensor parallel degree (0 = auto)
	OverheadGiB         float64 // Override calculated overhead (0 = auto-calculate)
	MaxModelLenOverride int     // Force specific max_model_len (0 = auto); concurrency adjusts to fit
	// VLLMVersion is the currently-configured vLLM image tag. Used only in
	// the transformers-compatibility error message so it reflects what's
	// actually running instead of a hardcoded "vLLM 0.19.0" string. Empty
	// string falls back to a generic wording.
	VLLMVersion string
}

// DefaultOverheadGiB calculates the default runtime overhead for a model based on its dimensions.
// This can be used by the UI to show users the calculated default before they override it.
func DefaultOverheadGiB(cfg ModelConfig) float64 {
	return inferenceOverheadBytes(cfg) / gibBytes
}

// Recommend computes configuration recommendations given model and instance specs.
// allInstances is used to suggest a larger instance when the model doesn't fit.
func Recommend(cfg ModelConfig, inst InstanceSpec, allInstances []InstanceSpec, opts RecommendOptions) *Recommendation {
	// Check if the model requires a transformers version that's too new for vLLM.
	// maxSupportedTransformersMajor is a hardcoded constant (4) — bump it and
	// remove this comment when vLLM ships a release that supports transformers 5.x.
	if unsupported, reason := isTransformersVersionUnsupported(cfg.TransformersVersion, opts.VLLMVersion); unsupported {
		return &Recommendation{
			ModelInfo: ModelInfo{
				ParameterCount:        cfg.ParameterCount,
				NativeDtype:           nativeDtype(cfg),
				MaxPositionEmbeddings: cfg.MaxPositionEmbeddings,
				Architecture:          cfg.ModelType,
			},
			InstanceInfo: InstanceInfo{
				AcceleratorCount:     inst.AcceleratorCount,
				AcceleratorMemoryGiB: inst.AcceleratorMemoryGiB,
				AcceleratorName:      inst.AcceleratorName,
			},
			Explanation: Explanation{
				Feasible: false,
				Reason:   reason,
			},
		}
	}

	dtype := nativeDtype(cfg)
	perDeviceGiB := float64(inst.AcceleratorMemoryGiB) / float64(inst.AcceleratorCount)
	perDeviceBytes := perDeviceGiB * gibBytes
	usablePerDevice := perDeviceBytes * gpuMemoryUtilization

	// Calculate model memory:
	// 1. Use ActualMemoryBytes from safetensors dtype breakdown (most accurate for mixed-precision)
	// 2. Fall back to PreQuantBits calculation for pre-quantized models
	// 3. Fall back to native dtype calculation
	var modelMemEffective float64
	if cfg.ActualMemoryBytes > 0 {
		// Use actual memory from safetensors dtype breakdown (most accurate)
		modelMemEffective = float64(cfg.ActualMemoryBytes)
	} else if cfg.PreQuantized && cfg.PreQuantBits > 0 {
		// Fall back to pre-quantization bit width
		effectiveDtype := dtype
		switch cfg.PreQuantBits {
		case 4:
			effectiveDtype = "int4"
		case 8:
			effectiveDtype = "int8"
		}
		modelMemEffective = modelMemoryBytes(cfg.ParameterCount, effectiveDtype)
	} else {
		// Use native precision
		modelMemEffective = modelMemoryBytes(cfg.ParameterCount, dtype)
	}
	modelMemNative := modelMemoryBytes(cfg.ParameterCount, dtype)
	minGPUs := int(math.Ceil(modelMemEffective / usablePerDevice))
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
			SlidingWindow:         cfg.SlidingWindow,
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

	// Handle pre-quantized models: use their actual size, don't suggest additional quantization
	if cfg.PreQuantized {
		if modelMemEffective <= totalUsableBytes {
			// Pre-quantized model fits
			tp := maxValidTPDegree(minGPUs, cfg.NumAttentionHeads, cfg.NumKeyValueHeads, inst.AcceleratorCount)
			if opts.TPOverride > 0 && opts.TPOverride >= minGPUs && opts.TPOverride <= inst.AcceleratorCount {
				if cfg.NumAttentionHeads%opts.TPOverride == 0 && cfg.NumKeyValueHeads%opts.TPOverride == 0 {
					tp = opts.TPOverride
				}
			}
			rec.TensorParallelDegree = tp
			rec.Quantization = nil // Don't apply additional quantization
			chosenQuant = dtype    // KV cache uses native dtype even for pre-quantized models
			rec.Explanation.Quantization = fmt.Sprintf("Model is pre-quantized (%s, %d-bit). Actual weights: %.1f GiB, available: %.0f GiB.",
				cfg.PreQuantMethod, cfg.PreQuantBits, modelMemEffective/gibBytes, totalUsableBytes/gibBytes)
			rec.Explanation.TensorParallelDegree = fmt.Sprintf("TP=%d: pre-quantized model requires %.1f GiB, each %s has %.0f GiB.",
				tp, modelMemEffective/gibBytes, inst.AcceleratorName, perDeviceGiB)
		} else {
			// Pre-quantized model doesn't fit - can't quantize further
			rec.Explanation.Feasible = false
			rec.Explanation.Reason = fmt.Sprintf("Pre-quantized model (%s, %d-bit) requires %.1f GiB but only %.0f GiB available on %s. Use a larger instance.",
				cfg.PreQuantMethod, cfg.PreQuantBits, modelMemEffective/gibBytes, totalUsableBytes/gibBytes, inst.Name)

			// Find a larger instance
			if len(allInstances) > 0 {
				for _, alt := range allInstances {
					if !strings.EqualFold(alt.AcceleratorType, "gpu") {
						continue
					}
					altTotal := float64(alt.AcceleratorMemoryGiB) * gibBytes * gpuMemoryUtilization
					if modelMemEffective <= altTotal && alt.AcceleratorMemoryGiB > inst.AcceleratorMemoryGiB {
						rec.Alternatives = &Alternatives{LargerInstance: alt.Name}
						rec.Explanation.SuggestedInstance = alt.Name
						break
					}
				}
			}
			return rec
		}
	} else if modelMemNative <= totalUsableBytes {
		// Fits at native precision.
		// Default to max valid TP to use all GPUs; allow user override.
		tp := maxValidTPDegree(minGPUs, cfg.NumAttentionHeads, cfg.NumKeyValueHeads, inst.AcceleratorCount)
		if opts.TPOverride > 0 && opts.TPOverride >= minGPUs && opts.TPOverride <= inst.AcceleratorCount {
			// Validate override divides attention heads
			if cfg.NumAttentionHeads%opts.TPOverride == 0 && cfg.NumKeyValueHeads%opts.TPOverride == 0 {
				tp = opts.TPOverride
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
		// FP8: Runtime quantization - requires hardware support (H100/H200/L40S)
		// INT8/INT4: On-the-fly quantization via bitsandbytes - works on any hardware
		var fitsWithQuant bool

		// First try FP8 if hardware supports it
		if supportsFP8(inst.AcceleratorName) {
			fp8Mem := modelMemoryBytes(cfg.ParameterCount, "fp8")
			if fp8Mem <= totalUsableBytes {
				chosenQuant = "fp8"
				fitsWithQuant = true
				rec.Alternatives.QuantizationOption = &QuantizationOption{
					Quantization:         "fp8",
					EstimatedMemGiB:      fp8Mem / gibBytes,
					RequiresPreQuantized: false,
				}
			}
		}

		// If FP8 didn't work, try INT8/INT4 via bitsandbytes (on-the-fly quantization)
		if !fitsWithQuant {
			for _, qName := range []string{"int8", "int4"} {
				qMem := modelMemoryBytes(cfg.ParameterCount, qName)
				if qMem <= totalUsableBytes {
					chosenQuant = qName
					fitsWithQuant = true
					rec.Alternatives.QuantizationOption = &QuantizationOption{
						Quantization:         qName,
						EstimatedMemGiB:      qMem / gibBytes,
						RequiresPreQuantized: false,
					}
					break
				}
			}
		}

		// Find a larger instance that fits at native precision.
		if len(allInstances) > 0 {
			for _, alt := range allInstances {
				if !strings.EqualFold(alt.AcceleratorType, "gpu") {
					continue
				}
				altTotal := float64(alt.AcceleratorMemoryGiB) * gibBytes * gpuMemoryUtilization
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
			rec.Explanation.Quantization = fmt.Sprintf("Model requires %.1f GiB in %s but only %.0f GiB available. Using %s quantization via bitsandbytes (%.1f GiB).",
				modelMemNative/gibBytes, dtype, totalUsableBytes/gibBytes, strings.ToUpper(chosenQuant), qMem/gibBytes)
			rec.Explanation.TensorParallelDegree = fmt.Sprintf("TP=%d with %s quantization: %.1f GiB model across %d × %s.",
				tp, chosenQuant, qMem/gibBytes, inst.AcceleratorCount, inst.AcceleratorName)
		} else {
			// Nothing fits — infeasible on this instance.
			rec.Explanation.Feasible = false
			if supportsFP8(inst.AcceleratorName) {
				rec.Explanation.Reason = fmt.Sprintf("Model requires %.1f GiB in %s. Even FP8 (%.1f GiB) exceeds %.0f GiB available on %s. Use a larger instance or search HuggingFace for a GPTQ or AWQ quantized variant of this model.",
					modelMemNative/gibBytes, dtype, modelMemoryBytes(cfg.ParameterCount, "fp8")/gibBytes,
					totalUsableBytes/gibBytes, inst.Name)
			} else {
				rec.Explanation.Reason = fmt.Sprintf("Model requires %.1f GiB in %s but only %.0f GiB available on %s. This GPU doesn't support runtime quantization. Use a larger instance or search HuggingFace for a GPTQ or AWQ quantized variant of this model.",
					modelMemNative/gibBytes, dtype, totalUsableBytes/gibBytes, inst.Name)
			}
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
	// Use user-provided overhead if specified, otherwise calculate from model dimensions
	var runtimeOverhead float64
	if opts.OverheadGiB > 0 {
		runtimeOverhead = opts.OverheadGiB * gibBytes
	} else {
		runtimeOverhead = inferenceOverheadBytes(cfg)
	}
	rec.OverheadGiB = runtimeOverhead / gibBytes
	// Use memory from the GPUs actually being used (TP), not total instance memory.
	// With TP=1 on a 4-GPU instance, only 1 GPU's memory is available for KV cache.
	tpUsableBytes := usablePerDevice * float64(rec.TensorParallelDegree)
	remainingBytes := tpUsableBytes - effectiveModelMem - runtimeOverhead
	if remainingBytes < 0 {
		remainingBytes = 0
	}

	maxTokensKV := int(remainingBytes / kvPerToken)
	nativeMaxModelLen := cfg.MaxPositionEmbeddings
	maxModelLen := nativeMaxModelLen
	if maxTokensKV < maxModelLen {
		maxModelLen = maxTokensKV
	}
	maxModelLen = roundDownContext(maxModelLen)

	// If user explicitly overrides max_model_len, use that as the target.
	// Concurrency will be calculated to fit within the KV budget.
	userOverrideMaxModelLen := opts.MaxModelLenOverride > 0
	if userOverrideMaxModelLen {
		maxModelLen = roundDownContext(opts.MaxModelLenOverride)
		if maxModelLen > maxTokensKV {
			maxModelLen = roundDownContext(maxTokensKV)
		}
	}

	// Scale input/output sequence lengths based on available context.
	switch {
	case maxModelLen >= 16384:
		rec.InputSequenceLength = 2048
		rec.OutputSequenceLength = 1024
	case maxModelLen >= 8192:
		rec.InputSequenceLength = 1024
		rec.OutputSequenceLength = 512
	case maxModelLen >= 4096:
		rec.InputSequenceLength = 512
		rec.OutputSequenceLength = 256
	default:
		rec.InputSequenceLength = maxModelLen * 2 / 3
		rec.OutputSequenceLength = maxModelLen / 3
	}

	// Calculate concurrency: KV budget / (tokens per sequence × bytes per token).
	avgSeqLen := float64(rec.InputSequenceLength + rec.OutputSequenceLength)
	effectiveSeqLen := float64(effectiveKVCacheLength(int(avgSeqLen), cfg.SlidingWindow))
	memPerSeq := kvPerToken * effectiveSeqLen
	maxConcurrent := 1
	if memPerSeq > 0 {
		maxConcurrent = int(remainingBytes / memPerSeq)
	}
	if maxConcurrent > 64 {
		maxConcurrent = 64
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}

	// Joint constraint: ensure max_model_len × concurrency fits within 90%
	// of the KV cache budget. vLLM uses paged attention so not every slot is
	// fully allocated, but we need headroom to prevent OOM under load.
	kvBudgetTokens := int(0.9 * remainingBytes / kvPerToken)
	benchMaxModelLen := maxModelLen // save before adjustment for production note
	if maxModelLen*maxConcurrent > kvBudgetTokens {
		if userOverrideMaxModelLen {
			// User chose max_model_len; adjust concurrency to fit.
			maxConcurrent = kvBudgetTokens / maxModelLen
			if maxConcurrent < 1 {
				maxConcurrent = 1
			}
		} else {
			// Auto mode: prefer keeping concurrency high, reduce max_model_len.
			safeMaxModelLen := roundDownContext(kvBudgetTokens / maxConcurrent)
			minModelLen := roundDownContext((rec.InputSequenceLength + rec.OutputSequenceLength) * 2)
			if safeMaxModelLen >= minModelLen {
				maxModelLen = safeMaxModelLen
			} else {
				maxModelLen = minModelLen
				maxConcurrent = kvBudgetTokens / maxModelLen
				if maxConcurrent < 1 {
					maxConcurrent = 1
				}
			}
			benchMaxModelLen = maxModelLen
		}
	}

	rec.MaxModelLen = maxModelLen
	rec.Concurrency = maxConcurrent

	// Production note: when the benchmark config uses less than the model's
	// full context, show what concurrency would look like at full context.
	if !userOverrideMaxModelLen && benchMaxModelLen < nativeMaxModelLen && kvBudgetTokens > 0 {
		fullContextConcurrency := kvBudgetTokens / nativeMaxModelLen
		if fullContextConcurrency < 1 {
			fullContextConcurrency = 1
		}
		rec.Explanation.ProductionNote = fmt.Sprintf(
			"For full %d-token context, set max_model_len=%d and reduce concurrency to %d.",
			nativeMaxModelLen, nativeMaxModelLen, fullContextConcurrency)
	}

	// Generate explanation for max_model_len
	if cfg.SlidingWindow > 0 {
		rec.Explanation.MaxModelLen = fmt.Sprintf("%.1f GiB available for KV cache (TP=%d × %.0f GiB per GPU). Set to %d tokens to safely support %d concurrent requests. Note: Model uses sliding window attention (%d tokens).",
			remainingBytes/gibBytes, rec.TensorParallelDegree, perDeviceGiB, maxModelLen, maxConcurrent, cfg.SlidingWindow)
	} else {
		rec.Explanation.MaxModelLen = fmt.Sprintf("%.1f GiB available for KV cache (TP=%d × %.0f GiB per GPU). Set to %d tokens to safely support %d concurrent requests.",
			remainingBytes/gibBytes, rec.TensorParallelDegree, perDeviceGiB, maxModelLen, maxConcurrent)
	}

	// Generate explanation for concurrency
	if cfg.SlidingWindow > 0 && cfg.SlidingWindow < int(avgSeqLen) {
		rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory. Sliding window (%d tokens) caps KV per sequence, enabling higher concurrency.",
			remainingBytes/gibBytes, cfg.SlidingWindow)
	} else {
		rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory with %d-token average sequence length.",
			remainingBytes/gibBytes, int(avgSeqLen))
	}

	return rec
}
