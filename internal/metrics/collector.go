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
type RequestResult struct {
	TTFTMs          float64 `json:"ttft_ms"`
	E2ELatencyMs    float64 `json:"e2e_latency_ms"`
	ITLMs           float64 `json:"itl_ms"`
	OutputTokens    int     `json:"output_tokens"`
	InputTokens     int     `json:"input_tokens"`
	DurationSeconds float64 `json:"duration_seconds"`
	Success         bool    `json:"success"`
}

// Summary holds aggregate metrics from the load generator.
type Summary struct {
	TotalDurationSeconds     float64 `json:"total_duration_seconds"`
	TotalRequests            int     `json:"total_requests"`
	SuccessfulRequests       int     `json:"successful_requests"`
	FailedRequests           int     `json:"failed_requests"`
	ThroughputAggregateTPS   float64 `json:"throughput_aggregate_tps"`
	RequestsPerSecond        float64 `json:"requests_per_second"`
	AcceleratorUtilizationPct *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorMemoryPeakGiB *float64 `json:"accelerator_memory_peak_gib,omitempty"`
}

// ParseLoadgenOutput parses the JSON output from a load generator pod.
// Pod logs may contain non-JSON progress lines on stderr; this function
// first looks for content between ACCELBENCH_JSON_BEGIN/END markers,
// then falls back to scanning for JSON lines.
func ParseLoadgenOutput(data []byte) (*LoadgenOutput, error) {
	var out LoadgenOutput

	// Strategy 1: Look for marker-delimited JSON.
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

	// Strategy 2: Try the whole blob (fast path for clean output).
	if err := json.Unmarshal(data, &out); err == nil {
		return &out, nil
	}

	// Strategy 3: Scan line-by-line for a JSON payload.
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal(line, &out); err == nil && len(out.Requests) > 0 {
			return &out, nil
		}
	}
	return nil, fmt.Errorf("parse loadgen output: no valid JSON payload found in %d bytes of log output", len(data))
}

// ComputeMetrics takes parsed loadgen output and computes the full set of
// benchmark metrics including p50/p90/p95/p99 percentiles.
func ComputeMetrics(out *LoadgenOutput) *database.BenchmarkMetrics {
	successful := filterSuccessful(out.Requests)

	var ttfts, e2es, itls []float64
	var totalOutputTokens int
	for _, r := range successful {
		ttfts = append(ttfts, r.TTFTMs)
		e2es = append(e2es, r.E2ELatencyMs)
		itls = append(itls, r.ITLMs)
		totalOutputTokens += r.OutputTokens
	}

	ttftP50, ttftP90, ttftP95, ttftP99 := percentiles(ttfts)
	e2eP50, e2eP90, e2eP95, e2eP99 := percentiles(e2es)
	itlP50, itlP90, itlP95, itlP99 := percentiles(itls)

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

	aggTPS := &out.Summary.ThroughputAggregateTPS
	rps := &out.Summary.RequestsPerSecond
	dur := &out.Summary.TotalDurationSeconds
	successCount := out.Summary.SuccessfulRequests
	failCount := out.Summary.FailedRequests

	return &database.BenchmarkMetrics{
		TTFTP50Ms:                ttftP50,
		TTFTP90Ms:                ttftP90,
		TTFTP95Ms:                ttftP95,
		TTFTP99Ms:                ttftP99,
		E2ELatencyP50Ms:          e2eP50,
		E2ELatencyP90Ms:          e2eP90,
		E2ELatencyP95Ms:          e2eP95,
		E2ELatencyP99Ms:          e2eP99,
		ITLP50Ms:                 itlP50,
		ITLP90Ms:                 itlP90,
		ITLP95Ms:                 itlP95,
		ITLP99Ms:                 itlP99,
		ThroughputPerRequestTPS:  throughputPerRequest,
		ThroughputAggregateTPS:   aggTPS,
		RequestsPerSecond:        rps,
		AcceleratorUtilizationPct: out.Summary.AcceleratorUtilizationPct,
		AcceleratorMemoryPeakGiB: out.Summary.AcceleratorMemoryPeakGiB,
		SuccessfulRequests:       &successCount,
		FailedRequests:           &failCount,
		TotalDurationSeconds:     dur,
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
