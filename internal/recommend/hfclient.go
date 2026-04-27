package recommend

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HFClientInterface defines the methods required for fetching model metadata.
// This allows for easy mocking in tests.
type HFClientInterface interface {
	FetchModelConfig(modelID, hfToken string) (*ModelConfig, error)
}

// HFClient fetches model metadata from the HuggingFace API.
type HFClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewHFClient creates a new HuggingFace API client.
func NewHFClient() *HFClient {
	return &HFClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    "https://huggingface.co",
	}
}

// hfModelResponse is the subset of the HuggingFace /api/models response we need.
type hfModelResponse struct {
	Safetensors *struct {
		Parameters map[string]int64 `json:"parameters"`
		Total      int64            `json:"total"`
	} `json:"safetensors"`
	Config *struct {
		ModelType     string   `json:"model_type"`
		Architectures []string `json:"architectures"`
	} `json:"config"`
	// PipelineTag is the HF pipeline category ("text-generation",
	// "feature-extraction", "text-to-image", ...). The recommender uses it
	// to reject non-decoder models that the loadgen can't drive.
	PipelineTag string `json:"pipeline_tag"`
	// Gated is false for public models, or "auto"/"manual" for gated models.
	Gated any `json:"gated"`
}

// hfQuantizationConfig represents the quantization_config from HuggingFace config.json.
// This indicates the model is pre-quantized (GPTQ, AWQ, NVFP4, bitsandbytes, etc.)
type hfQuantizationConfig struct {
	// Common fields across quantization methods
	QuantMethod string `json:"quant_method"` // "gptq", "awq", "modelopt", "bitsandbytes"
	Bits        int    `json:"bits"`         // GPTQ/AWQ: 4 or 8

	// NVIDIA ModelOpt (NVFP4, etc.)
	QuantAlgo    string                     `json:"quant_algo"` // "NVFP4"
	ConfigGroups map[string]hfConfigGroup   `json:"config_groups"`

	// bitsandbytes
	LoadIn4Bit bool `json:"load_in_4bit"`
	LoadIn8Bit bool `json:"load_in_8bit"`
}

// hfConfigGroup represents a quantization config group (for ModelOpt).
type hfConfigGroup struct {
	Weights *hfQuantBits `json:"weights"`
}

// hfQuantBits holds bit width info.
type hfQuantBits struct {
	NumBits int `json:"num_bits"`
}

// hfConfigJSON is the subset of a model's config.json we need.
type hfConfigJSON struct {
	HiddenSize            int      `json:"hidden_size"`
	NumAttentionHeads     int      `json:"num_attention_heads"`
	NumKeyValueHeads      int      `json:"num_key_value_heads"`
	NumHiddenLayers       int      `json:"num_hidden_layers"`
	MaxPositionEmbeddings int      `json:"max_position_embeddings"`
	TorchDtype            string   `json:"torch_dtype"`
	ModelType             string   `json:"model_type"`
	Architectures         []string `json:"architectures"`
	VocabSize             int      `json:"vocab_size"`
	IntermediateSize      int      `json:"intermediate_size"`

	// Transformers version required by this model (e.g., "4.45.0", "5.3.0")
	TransformersVersion string `json:"transformers_version"`

	// Sliding window attention (Mistral, Mixtral, etc.)
	// If set, KV cache is capped at this size instead of max_position_embeddings.
	SlidingWindow *int `json:"sliding_window"`

	// MoE fields (DeepSeek, Mixtral, etc.)
	NRoutedExperts      int `json:"n_routed_experts"`
	NSharedExperts      int `json:"n_shared_experts"`
	MoeIntermediateSize int `json:"moe_intermediate_size"`
	FirstKDenseReplace  int `json:"first_k_dense_replace"`
	// Mixtral-style MoE
	NumLocalExperts int `json:"num_local_experts"`

	// Multimodal models (Gemma 4, LLaVA, etc.) nest LLM config under text_config
	TextConfig *hfConfigJSON `json:"text_config"`

	// Pre-quantized models (GPTQ, AWQ, NVFP4, bitsandbytes)
	QuantizationConfig *hfQuantizationConfig `json:"quantization_config"`
}

// FetchModelConfig fetches model metadata from HuggingFace and returns a
// ModelConfig. It makes two parallel requests: one for safetensors metadata
// and one for config.json.
func (c *HFClient) FetchModelConfig(modelID, hfToken string) (*ModelConfig, error) {
	type result struct {
		model  *hfModelResponse
		config *hfConfigJSON
		err    error
	}

	modelCh := make(chan result, 1)
	configCh := make(chan result, 1)

	// Fetch safetensors metadata.
	go func() {
		url := fmt.Sprintf("%s/api/models/%s?expand[]=safetensors", c.baseURL, modelID)
		var resp hfModelResponse
		if err := c.doGet(url, hfToken, &resp); err != nil {
			modelCh <- result{err: fmt.Errorf("fetch model info: %w", err)}
			return
		}
		modelCh <- result{model: &resp}
	}()

	// Fetch config.json.
	go func() {
		url := fmt.Sprintf("%s/%s/resolve/main/config.json", c.baseURL, modelID)
		var cfg hfConfigJSON
		if err := c.doGet(url, hfToken, &cfg); err != nil {
			configCh <- result{err: fmt.Errorf("fetch config.json: %w", err)}
			return
		}
		configCh <- result{config: &cfg}
	}()

	mr := <-modelCh
	cr := <-configCh

	if mr.err != nil {
		return nil, mr.err
	}
	if cr.err != nil {
		// If the model API succeeded and the model is gated, give a clear message.
		if mr.model != nil && isGated(mr.model.Gated) {
			return nil, &HFError{
				StatusCode: http.StatusForbidden,
				Message:    "This model is gated on HuggingFace. Provide an HF token with access above and try again.",
			}
		}
		return nil, cr.err
	}

	// For multimodal models (Gemma 4, LLaVA, etc.), LLM config is nested under text_config
	srcCfg := cr.config
	if srcCfg.TextConfig != nil && srcCfg.HiddenSize == 0 {
		srcCfg = srcCfg.TextConfig
	}

	cfg := &ModelConfig{
		HiddenSize:            srcCfg.HiddenSize,
		NumAttentionHeads:     srcCfg.NumAttentionHeads,
		NumKeyValueHeads:      srcCfg.NumKeyValueHeads,
		NumHiddenLayers:       srcCfg.NumHiddenLayers,
		MaxPositionEmbeddings: srcCfg.MaxPositionEmbeddings,
		TorchDtype:            srcCfg.TorchDtype,
		ModelType:             cr.config.ModelType,             // Keep top-level model_type
		TransformersVersion:   cr.config.TransformersVersion,   // Keep top-level transformers_version
		PipelineTag:           mr.model.PipelineTag,
		Architectures:         cr.config.Architectures,
	}
	// Fall back to the architectures advertised on /api/models if config.json
	// didn't declare them (rare but happens on some older repos).
	if len(cfg.Architectures) == 0 && mr.model.Config != nil {
		cfg.Architectures = mr.model.Config.Architectures
	}

	// Sliding window attention (Mistral, Mixtral, etc.)
	if srcCfg.SlidingWindow != nil && *srcCfg.SlidingWindow > 0 {
		cfg.SlidingWindow = *srcCfg.SlidingWindow
	}

	if mr.model.Safetensors != nil && mr.model.Safetensors.Total > 0 {
		cfg.ParameterCount = mr.model.Safetensors.Total
		// Calculate actual memory from dtype breakdown (more accurate for mixed-precision models)
		cfg.ActualMemoryBytes = calculateMemoryFromDtypes(mr.model.Safetensors.Parameters)
	}
	if cfg.ParameterCount == 0 {
		// Safetensors metadata unavailable (common for MoE models like
		// DeepSeek-V3). Estimate from architecture config.
		cfg.ParameterCount = estimateParameterCount(srcCfg)
	}
	if mr.model.Config != nil && cfg.ModelType == "" {
		cfg.ModelType = mr.model.Config.ModelType
	}

	// Default num_key_value_heads to num_attention_heads if not set (non-GQA models).
	if cfg.NumKeyValueHeads == 0 {
		cfg.NumKeyValueHeads = cfg.NumAttentionHeads
	}

	// Extract pre-quantization info (GPTQ, AWQ, NVFP4, bitsandbytes, etc.)
	// Check top-level config first, then text_config for multimodal models
	qcfg := cr.config.QuantizationConfig
	if qcfg == nil && srcCfg.QuantizationConfig != nil {
		qcfg = srcCfg.QuantizationConfig
	}
	if qcfg != nil {
		cfg.PreQuantized = true
		cfg.PreQuantMethod = qcfg.QuantMethod
		cfg.PreQuantBits = extractQuantBits(qcfg)
	}

	return cfg, nil
}

// extractQuantBits determines the bit width from a quantization config.
func extractQuantBits(qcfg *hfQuantizationConfig) int {
	// Direct bits field (GPTQ, AWQ)
	if qcfg.Bits > 0 {
		return qcfg.Bits
	}

	// ModelOpt (NVFP4, etc.) - check config_groups
	for _, group := range qcfg.ConfigGroups {
		if group.Weights != nil && group.Weights.NumBits > 0 {
			return group.Weights.NumBits
		}
	}

	// bitsandbytes
	if qcfg.LoadIn4Bit {
		return 4
	}
	if qcfg.LoadIn8Bit {
		return 8
	}

	// Default to 4-bit if we can't determine
	return 4
}

// calculateMemoryFromDtypes calculates actual model memory in bytes from safetensors dtype breakdown.
// This is more accurate than ParameterCount * bytesPerDtype for mixed-precision models.
func calculateMemoryFromDtypes(dtypes map[string]int64) int64 {
	if len(dtypes) == 0 {
		return 0
	}

	var totalBytes int64
	for dtype, count := range dtypes {
		bytesPerParam := dtypeBytesPerParam(dtype)
		totalBytes += int64(float64(count) * bytesPerParam)
	}
	return totalBytes
}

// dtypeBytesPerParam returns bytes per parameter for a given dtype string from HuggingFace.
func dtypeBytesPerParam(dtype string) float64 {
	switch dtype {
	// 32-bit formats
	case "F32", "FP32", "float32":
		return 4.0
	// 16-bit formats
	case "F16", "FP16", "float16", "BF16", "bfloat16":
		return 2.0
	// 8-bit formats
	case "F8_E4M3", "F8_E5M2", "FP8", "INT8", "I8", "U8", "int8", "uint8":
		return 1.0
	// 4-bit formats
	case "INT4", "I4", "U4", "int4", "uint4", "NF4", "FP4":
		return 0.5
	default:
		// Unknown dtype - assume 2 bytes (FP16) as safe default
		return 2.0
	}
}

func (c *HFClient) doGet(url, hfToken string, out any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+hfToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &HFError{StatusCode: resp.StatusCode, Message: "model is gated — provide an HF token with access"}
	}
	if resp.StatusCode == http.StatusNotFound {
		msg := "Model not found on HuggingFace."
		if hfToken == "" {
			msg += " If this is a private or gated model, provide an HF token above and try again."
		}
		return &HFError{StatusCode: resp.StatusCode, Message: msg}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &HFError{StatusCode: resp.StatusCode, Message: string(body)}
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// estimateParameterCount estimates total parameters from architecture fields
// in config.json. This is used as a fallback when HuggingFace safetensors
// metadata is missing (common for MoE models like DeepSeek-V3).
func estimateParameterCount(cfg *hfConfigJSON) int64 {
	if cfg.HiddenSize == 0 || cfg.NumHiddenLayers == 0 {
		return 0
	}

	h := int64(cfg.HiddenSize)
	layers := int64(cfg.NumHiddenLayers)
	vocab := int64(cfg.VocabSize)
	interSize := int64(cfg.IntermediateSize)

	// Embeddings + LM head.
	var total int64
	if vocab > 0 {
		total += 2 * vocab * h
	}

	// Per-layer attention: Q, K, V, O projections ≈ 4 × hidden_size².
	attnPerLayer := 4 * h * h

	// Per-layer layer norms (small).
	normPerLayer := 2 * h

	// Determine number of MoE experts (support both DeepSeek and Mixtral field names).
	numExperts := cfg.NRoutedExperts
	if numExperts == 0 {
		numExperts = cfg.NumLocalExperts
	}

	moeInterSize := int64(cfg.MoeIntermediateSize)
	if moeInterSize == 0 {
		moeInterSize = interSize // Mixtral uses intermediate_size for experts
	}

	if numExperts > 0 && moeInterSize > 0 {
		// MoE model. Some layers may be dense (first_k_dense_replace).
		denseLayers := int64(cfg.FirstKDenseReplace)
		moeLayers := layers - denseLayers
		if moeLayers < 0 {
			moeLayers = layers
		}

		// Dense FFN: gate + up + down = 3 × h × intermediate_size
		denseFFN := int64(3) * h * interSize

		// MoE FFN: routed experts + shared experts
		routedFFN := int64(numExperts) * 3 * h * moeInterSize
		sharedFFN := int64(cfg.NSharedExperts) * 3 * h * interSize
		moeFFN := routedFFN + sharedFFN

		total += denseLayers * (attnPerLayer + denseFFN + normPerLayer)
		total += moeLayers * (attnPerLayer + moeFFN + normPerLayer)
	} else if interSize > 0 {
		// Dense model: gate + up + down = 3 × h × intermediate_size
		ffnPerLayer := int64(3) * h * interSize
		total += layers * (attnPerLayer + ffnPerLayer + normPerLayer)
	} else {
		// No intermediate_size — rough estimate: ~12 × h² per layer.
		total += layers * 12 * h * h
	}

	return total
}

// isGated returns true if the HuggingFace gated field indicates the model is gated.
// The field is false for public models, or a string like "auto" or "manual" for gated models.
func isGated(v any) bool {
	switch g := v.(type) {
	case bool:
		return g
	case string:
		return g != "" && g != "false"
	default:
		return false
	}
}

// HFError represents an error from the HuggingFace API.
type HFError struct {
	StatusCode int
	Message    string
}

func (e *HFError) Error() string {
	return fmt.Sprintf("huggingface API %d: %s", e.StatusCode, e.Message)
}
