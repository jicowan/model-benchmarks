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

	// PipelineTag from the HuggingFace /api/models response (e.g.
	// "text-generation", "feature-extraction", "text-to-image"). Used to
	// reject embedding / diffusion / classifier models that inference-perf
	// can't drive. Empty when fetched from an S3-cached config (HF pipeline
	// tag isn't in config.json); callers fall back to architecture sniffing.
	PipelineTag string `json:"pipeline_tag,omitempty"`

	// Architectures from config.json. Populated alongside ModelType so the
	// recommender can cross-check for encoder-only / pooling models when
	// PipelineTag is missing.
	Architectures []string `json:"architectures,omitempty"`
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

	// PRD-46: vLLM scheduler knobs. Zero / empty means "let vLLM pick
	// its default." Populated for GPU runs; Neuron recommender leaves
	// them at zero-value since Neuron vLLM doesn't accept these flags.
	MaxNumBatchedTokens int    `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype        string `json:"kv_cache_dtype,omitempty"`

	Explanation  Explanation  `json:"explanation"`
	ModelInfo    ModelInfo    `json:"model_info"`
	InstanceInfo InstanceInfo `json:"instance_info"`

	// Alternatives is non-nil when the model doesn't fit at native precision.
	Alternatives *Alternatives `json:"alternatives,omitempty"`

	// PRD-51: non-blocking guidance about footgun combinations. The UI
	// renders these as an informational block alongside the
	// recommendation. Unlike Explanation.Reason (which blocks the
	// submission when infeasible), warnings are advisory — the user
	// can still submit.
	Warnings []string `json:"warnings,omitempty"`
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

	// maxSupportedTransformersMajor is the highest major version of the
	// transformers package that the bundled vLLM supports. vLLM 0.19.1+
	// requires transformers 5.x (see release notes), so we accept up to 5.
	// When vLLM ships a version requiring transformers 6.x, bump this and
	// update the infeasibility reason wording in
	// isTransformersVersionUnsupported.
	maxSupportedTransformersMajor = 5
)

// unsupportedPipelineTags names HuggingFace pipeline tags that vLLM +
// inference-perf can't drive through the chat/completion load types the
// rest of the platform uses. Encoder-only / pooling / diffusion / classifier
// models expose different endpoints (/v1/embeddings, /pooling, etc.), which
// the loadgen would POST /v1/completions against and 404 every request.
var unsupportedPipelineTags = map[string]string{
	"feature-extraction":             "embedding",
	"sentence-similarity":            "embedding",
	"fill-mask":                      "masked language model",
	"text-classification":            "text classifier",
	"token-classification":           "token classifier",
	"zero-shot-classification":       "zero-shot classifier",
	"text-to-image":                  "diffusion",
	"image-to-image":                 "diffusion",
	"text-to-video":                  "diffusion",
	"image-to-video":                 "diffusion",
	"text-to-3d":                     "diffusion",
	"image-to-3d":                    "diffusion",
	"unconditional-image-generation": "diffusion",
	"text-to-audio":                  "audio",
	"text-to-speech":                 "audio",
	"automatic-speech-recognition":   "audio",
	"audio-to-audio":                 "audio",
	"image-classification":           "vision classifier",
	"object-detection":               "vision",
	"image-segmentation":             "vision",
	"depth-estimation":               "vision",
}

// isUnsupportedModelKind returns a human-readable reason if the model isn't a
// decoder-only LLM that vLLM + inference-perf's chat/completion load types can
// drive. Relies on PipelineTag when present (fetched from HF), falls back to
// architecture-name heuristics for S3-cached configs where the tag is missing.
// Returns "" if the model is supported or undeterminable.
func isUnsupportedModelKind(cfg ModelConfig) string {
	if cfg.PipelineTag != "" {
		if kind, bad := unsupportedPipelineTags[cfg.PipelineTag]; bad {
			return fmt.Sprintf("Model pipeline tag is %q (%s). This platform benchmarks decoder-only LLMs via /v1/completions or /v1/chat/completions — %s models expose different endpoints that inference-perf can't drive.",
				cfg.PipelineTag, kind, kind)
		}
		// Explicit text-generation tag: trust and continue.
		if cfg.PipelineTag == "text-generation" {
			return ""
		}
	}
	// Fallback for S3-cached models or untagged repos: sniff architecture names.
	// Pooling / embedding models almost always declare an architecture whose
	// name ends in "Model" (e.g. BertModel, ModernBertModel, XLMRobertaModel),
	// whereas decoder LMs end in "ForCausalLM" / "LMHeadModel".
	for _, arch := range cfg.Architectures {
		low := strings.ToLower(arch)
		if strings.Contains(low, "forcausallm") || strings.Contains(low, "lmheadmodel") {
			return "" // Clearly a causal LM.
		}
		if strings.HasSuffix(low, "formaskedlm") ||
			strings.HasSuffix(low, "forsequenceclassification") ||
			strings.HasSuffix(low, "fortokenclassification") ||
			strings.HasSuffix(low, "forquestionanswering") {
			return fmt.Sprintf("Model architecture %q is not a decoder-only LLM. This platform benchmarks generative models only.", arch)
		}
	}
	return ""
}

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

// Host-memory checking constants. Defaults; overridden per-family by
// observed p95 when available (PRD-47 PR #5).
//
// hfLoaderHostMultiplier — the HuggingFace loader materializes full
// BF16 weights in CPU RAM before copying to GPU. Empirical: Phi-4
// (30.2 GiB weights) hit 31.1 GiB peak on g6e.2xlarge → ~1.03×.
// Keep 1.08 as a safe bound; tuned across Llama / Mistral / Phi
// observations.
//
// s3StreamerHostMultiplier — the Run:ai streamer streams `concurrency`
// shards at a time directly to GPU, so peak is bounded by
// concurrency × avg_shard_size. For a typical 5-30 shard model, that
// lands well under half the weight size. 0.5 is a conservative upper
// bound for the common case; the single observed worst case
// (Qwen3-8B with 399 safetensors shards at concurrency=16) really did
// push toward 0.95×, but applying that constant to every model
// incorrectly rejected Llama / Mistral / Qwen2.5 on 16 GiB-host L40S
// instances. Per-family calibration (PR #5) catches the Qwen3 outlier
// once the history column fills in.
//
// hostMemAllocatableFrac — kubelet+system reservations on EKS AL2023
// are ~750 MiB kubelet + ~250 MiB system ≈ 1 GiB fixed. On a 16 GiB
// host that's ~94% allocatable; on 64 GiB it's ~98%. A flat 85%
// under-approximated allocatable on larger hosts. Tiered:
//   MemoryGiB >= 64 → 0.92
//   MemoryGiB <  64 → 0.85 (keep safety margin where overhead bites most)
const (
	hfLoaderHostMultiplier   = 1.08
	// s3StreamerHostMultiplier: empirical median across 8 completed TP=1 runs
	// on the S3 streamer path is ~1.15 (range 0.99–1.19). The streamer's
	// default RUNAI_STREAMER_MEMORY_LIMIT=-1 allocates a buffer equal to the
	// full safetensor file, so real peak is ≈ weights + small fixed overhead,
	// not the "concurrency × shard_size" 0.5× we initially guessed. See
	// PRD-47 and run-ai/runai-model-streamer docs for background.
	s3StreamerHostMultiplier = 1.15
	hostMemBufferBytes       = 2 * gibBytes
	hostMemAllocatableFracSmall = 0.85 // hosts < 64 GiB
	hostMemAllocatableFracLarge = 0.92 // hosts ≥ 64 GiB
	hostMemAllocatableLargeThresholdGiB = 64
)

// hostAllocatableFrac returns the allocatable fraction of host memory
// for a given total-host-memory size. Larger hosts see more of their
// RAM as usable because kubelet overhead is roughly fixed-absolute.
func hostAllocatableFrac(memoryGiB int) float64 {
	if memoryGiB >= hostMemAllocatableLargeThresholdGiB {
		return hostMemAllocatableFracLarge
	}
	return hostMemAllocatableFracSmall
}

// peakHostMemBytes estimates peak host RAM during model load. The
// multiplier reflects whether vLLM's HuggingFace loader (CPU-resident
// weights) or Run:ai streamer (streaming layers) is used.
//
// PRD-47: when modelType is non-empty AND the calibration map has a
// matching entry, use the observed p95 ratio instead of the hand-tuned
// default. Unseen types keep the conservative default.
//
// PRD-51: streamerBufferBytes is the predicted streamer CPU-buffer
// size for the candidate run — `min(weight, memory_limit)` when the
// streamer will be active, zero otherwise. The calibration ratio is
// now NON-streamer per-weight overhead (PRD-51 subtracts the streamer
// term in the SQL before the p95), so we add the streamer term back
// here ONLY when a calibrated ratio was actually applied. Without
// calibration, the hand-tuned s3StreamerHostMultiplier (1.15) still
// bundles the streamer contribution, so adding streamerBufferBytes
// on top would double-count.
func peakHostMemBytes(
	modelWeightBytes float64,
	useS3Streamer bool,
	modelType string,
	calibration map[string]float64,
	streamerBufferBytes float64,
) float64 {
	mult := hfLoaderHostMultiplier
	if useS3Streamer {
		mult = s3StreamerHostMultiplier
	}
	usedCalibration := false
	if modelType != "" && len(calibration) > 0 {
		loader := "hf"
		if useS3Streamer {
			loader = "s3"
		}
		if observed, ok := calibration[modelType+"|"+loader]; ok && observed > 0 {
			mult = observed
			usedCalibration = true
		}
	}
	// Only add the explicit streamer term on top when we used the
	// PRD-51 non-streamer calibration ratio. The hand-tuned defaults
	// still bundle the streamer; adding it again would double-count.
	if !usedCalibration {
		streamerBufferBytes = 0
	}
	return modelWeightBytes*mult + hostMemBufferBytes + streamerBufferBytes
}

// checkHostMemory returns (ok, reason, suggestedInstance). ok=true when
// peak host-RAM usage fits comfortably under the instance's allocatable
// host memory. On failure, suggestedInstance is the smallest larger GPU
// instance from allInstances that would fit, or "" if none found.
//
// `modelWeightBytes` is the native-precision weight size — we intentionally
// don't reduce it for on-the-fly quantization (int8/int4 via bitsandbytes)
// because the loader still materializes full BF16 weights in host RAM
// before quantizing. Only pre-quantized models (GPTQ/AWQ/NVFP4/etc., with
// smaller on-disk weights) would reduce peak host RAM, and callers should
// pass cfg.ActualMemoryBytes in that case.
func checkHostMemory(
	modelWeightBytes float64,
	inst InstanceSpec,
	allInstances []InstanceSpec,
	useS3Streamer bool,
	modelType string,
	calibration map[string]float64,
	streamerBufferBytes float64,
) (bool, string, string) {
	// inst.MemoryGiB == 0 means the instance catalog doesn't carry host-mem
	// info. Skip the check rather than reject conservatively.
	if inst.MemoryGiB == 0 {
		return true, "", ""
	}
	peak := peakHostMemBytes(modelWeightBytes, useS3Streamer, modelType, calibration, streamerBufferBytes)
	allocatable := float64(inst.MemoryGiB) * gibBytes * hostAllocatableFrac(inst.MemoryGiB)
	if peak <= allocatable {
		return true, "", ""
	}

	path := "HuggingFace"
	remedy := "Use a larger instance, or cache the model in S3 to load via the Run:ai streamer (which keeps host RAM close to the weight-stream buffer)"
	if useS3Streamer {
		path = "the Run:ai streamer from S3"
		remedy = "Use a larger instance"
	}

	reason := fmt.Sprintf(
		"Model weights in %s precision (%.1f GiB) require ~%.1f GiB of host RAM to load via %s, but %s allocates only %.1f GiB after kubelet/DaemonSet reservations. %s.",
		// The precision label is a best-effort hint; callers can't easily
		// thread the dtype name in without cfg, so just say "native" here.
		"native",
		modelWeightBytes/gibBytes,
		peak/gibBytes,
		path,
		inst.Name,
		allocatable/gibBytes,
		remedy,
	)

	var suggested string
	for _, alt := range allInstances {
		if !strings.EqualFold(alt.AcceleratorType, "gpu") {
			continue
		}
		// Only suggest strictly larger host memory; if the caller passes
		// the current instance in allInstances, we don't want to echo it.
		if alt.MemoryGiB <= inst.MemoryGiB {
			continue
		}
		altAllocatable := float64(alt.MemoryGiB) * gibBytes * hostAllocatableFrac(alt.MemoryGiB)
		if peak <= altAllocatable {
			suggested = alt.Name
			break
		}
	}
	return false, reason, suggested
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

// SupportsFP8KVCache returns true if the accelerator has native FP8
// compute (Hopper / Ada Lovelace) and therefore can halve KV-cache
// memory with --kv-cache-dtype=fp8 at negligible quality cost. Exported
// so handlers can default the flag without re-running the full
// recommender.
func SupportsFP8KVCache(acceleratorName string) bool {
	return supportsFP8(acceleratorName)
}

// modelMemoryBytes returns the model weight memory in bytes for a given quantization.
func modelMemoryBytes(params int64, quant string) float64 {
	return float64(params) * bytesPerParam(quant)
}

// KVCachePerTokenBytes returns KV cache memory per token in bytes.
// Formula: 2 (K+V) × num_layers × num_kv_heads × head_dim × bytes_per_element
//
// bytes_per_element is derived from kvCacheDtype: fp16/bf16 = 2, fp8 = 1.
// PRD-46 wires --kv-cache-dtype=fp8 as the default on H100/H200/L40S, so the
// recommender must size KV budget to match. An empty string means
// "auto / matches compute dtype", which we treat as fp16.
//
// Exported so the memory-breakdown handler shares one source of truth with
// the recommender's feasibility math.
func KVCachePerTokenBytes(cfg ModelConfig, kvCacheDtype string) float64 {
	return kvCachePerTokenBytes(cfg, kvCacheDtype)
}

func kvCachePerTokenBytes(cfg ModelConfig, kvCacheDtype string) float64 {
	bytesPerElement := 2.0 // fp16 / bf16 / auto
	switch kvCacheDtype {
	case "fp8", "fp8_e4m3", "fp8_e5m2":
		bytesPerElement = 1.0
	}
	headDim := float64(cfg.HiddenSize) / float64(cfg.NumAttentionHeads)
	return 2 * float64(cfg.NumHiddenLayers) * float64(cfg.NumKeyValueHeads) * headDim * bytesPerElement
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
	// PRD-46: force a specific --max-num-batched-tokens (0 = auto-derive
	// from ISL). Useful for prefill-budget sweeps; bypasses the
	// max(2048, ISL) floor.
	MaxNumBatchedTokensOverride int
	// VLLMVersion is the currently-configured vLLM image tag. Used only in
	// the transformers-compatibility error message so it reflects what's
	// actually running instead of a hardcoded "vLLM 0.19.0" string. Empty
	// string falls back to a generic wording.
	VLLMVersion string

	// UseS3Streamer indicates the model will be loaded via vLLM's Run:ai
	// streamer from an S3-cached prefix (ModelS3URI set on the run request).
	// The streamer keeps only a small layer buffer in host RAM, so peak host
	// memory during load is far lower than the HuggingFace loader path
	// (which materializes full weights in CPU memory before copying to GPU).
	// The host-memory feasibility check consults this to decide whether a
	// given instance can fit the model weight load in container RAM.
	UseS3Streamer bool

	// ModelType is HuggingFace's canonical architecture name from
	// config.json ("llama", "qwen2", "qwen3", "phi3", "mistral",
	// "gpt_oss", …). Used as the per-family calibration key. Empty
	// string disables calibration for this call; the recommender falls
	// back to the hand-tuned default multipliers.
	ModelType string

	// HostMemCalibration maps `"{model_type}|{loader}"` to the observed
	// p95 of host_memory_peak / weight_size for completed runs in that
	// bucket. Loader is "hf" or "s3". When a matching key is present and
	// the ratio is positive, the recommender uses that instead of the
	// hard-coded defaults for the host-memory feasibility check.
	// Unseen buckets keep the defaults. PRD-47. After PRD-51 the stored
	// ratios are NON-streamer overhead only — the recommender adds the
	// streamer term back via StreamerMemoryLimitGiB below.
	HostMemCalibration map[string]float64

	// StreamerMemoryLimitGiB is the cap the user set on
	// RUNAI_STREAMER_MEMORY_LIMIT (PRD-50). 0 means "auto-size to
	// min(weight, instance_memory / 2)", matching the orchestrator's
	// default. Ignored when UseS3Streamer is false. PRD-51 uses this to
	// size the streamer buffer term added back to the calibrated
	// non-streamer ratio.
	StreamerMemoryLimitGiB int
}

// DefaultOverheadGiB calculates the default runtime overhead for a model based on its dimensions.
// This can be used by the UI to show users the calculated default before they override it.
func DefaultOverheadGiB(cfg ModelConfig) float64 {
	return inferenceOverheadBytes(cfg) / gibBytes
}

// Recommend computes configuration recommendations given model and instance specs.
// allInstances is used to suggest a larger instance when the model doesn't fit.
func Recommend(cfg ModelConfig, inst InstanceSpec, allInstances []InstanceSpec, opts RecommendOptions) *Recommendation {
	// Reject embedding / diffusion / classifier models up front — the loadgen
	// only drives /v1/completions and /v1/chat/completions, which these
	// architectures don't expose.
	if reason := isUnsupportedModelKind(cfg); reason != "" {
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

	// Host-memory check. Runs AFTER model-memory computation so the helper
	// can compare peak loader RAM against the instance's allocatable host
	// memory. Failing this gate marks the pair infeasible and suggests a
	// larger instance, even if the GPU math would otherwise succeed. This
	// catches cases like "Qwen3-8B bfloat16 on g6.xlarge" where 15 GiB
	// weights fit in 24 GiB GPU memory but won't fit in 13 GiB container
	// RAM during HuggingFace-loader weight materialization.
	//
	// Pre-quantized models use modelMemEffective (smaller on-disk weight
	// size); everything else uses modelMemNative, since on-the-fly INT8/
	// INT4 via bitsandbytes doesn't reduce peak host RAM.
	hostCheckBytes := modelMemNative
	if cfg.PreQuantized && cfg.ActualMemoryBytes > 0 {
		hostCheckBytes = modelMemEffective
	}
	// PRD-51: streamer CPU buffer term. Zero when the streamer won't
	// run (HF load) or when the user disabled it (PRD-50 streamer_mode=off;
	// expressed here as opts.UseS3Streamer=false — the handler gates on
	// the same signals). When active, the buffer is min(weight, limit);
	// auto-size default is min(weight, instance_memory / 2).
	streamerBufferBytes := 0.0
	if opts.UseS3Streamer {
		limitBytes := float64(opts.StreamerMemoryLimitGiB) * gibBytes
		if opts.StreamerMemoryLimitGiB == 0 {
			// Auto-size: half the node RAM. Mirrors orchestrator.
			limitBytes = float64(inst.MemoryGiB) * gibBytes / 2
		}
		if hostCheckBytes < limitBytes {
			streamerBufferBytes = hostCheckBytes
		} else {
			streamerBufferBytes = limitBytes
		}
	}
	if ok, reason, suggested := checkHostMemory(hostCheckBytes, inst, allInstances, opts.UseS3Streamer, opts.ModelType, opts.HostMemCalibration, streamerBufferBytes); !ok {
		rec := &Recommendation{
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
			Explanation: Explanation{
				Feasible:          false,
				Reason:            reason,
				SuggestedInstance: suggested,
			},
		}
		if suggested != "" {
			rec.Alternatives = &Alternatives{LargerInstance: suggested}
		}
		return rec
	}
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

	// PRD-46: decide --kv-cache-dtype up front so KV budget math below
	// matches the dtype vLLM will actually use. On FP8-capable GPUs
	// (H100/H200/L40S) we default to fp8, which halves KV per token and
	// roughly doubles max_model_len / concurrency compared to fp16.
	kvCacheDtype := ""
	if supportsFP8(inst.AcceleratorName) {
		kvCacheDtype = "fp8"
	}

	// Calculate max model length.
	// Beyond raw weights, vLLM consumes GPU memory for:
	//   1. CUDA context + runtime: ~0.5 GiB (fixed)
	//   2. CUDA graph captures: vLLM pre-captures ~35 graphs for different
	//      batch sizes (sum ≈ 3400 tokens). Each graph allocates per-layer
	//      activation buffers proportional to hidden_size and FFN width.
	//      FFN intermediate_size ≈ 3.5 × hidden_size for gated architectures.
	kvPerToken := kvCachePerTokenBytes(cfg, kvCacheDtype)
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

	// PRD-47: concurrency is sized against maxModelLen consistently.
	// vLLM pre-provisions a KV slot big enough to fit a full-length
	// request per in-flight sequence (paged attention reclaims idle
	// pages, but worst-case inputs still push toward the full slot).
	// Using avgSeqLen here and maxModelLen in the joint constraint
	// below chained two different cost models together and produced
	// outputs that were hard to reason about. One model, one scalar:
	// plan the budget for the largest request vLLM might accept.
	//
	// Cost: on models where the user keeps a long max_model_len for
	// headroom but actually runs short requests, the recommended
	// concurrency is 2-3× lower than the workload needs. Remedy:
	// lower max_model_len (explained in Explanation.Concurrency below).
	effectiveSeqLen := float64(effectiveKVCacheLength(maxModelLen, cfg.SlidingWindow))
	memPerSeq := kvPerToken * effectiveSeqLen
	kvBudgetBytes := 0.9 * remainingBytes // 10% headroom for paged-attention churn
	maxConcurrent := 1
	if memPerSeq > 0 {
		maxConcurrent = int(kvBudgetBytes / memPerSeq)
	}
	if maxConcurrent > 64 {
		maxConcurrent = 64
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}

	// Auto mode: if maxConcurrent ended up at 1 because maxModelLen is
	// huge, try trimming maxModelLen down to recover parallelism.
	// User-overridden maxModelLen is sacred — we respect their choice.
	benchMaxModelLen := maxModelLen // save for production note
	if !userOverrideMaxModelLen && maxConcurrent == 1 && memPerSeq > 0 {
		minModelLen := roundDownContext((rec.InputSequenceLength + rec.OutputSequenceLength) * 2)
		for maxConcurrent < 4 && maxModelLen > minModelLen {
			maxModelLen = roundDownContext(maxModelLen / 2)
			if maxModelLen < minModelLen {
				maxModelLen = minModelLen
			}
			effectiveSeqLen = float64(effectiveKVCacheLength(maxModelLen, cfg.SlidingWindow))
			memPerSeq = kvPerToken * effectiveSeqLen
			if memPerSeq > 0 {
				maxConcurrent = int(kvBudgetBytes / memPerSeq)
			}
			if maxConcurrent > 64 {
				maxConcurrent = 64
				break
			}
			if maxModelLen == minModelLen {
				break
			}
		}
		benchMaxModelLen = maxModelLen
	}

	rec.MaxModelLen = maxModelLen
	rec.Concurrency = maxConcurrent

	// Production note: when the benchmark config uses less than the model's
	// full context, show what concurrency would look like at full context.
	kvBudgetTokens := int(kvBudgetBytes / kvPerToken)
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

	// Generate explanation for concurrency. PRD-47 sizes concurrency
	// against max_model_len (not avg request length) so vLLM can still
	// fit worst-case inputs per slot. Lowering max_model_len trades
	// context for parallelism.
	if cfg.SlidingWindow > 0 && cfg.SlidingWindow < maxModelLen {
		rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory (90%% budgeted). Sliding window (%d tokens) caps KV per sequence, enabling higher concurrency than max_model_len=%d alone would allow.",
			remainingBytes/gibBytes, cfg.SlidingWindow, maxModelLen)
	} else {
		rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory (90%% budgeted), sized for max_model_len=%d tokens per slot. Lower max_model_len for higher concurrency.",
			remainingBytes/gibBytes, maxModelLen)
	}

	// PRD-51: --max-num-batched-tokens defaults to vLLM's own
	// device-tuned value (2048 on A10G/L4/L40S/A100, 8192 on
	// H100/H200/MI300x). We only emit the flag when:
	//   - user explicitly overrides, OR
	//   - ISL exceeds vLLM's largest device default (8192), so a
	//     single prompt can't fit in one prefill iteration with the
	//     built-in default.
	// Otherwise we emit nothing and let vLLM pick. PRD-46's
	// `max(2048, ISL)` formula silently capped prefill at ISL on
	// ISL=2048 runs, starving batching and producing 55% success
	// rates in the Mistral-7B chatbot benchmark (2026-05-13).
	mnbt := 0
	if opts.MaxNumBatchedTokensOverride > 0 {
		mnbt = opts.MaxNumBatchedTokensOverride
	} else if rec.InputSequenceLength > 8192 {
		mnbt = rec.InputSequenceLength
		if rec.MaxModelLen > 0 && mnbt > rec.MaxModelLen {
			mnbt = rec.MaxModelLen
		}
	}
	rec.MaxNumBatchedTokens = mnbt

	// PRD-46 / PRD-47: KV cache dtype decided up front (see kvCacheDtype
	// above) so the KV budget math uses the right bytes/element; emit
	// it here. FP8 on H100/H200/L40S halves KV memory with negligible
	// quality impact under throughput benchmarks.
	rec.KVCacheDtype = kvCacheDtype

	// PRD-51: non-blocking warnings for common footguns.
	rec.Warnings = collectWarnings(rec, cfg, inst, opts)

	return rec
}

// collectWarnings returns advisory messages about knob combinations
// that will silently degrade a run. These don't block submission —
// they let the user see the risk and decide.
func collectWarnings(rec *Recommendation, cfg ModelConfig, inst InstanceSpec, opts RecommendOptions) []string {
	var out []string

	// (a) mnbt override below ISL with concurrency > 1 — starves batching.
	if opts.MaxNumBatchedTokensOverride > 0 &&
		opts.MaxNumBatchedTokensOverride < rec.InputSequenceLength &&
		rec.Concurrency > 1 {
		out = append(out, fmt.Sprintf(
			"max_num_batched_tokens (%d) is below input-sequence-length (%d). "+
				"Only one prompt can prefill per step; concurrent requests will queue. "+
				"Leave unset for vLLM's tuned default.",
			opts.MaxNumBatchedTokensOverride, rec.InputSequenceLength))
	}

	// (b) streamer_memory_limit approaches instance RAM.
	if opts.UseS3Streamer && opts.StreamerMemoryLimitGiB > 0 && inst.MemoryGiB > 0 {
		threshold := float64(inst.MemoryGiB) * 0.9
		if float64(opts.StreamerMemoryLimitGiB) > threshold {
			out = append(out, fmt.Sprintf(
				"Streamer memory limit (%d GiB) approaches instance RAM (%d GiB). "+
					"Load-phase OOM is likely. Lower the limit or pick a bigger instance.",
				opts.StreamerMemoryLimitGiB, inst.MemoryGiB))
		}
	}

	// (c) streamer disabled for an S3 model — vLLM's default loader
	// against S3 isn't universally supported.
	// This signal isn't available here directly (opts.UseS3Streamer is
	// already the resolved value), so the warning is emitted by the
	// API handler where the raw streamer_mode / cache-status pair is
	// visible. Placeholder left here as a reminder.

	return out
}
