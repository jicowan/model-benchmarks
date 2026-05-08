package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/accelbench/accelbench/internal/recommend"
)

// EstimateFilters holds optional filters for the estimate endpoint.
type EstimateFilters struct {
	AcceleratorType  string  // "gpu", "neuron", or "" for all
	MaxCostHourly    float64 // 0 means no limit
	MinContextLength int     // 0 means no minimum
	Quantization     string  // force specific quantization
	Region           string  // for pricing lookup
}

// EstimateResponse is the response from GET /api/v1/estimate.
type EstimateResponse struct {
	ModelInfo ModelInfoResponse   `json:"model_info"`
	Estimates []EstimateRow       `json:"estimates"`
	Summary   EstimateSummary     `json:"summary"`
}

// ModelInfoResponse summarizes the model metadata.
type ModelInfoResponse struct {
	HfID                  string `json:"hf_id"`
	ParameterCount        int64  `json:"parameter_count"`
	NativeDtype           string `json:"native_dtype"`
	MaxPositionEmbeddings int    `json:"max_position_embeddings"`
	Architecture          string `json:"architecture"`
	NumAttentionHeads     int    `json:"num_attention_heads"`
	NumKVHeads            int    `json:"num_kv_heads"`
}

// EstimateRow represents one instance type's estimate.
type EstimateRow struct {
	InstanceType         string          `json:"instance_type"`
	AcceleratorType      string          `json:"accelerator_type"`
	AcceleratorName      string          `json:"accelerator_name"`
	AcceleratorCount     int             `json:"accelerator_count"`
	Feasible             bool            `json:"feasible"`
	RequiresQuantization bool            `json:"requires_quantization"`
	Config               *EstimateConfig `json:"config,omitempty"`
	Memory               *MemoryEstimate `json:"memory,omitempty"`
	Cost                 *CostEstimate   `json:"cost,omitempty"`
	Explanation          string          `json:"explanation,omitempty"`
	HasBenchmarkData     bool            `json:"has_benchmark_data"`
}

// EstimateConfig holds the recommended vLLM configuration.
type EstimateConfig struct {
	TensorParallelDegree int     `json:"tensor_parallel_degree"`
	Quantization         *string `json:"quantization"`
	MaxModelLen          int     `json:"max_model_len"`
	Concurrency          int     `json:"concurrency"`
	InputSequenceLength  int     `json:"input_sequence_length"`
	OutputSequenceLength int     `json:"output_sequence_length"`
	// PRD-46: scheduler knobs from the recommender so the Estimate →
	// Run handoff pre-fills them on the New Benchmark form.
	MaxNumBatchedTokens int    `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype        string `json:"kv_cache_dtype,omitempty"`
}

// MemoryEstimate shows memory breakdown.
type MemoryEstimate struct {
	ModelWeightsGiB float64 `json:"model_weights_gib"`
	AvailableGiB    float64 `json:"available_gib"`
	UtilizationPct  float64 `json:"utilization_pct"`
}

// CostEstimate shows pricing info.
type CostEstimate struct {
	HourlyUSD float64 `json:"hourly_usd"`
}

// EstimateSummary provides aggregate stats.
type EstimateSummary struct {
	TotalEvaluated    int    `json:"total_evaluated"`
	FeasibleNative    int    `json:"feasible_native"`
	FeasibleQuantized int    `json:"feasible_quantized"`
	Infeasible        int    `json:"infeasible"`
	CheapestFeasible  string `json:"cheapest_feasible,omitempty"`
	MostHeadroom      string `json:"most_headroom,omitempty"`
}

func (s *Server) handleEstimate(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model")
	if modelID == "" {
		writeError(w, http.StatusBadRequest, "model query parameter is required")
		return
	}

	// Parse filters
	filters := EstimateFilters{
		AcceleratorType: strings.ToLower(r.URL.Query().Get("accelerator_type")),
		Region:          r.URL.Query().Get("region"),
		Quantization:    r.URL.Query().Get("quantization"),
	}
	if filters.Region == "" {
		filters.Region = "us-east-2"
	}
	if v := r.URL.Query().Get("max_cost_hourly"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			filters.MaxCostHourly = f
		}
	}
	if v := r.URL.Query().Get("min_context_length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filters.MinContextLength = n
		}
	}

	hfToken := r.Header.Get("X-HF-Token")
	ctx := r.Context()

	// Fetch model config (from S3 cache if available, else HuggingFace).
	modelCfg, err := s.FetchModelConfig(ctx, modelID, hfToken)
	if err != nil {
		var hfErr *recommend.HFError
		if errors.As(err, &hfErr) {
			writeError(w, hfErr.StatusCode, hfErr.Message)
			return
		}
		writeError(w, http.StatusBadGateway, "failed to fetch model metadata from HuggingFace")
		return
	}

	// Get all instance types
	instances, err := s.repo.ListInstanceTypes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list instance types")
		return
	}

	// Get pricing
	pricing, err := s.repo.ListPricing(ctx, filters.Region)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch pricing")
		return
	}
	priceMap := make(map[string]float64)
	for _, p := range pricing {
		priceMap[p.InstanceTypeName] = p.OnDemandHourlyUSD
	}

	// Build instance specs for alternatives lookup
	var allSpecs []recommend.InstanceSpec
	for _, it := range instances {
		allSpecs = append(allSpecs, recommend.InstanceSpec{
			Name:                 it.Name,
			AcceleratorType:      it.AcceleratorType,
			AcceleratorName:      it.AcceleratorName,
			AcceleratorCount:     it.AcceleratorCount,
			AcceleratorMemoryGiB: it.AcceleratorMemoryGiB,
			MemoryGiB:            it.MemoryGiB,
		})
	}

	// Tag the transformers-compat warning with the configured vLLM version.
	var recOpts recommend.RecommendOptions
	if tv, err := s.repo.GetToolVersions(r.Context()); err == nil && tv != nil {
		recOpts.VLLMVersion = tv.FrameworkVersion
	}

	// If the model is cached in S3, every row in the estimate uses the
	// Run:ai streamer path, which lowers the host-RAM peak used by the
	// recommender's host-memory feasibility check.
	if mc, _ := s.repo.GetModelCacheByHfID(r.Context(), modelID, "main"); mc != nil && mc.Status == "cached" {
		recOpts.UseS3Streamer = true
	}

	// PRD-47 PR #5: apply per-family host-memory calibration. Model
	// type is derived from the HF config's model_type field (populated
	// just below once FetchModelConfig returns).
	if calib, err := s.repo.GetHostMemCalibration(r.Context()); err == nil {
		recOpts.HostMemCalibration = calib
	}
	if modelCfg != nil {
		recOpts.ModelType = modelCfg.ModelType
	}

	// Generate estimates for each instance type
	var estimates []EstimateRow
	var feasibleNative, feasibleQuantized, infeasible int
	var cheapestFeasible string
	var cheapestPrice float64 = -1
	var mostHeadroom string
	var lowestUtilization float64 = 101

	for _, it := range instances {
		// Apply accelerator type filter
		if filters.AcceleratorType != "" && filters.AcceleratorType != "all" {
			if strings.ToLower(it.AcceleratorType) != filters.AcceleratorType {
				continue
			}
		}

		// Apply cost filter
		price, hasPrice := priceMap[it.Name]
		if filters.MaxCostHourly > 0 && hasPrice && price > filters.MaxCostHourly {
			continue
		}

		inst := recommend.InstanceSpec{
			Name:                 it.Name,
			AcceleratorType:      it.AcceleratorType,
			AcceleratorName:      it.AcceleratorName,
			AcceleratorCount:     it.AcceleratorCount,
			AcceleratorMemoryGiB: it.AcceleratorMemoryGiB,
			MemoryGiB:            it.MemoryGiB,
		}

		// Call appropriate recommendation function
		var rec *recommend.Recommendation
		if strings.EqualFold(it.AcceleratorType, "neuron") {
			rec = recommend.RecommendNeuron(*modelCfg, inst)
		} else {
			rec = recommend.Recommend(*modelCfg, inst, allSpecs, recOpts)
		}

		row := EstimateRow{
			InstanceType:     it.Name,
			AcceleratorType:  it.AcceleratorType,
			AcceleratorName:  it.AcceleratorName,
			AcceleratorCount: it.AcceleratorCount,
			Feasible:         rec.Explanation.Feasible,
		}

		if rec.Explanation.Feasible {
			row.RequiresQuantization = rec.Quantization != nil
			row.Config = &EstimateConfig{
				TensorParallelDegree: rec.TensorParallelDegree,
				Quantization:         rec.Quantization,
				MaxModelLen:          rec.MaxModelLen,
				Concurrency:          rec.Concurrency,
				InputSequenceLength:  rec.InputSequenceLength,
				OutputSequenceLength: rec.OutputSequenceLength,
				MaxNumBatchedTokens:  rec.MaxNumBatchedTokens,
				KVCacheDtype:         rec.KVCacheDtype,
			}

			// Apply min context filter
			if filters.MinContextLength > 0 && rec.MaxModelLen < filters.MinContextLength {
				continue
			}

			// Calculate memory utilization
			modelWeightsGiB := float64(modelCfg.ParameterCount) * 2 / (1024 * 1024 * 1024) // BF16
			if rec.Quantization != nil {
				switch *rec.Quantization {
				case "fp8", "int8":
					modelWeightsGiB = float64(modelCfg.ParameterCount) / (1024 * 1024 * 1024)
				case "int4":
					modelWeightsGiB = float64(modelCfg.ParameterCount) * 0.5 / (1024 * 1024 * 1024)
				}
			}
			availableGiB := float64(it.AcceleratorMemoryGiB)
			utilizationPct := (modelWeightsGiB / availableGiB) * 100

			row.Memory = &MemoryEstimate{
				ModelWeightsGiB: modelWeightsGiB,
				AvailableGiB:    availableGiB,
				UtilizationPct:  utilizationPct,
			}

			if hasPrice && price > 0 {
				row.Cost = &CostEstimate{HourlyUSD: price}
			}

			// Track cheapest feasible (ignore $0 prices)
			if hasPrice && price > 0 && (cheapestPrice < 0 || price < cheapestPrice) {
				cheapestPrice = price
				cheapestFeasible = it.Name
			}

			// Track most headroom (lowest utilization)
			if utilizationPct < lowestUtilization {
				lowestUtilization = utilizationPct
				mostHeadroom = it.Name
			}

			if row.RequiresQuantization {
				feasibleQuantized++
			} else {
				feasibleNative++
			}
		} else {
			row.Explanation = rec.Explanation.Reason
			infeasible++
		}

		estimates = append(estimates, row)
	}

	// Sort by cost (feasible first, then by price)
	sort.Slice(estimates, func(i, j int) bool {
		// Feasible before infeasible
		if estimates[i].Feasible != estimates[j].Feasible {
			return estimates[i].Feasible
		}
		// Native before quantized
		if estimates[i].Feasible && estimates[j].Feasible {
			if estimates[i].RequiresQuantization != estimates[j].RequiresQuantization {
				return !estimates[i].RequiresQuantization
			}
		}
		// Then by cost
		if estimates[i].Cost != nil && estimates[j].Cost != nil {
			return estimates[i].Cost.HourlyUSD < estimates[j].Cost.HourlyUSD
		}
		return estimates[i].Cost != nil
	})

	resp := EstimateResponse{
		ModelInfo: ModelInfoResponse{
			HfID:                  modelID,
			ParameterCount:        modelCfg.ParameterCount,
			NativeDtype:           modelCfg.TorchDtype,
			MaxPositionEmbeddings: modelCfg.MaxPositionEmbeddings,
			Architecture:          modelCfg.ModelType,
			NumAttentionHeads:     modelCfg.NumAttentionHeads,
			NumKVHeads:            modelCfg.NumKeyValueHeads,
		},
		Estimates: estimates,
		Summary: EstimateSummary{
			TotalEvaluated:    len(estimates),
			FeasibleNative:    feasibleNative,
			FeasibleQuantized: feasibleQuantized,
			Infeasible:        infeasible,
			CheapestFeasible:  cheapestFeasible,
			MostHeadroom:      mostHeadroom,
		},
	}

	writeJSON(w, http.StatusOK, resp)
}
