package recommend

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

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
		ModelType string `json:"model_type"`
	} `json:"config"`
	// Gated is false for public models, or "auto"/"manual" for gated models.
	Gated any `json:"gated"`
}

// hfConfigJSON is the subset of a model's config.json we need.
type hfConfigJSON struct {
	HiddenSize            int    `json:"hidden_size"`
	NumAttentionHeads     int    `json:"num_attention_heads"`
	NumKeyValueHeads      int    `json:"num_key_value_heads"`
	NumHiddenLayers       int    `json:"num_hidden_layers"`
	MaxPositionEmbeddings int    `json:"max_position_embeddings"`
	TorchDtype            string `json:"torch_dtype"`
	ModelType             string `json:"model_type"`
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

	cfg := &ModelConfig{
		HiddenSize:            cr.config.HiddenSize,
		NumAttentionHeads:     cr.config.NumAttentionHeads,
		NumKeyValueHeads:      cr.config.NumKeyValueHeads,
		NumHiddenLayers:       cr.config.NumHiddenLayers,
		MaxPositionEmbeddings: cr.config.MaxPositionEmbeddings,
		TorchDtype:            cr.config.TorchDtype,
		ModelType:             cr.config.ModelType,
	}

	if mr.model.Safetensors != nil {
		cfg.ParameterCount = mr.model.Safetensors.Total
	}
	if mr.model.Config != nil && cfg.ModelType == "" {
		cfg.ModelType = mr.model.Config.ModelType
	}

	// Default num_key_value_heads to num_attention_heads if not set (non-GQA models).
	if cfg.NumKeyValueHeads == 0 {
		cfg.NumKeyValueHeads = cfg.NumAttentionHeads
	}

	return cfg, nil
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
