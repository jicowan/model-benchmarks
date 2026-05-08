package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/accelbench/accelbench/internal/recommend"
)

// MemoryBreakdownRequest holds parameters for memory breakdown calculation.
type MemoryBreakdownRequest struct {
	ModelID      string  `json:"model_id"`
	InstanceType string  `json:"instance_type"`
	TP           int     `json:"tensor_parallel_degree"`
	Quantization string  `json:"quantization"`
	MaxModelLen  int     `json:"max_model_len"`
	Concurrency  int     `json:"concurrency"`
	OverheadGiB  float64 `json:"overhead_gib"`
}

// MemoryBreakdownResponse includes detailed memory breakdown.
type MemoryBreakdownResponse struct {
	recommend.MemoryBreakdown
	WarningMessage string `json:"warning_message,omitempty"`
}

// handleMemoryBreakdown returns a detailed memory breakdown for given configuration.
func (s *Server) handleMemoryBreakdown(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	modelID := q.Get("model")
	instanceName := q.Get("instance_type")

	if modelID == "" || instanceName == "" {
		writeError(w, http.StatusBadRequest, "model and instance_type are required")
		return
	}

	hfToken := r.Header.Get("X-HF-Token")

	// Parse optional parameters
	var tp, maxModelLen, concurrency, inputSeqLen, outputSeqLen int
	var overheadGiB float64
	var quant string

	fmt.Sscanf(q.Get("tp"), "%d", &tp)
	fmt.Sscanf(q.Get("max_model_len"), "%d", &maxModelLen)
	fmt.Sscanf(q.Get("concurrency"), "%d", &concurrency)
	fmt.Sscanf(q.Get("input_seq_len"), "%d", &inputSeqLen)
	fmt.Sscanf(q.Get("output_seq_len"), "%d", &outputSeqLen)
	fmt.Sscanf(q.Get("overhead_gib"), "%f", &overheadGiB)
	quant = q.Get("quantization")
	kvCacheDtype := q.Get("kv_cache_dtype")

	// Fetch model config (from S3 cache if available, else HuggingFace).
	modelCfg, err := s.FetchModelConfig(r.Context(), modelID, hfToken)
	if err != nil {
		var hfErr *recommend.HFError
		if errors.As(err, &hfErr) {
			writeError(w, hfErr.StatusCode, hfErr.Message)
			return
		}
		writeError(w, http.StatusBadGateway, "failed to fetch model metadata")
		return
	}

	// Fetch instance spec
	instType, err := s.repo.GetInstanceTypeByName(r.Context(), instanceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "instance lookup failed")
		return
	}
	if instType == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("instance type %s not found", instanceName))
		return
	}

	// Use defaults if not specified (matching Recommend() defaults)
	if tp <= 0 {
		tp = 1
	}
	if maxModelLen <= 0 {
		maxModelLen = modelCfg.MaxPositionEmbeddings
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if inputSeqLen <= 0 {
		inputSeqLen = 512 // Default matches Recommend()
	}
	if outputSeqLen <= 0 {
		outputSeqLen = 256 // Default matches Recommend()
	}
	if quant == "" {
		if modelCfg.TorchDtype != "" {
			quant = modelCfg.TorchDtype
		} else {
			quant = "bfloat16"
		}
	}

	// If the caller didn't specify a KV dtype, mirror the recommender's
	// auto behavior: fp8 on FP8-capable GPUs (H100/H200/L40S), else fp16.
	// Without this the breakdown would over-count KV cache by 2× on
	// Hopper/Ada instances where PRD-46 actually ships fp8.
	if kvCacheDtype == "" && recommend.SupportsFP8KVCache(instType.AcceleratorName) {
		kvCacheDtype = "fp8"
	}

	// Calculate KV cache per token — shared with the recommender so the
	// breakdown and the feasibility check agree on what a KV slot costs.
	kvPerToken := recommend.KVCachePerTokenBytes(*modelCfg, kvCacheDtype)

	// Calculate overhead
	var overheadBytes float64
	if overheadGiB > 0 {
		overheadBytes = overheadGiB * 1024 * 1024 * 1024
	} else {
		overheadBytes = recommend.DefaultOverheadGiB(*modelCfg) * 1024 * 1024 * 1024
	}

	perDeviceGiB := float64(instType.AcceleratorMemoryGiB) / float64(instType.AcceleratorCount)

	breakdown := recommend.CalculateMemoryBreakdown(
		modelCfg.ParameterCount,
		quant,
		kvPerToken,
		maxModelLen,
		inputSeqLen,
		outputSeqLen,
		concurrency,
		overheadBytes,
		tp,
		perDeviceGiB,
	)

	resp := MemoryBreakdownResponse{
		MemoryBreakdown: breakdown,
	}

	// Add warning if memory usage is high
	if breakdown.HeadroomGiB < 1.0 {
		resp.WarningMessage = fmt.Sprintf("Low memory headroom (%.1f GiB). Consider reducing concurrency or max_model_len.", breakdown.HeadroomGiB)
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleOOMHistory returns OOM events for a model+instance combination.
func (s *Server) handleOOMHistory(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model")
	instanceType := r.URL.Query().Get("instance_type")

	if modelID == "" || instanceType == "" {
		writeError(w, http.StatusBadRequest, "model and instance_type are required")
		return
	}

	var limit int
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	if limit <= 0 {
		limit = 10
	}

	history, err := s.repo.GetOOMHistory(r.Context(), modelID, instanceType, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch OOM history")
		return
	}

	writeJSON(w, http.StatusOK, history)
}
