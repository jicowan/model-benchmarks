package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/accelbench/accelbench/internal/database"
)

// LoadgenOutput represents the JSON output from the load generator.
type LoadgenOutput struct {
	Requests []RequestResult `json:"requests"`
	Summary  Summary         `json:"summary"`
}

// RequestResult holds per-request measurements from the load generator.
// Supports both legacy AccelBench loadgen and inference-perf output formats.
type RequestResult struct {
	TTFTMs          float64 `json:"ttft_ms"`
	E2ELatencyMs    float64 `json:"e2e_latency_ms"`
	ITLMs           float64 `json:"itl_ms"`            // legacy loadgen
	TPOTMs          float64 `json:"tpot_ms"`           // inference-perf (time per output token)
	OutputTokens    int     `json:"output_tokens"`
	InputTokens     int     `json:"input_tokens"`
	DurationSeconds float64 `json:"duration_seconds"`
	Success         bool    `json:"success"`

	// Extended latency breakdown (PRD-14)
	PrefillTimeMs float64 `json:"prefill_time_ms"`
	DecodeTimeMs  float64 `json:"decode_time_ms"`
	QueueTimeMs   float64 `json:"queue_time_ms"`
}

// Summary holds aggregate metrics from the load generator.
// Supports both legacy AccelBench loadgen and inference-perf output formats.
type Summary struct {
	// Legacy loadgen fields
	TotalDurationSeconds   float64 `json:"total_duration_seconds"`
	ThroughputAggregateTPS float64 `json:"throughput_aggregate_tps"`

	// inference-perf fields
	TotalDurationS float64 `json:"total_duration_s"`
	ThroughputTPS  float64 `json:"throughput_tps"`

	// Common fields
	TotalRequests      int     `json:"total_requests"`
	SuccessfulRequests int     `json:"successful_requests"`
	FailedRequests     int     `json:"failed_requests"`
	RequestsPerSecond  float64 `json:"requests_per_second"`

	// Optional accelerator metrics (from legacy loadgen)
	AcceleratorUtilizationPct *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorMemoryPeakGiB  *float64 `json:"accelerator_memory_peak_gib,omitempty"`
}

// ParseLoadgenOutput parses the JSON output from a load generator pod.
// With S3 storage, the JSON is read directly from a file without truncation.
// Falls back to log parsing for backward compatibility.
func ParseLoadgenOutput(data []byte) (*LoadgenOutput, error) {
	var out LoadgenOutput

	// Strategy 1: Try direct JSON unmarshal (S3 case - clean JSON)
	if err := json.Unmarshal(data, &out); err == nil && len(out.Requests) > 0 {
		return &out, nil
	}

	// Strategy 2: Look for marker-delimited JSON (log parsing fallback)
	beginMarker := []byte("ACCELBENCH_JSON_BEGIN")
	endMarker := []byte("ACCELBENCH_JSON_END")
	if beginIdx := bytes.Index(data, beginMarker); beginIdx >= 0 {
		rest := data[beginIdx+len(beginMarker):]
		if endIdx := bytes.Index(rest, endMarker); endIdx >= 0 {
			jsonData := bytes.TrimSpace(rest[:endIdx])
			if err := json.Unmarshal(jsonData, &out); err == nil && len(out.Requests) > 0 {
				return &out, nil
			}
		}
	}

	// Strategy 3: Scan line-by-line for a JSON payload
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal(line, &out); err == nil && len(out.Requests) > 0 {
			return &out, nil
		}
	}

	return nil, fmt.Errorf("no valid JSON payload found in %d bytes of output", len(data))
}

// ComputeMetrics takes parsed loadgen output and computes the full set of
// benchmark metrics including p50/p90/p95/p99 percentiles.
// Supports both legacy AccelBench loadgen and inference-perf output formats.
func ComputeMetrics(out *LoadgenOutput) *database.BenchmarkMetrics {
	successful := filterSuccessful(out.Requests)

	var ttfts, e2es, itls []float64
	var tpots, prefills, decodes, queues []float64
	var totalOutputTokens int
	for _, r := range successful {
		ttfts = append(ttfts, r.TTFTMs)
		e2es = append(e2es, r.E2ELatencyMs)
		// Use ITL if available, otherwise use TPOT (inference-perf)
		itl := r.ITLMs
		if itl == 0 && r.TPOTMs > 0 {
			itl = r.TPOTMs
		}
		itls = append(itls, itl)
		totalOutputTokens += r.OutputTokens

		// Extended latency breakdown (only include if > 0)
		if r.TPOTMs > 0 {
			tpots = append(tpots, r.TPOTMs)
		}
		if r.PrefillTimeMs > 0 {
			prefills = append(prefills, r.PrefillTimeMs)
		}
		if r.DecodeTimeMs > 0 {
			decodes = append(decodes, r.DecodeTimeMs)
		}
		if r.QueueTimeMs > 0 {
			queues = append(queues, r.QueueTimeMs)
		}
	}

	ttftP50, ttftP90, ttftP95, ttftP99 := percentiles(ttfts)
	e2eP50, e2eP90, e2eP95, e2eP99 := percentiles(e2es)
	itlP50, itlP90, itlP95, itlP99 := percentiles(itls)

	// Extended latency percentiles
	tpotP50, tpotP90, _, tpotP99 := percentiles(tpots)
	prefillP50, _, _, _ := percentiles(prefills)
	decodeP50, _, _, _ := percentiles(decodes)
	queueP50, _, _, _ := percentiles(queues)

	// Per-request throughput: average output tokens / average duration.
	var throughputPerRequest *float64
	if len(successful) > 0 {
		avgTokens := float64(totalOutputTokens) / float64(len(successful))
		avgDur := mean(extractField(successful, func(r RequestResult) float64 { return r.DurationSeconds }))
		if avgDur > 0 {
			v := avgTokens / avgDur
			throughputPerRequest = &v
		}
	}

	// Handle both field naming conventions for aggregate TPS
	aggTPSVal := out.Summary.ThroughputAggregateTPS
	if aggTPSVal == 0 {
		aggTPSVal = out.Summary.ThroughputTPS // inference-perf field
	}
	aggTPS := &aggTPSVal

	rps := &out.Summary.RequestsPerSecond

	// Handle both field naming conventions for duration
	durVal := out.Summary.TotalDurationSeconds
	if durVal == 0 {
		durVal = out.Summary.TotalDurationS // inference-perf field
	}
	dur := &durVal

	successCount := out.Summary.SuccessfulRequests
	failCount := out.Summary.FailedRequests

	return &database.BenchmarkMetrics{
		TTFTP50Ms:               ttftP50,
		TTFTP90Ms:               ttftP90,
		TTFTP95Ms:               ttftP95,
		TTFTP99Ms:               ttftP99,
		E2ELatencyP50Ms:         e2eP50,
		E2ELatencyP90Ms:         e2eP90,
		E2ELatencyP95Ms:         e2eP95,
		E2ELatencyP99Ms:         e2eP99,
		ITLP50Ms:                itlP50,
		ITLP90Ms:                itlP90,
		ITLP95Ms:                itlP95,
		ITLP99Ms:                itlP99,
		TPOTP50Ms:               tpotP50,
		TPOTP90Ms:               tpotP90,
		TPOTP99Ms:               tpotP99,
		PrefillTimeP50Ms:        prefillP50,
		DecodeTimeP50Ms:         decodeP50,
		QueueTimeP50Ms:          queueP50,
		ThroughputPerRequestTPS: throughputPerRequest,
		ThroughputAggregateTPS:  aggTPS,
		RequestsPerSecond:       rps,
		AcceleratorUtilizationPct: out.Summary.AcceleratorUtilizationPct,
		AcceleratorMemoryPeakGiB:  out.Summary.AcceleratorMemoryPeakGiB,
		SuccessfulRequests:        &successCount,
		FailedRequests:            &failCount,
		TotalDurationSeconds:      dur,
	}
}

// percentiles computes p50, p90, p95, p99 from a slice of float64 values.
// Returns nil pointers if the slice is empty.
func percentiles(vals []float64) (p50, p90, p95, p99 *float64) {
	if len(vals) == 0 {
		return nil, nil, nil, nil
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)

	p50v := percentile(sorted, 50)
	p90v := percentile(sorted, 90)
	p95v := percentile(sorted, 95)
	p99v := percentile(sorted, 99)
	return &p50v, &p90v, &p95v, &p99v
}

// percentile computes the p-th percentile from a sorted slice using
// the nearest-rank method.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p / 100.0) * float64(len(sorted))
	idx := int(math.Ceil(rank)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func filterSuccessful(results []RequestResult) []RequestResult {
	var out []RequestResult
	for _, r := range results {
		if r.Success {
			out = append(out, r)
		}
	}
	return out
}

func extractField(results []RequestResult, f func(RequestResult) float64) []float64 {
	vals := make([]float64, len(results))
	for i, r := range results {
		vals[i] = f(r)
	}
	return vals
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
