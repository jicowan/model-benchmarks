package metrics

import (
	"encoding/json"
	"math"
	"testing"
)

func TestPercentile_Empty(t *testing.T) {
	got := percentile(nil, 50)
	if got != 0 {
		t.Errorf("percentile of empty slice: got %f, want 0", got)
	}
}

func TestPercentile_Single(t *testing.T) {
	got := percentile([]float64{42.0}, 99)
	if got != 42.0 {
		t.Errorf("percentile of single element: got %f, want 42.0", got)
	}
}

func TestPercentile_Known(t *testing.T) {
	// sorted: 1,2,3,4,5,6,7,8,9,10
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		p    float64
		want float64
	}{
		{50, 5},
		{90, 9},
		{95, 10},
		{99, 10},
		{10, 1},
	}
	for _, tt := range tests {
		got := percentile(sorted, tt.p)
		if got != tt.want {
			t.Errorf("percentile(%v, %.0f) = %f, want %f", sorted, tt.p, got, tt.want)
		}
	}
}

func TestPercentiles_Empty(t *testing.T) {
	p50, p90, p95, p99 := percentiles(nil)
	if p50 != nil || p90 != nil || p95 != nil || p99 != nil {
		t.Error("percentiles of nil should return all nils")
	}
}

func TestPercentiles_Values(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50, p90, p95, p99 := percentiles(vals)

	if p50 == nil || p90 == nil || p95 == nil || p99 == nil {
		t.Fatal("percentiles returned unexpected nil")
	}
	if *p50 != 50 {
		t.Errorf("p50 = %f, want 50", *p50)
	}
	if *p90 != 90 {
		t.Errorf("p90 = %f, want 90", *p90)
	}
	if *p99 != 100 {
		t.Errorf("p99 = %f, want 100", *p99)
	}
}

func TestPercentiles_UnsortedInput(t *testing.T) {
	// Verify percentiles sorts internally and doesn't mutate input.
	vals := []float64{50, 10, 90, 30, 70, 100, 20, 80, 40, 60}
	orig := make([]float64, len(vals))
	copy(orig, vals)

	p50, _, _, _ := percentiles(vals)
	if p50 == nil || *p50 != 50 {
		t.Errorf("p50 of unsorted input = %v, want 50", p50)
	}
	// Input should not be mutated.
	for i := range vals {
		if vals[i] != orig[i] {
			t.Errorf("input mutated at index %d: got %f, want %f", i, vals[i], orig[i])
		}
	}
}

func TestFilterSuccessful(t *testing.T) {
	results := []RequestResult{
		{TTFTMs: 10, Success: true},
		{TTFTMs: 20, Success: false},
		{TTFTMs: 30, Success: true},
	}
	got := filterSuccessful(results)
	if len(got) != 2 {
		t.Fatalf("filterSuccessful: got %d results, want 2", len(got))
	}
	if got[0].TTFTMs != 10 || got[1].TTFTMs != 30 {
		t.Errorf("filterSuccessful returned wrong results: %+v", got)
	}
}

func TestMean(t *testing.T) {
	tests := []struct {
		vals []float64
		want float64
	}{
		{nil, 0},
		{[]float64{10}, 10},
		{[]float64{10, 20, 30}, 20},
		{[]float64{1, 2, 3, 4}, 2.5},
	}
	for _, tt := range tests {
		got := mean(tt.vals)
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("mean(%v) = %f, want %f", tt.vals, got, tt.want)
		}
	}
}

func TestParseLoadgenOutput(t *testing.T) {
	input := LoadgenOutput{
		Requests: []RequestResult{
			{TTFTMs: 10, E2ELatencyMs: 100, ITLMs: 5, OutputTokens: 50, InputTokens: 20, DurationSeconds: 1.0, Success: true},
			{TTFTMs: 20, E2ELatencyMs: 200, ITLMs: 10, OutputTokens: 60, InputTokens: 20, DurationSeconds: 2.0, Success: true},
		},
		Summary: Summary{
			TotalDurationSeconds:   5.0,
			TotalRequests:          2,
			SuccessfulRequests:     2,
			FailedRequests:         0,
			ThroughputAggregateTPS: 22.0,
			RequestsPerSecond:      0.4,
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out, err := ParseLoadgenOutput(data)
	if err != nil {
		t.Fatalf("ParseLoadgenOutput: %v", err)
	}
	if len(out.Requests) != 2 {
		t.Errorf("got %d requests, want 2", len(out.Requests))
	}
	if out.Summary.TotalRequests != 2 {
		t.Errorf("total_requests = %d, want 2", out.Summary.TotalRequests)
	}
}

func TestParseLoadgenOutput_Invalid(t *testing.T) {
	_, err := ParseLoadgenOutput([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestComputeMetrics(t *testing.T) {
	out := &LoadgenOutput{
		Requests: []RequestResult{
			{TTFTMs: 10, E2ELatencyMs: 100, ITLMs: 5, OutputTokens: 50, DurationSeconds: 1.0, Success: true},
			{TTFTMs: 20, E2ELatencyMs: 200, ITLMs: 10, OutputTokens: 100, DurationSeconds: 2.0, Success: true},
			{TTFTMs: 30, E2ELatencyMs: 300, ITLMs: 15, OutputTokens: 75, DurationSeconds: 1.5, Success: true},
			{TTFTMs: 999, E2ELatencyMs: 9999, ITLMs: 999, OutputTokens: 0, DurationSeconds: 0, Success: false},
		},
		Summary: Summary{
			TotalDurationSeconds:   10.0,
			TotalRequests:          4,
			SuccessfulRequests:     3,
			FailedRequests:         1,
			ThroughputAggregateTPS: 22.5,
			RequestsPerSecond:      0.3,
		},
	}

	m := ComputeMetrics(out)

	// Should only use 3 successful requests.
	if m.SuccessfulRequests == nil || *m.SuccessfulRequests != 3 {
		t.Errorf("successful_requests = %v, want 3", m.SuccessfulRequests)
	}
	if m.FailedRequests == nil || *m.FailedRequests != 1 {
		t.Errorf("failed_requests = %v, want 1", m.FailedRequests)
	}

	// TTFT percentiles should be computed from [10, 20, 30].
	if m.TTFTP50Ms == nil {
		t.Fatal("ttft_p50 is nil")
	}
	if *m.TTFTP50Ms != 20 {
		t.Errorf("ttft_p50 = %f, want 20", *m.TTFTP50Ms)
	}

	// Aggregate throughput should pass through from summary.
	if m.ThroughputAggregateTPS == nil || *m.ThroughputAggregateTPS != 22.5 {
		t.Errorf("throughput_aggregate = %v, want 22.5", m.ThroughputAggregateTPS)
	}

	// Per-request throughput: avg tokens = (50+100+75)/3 = 75, avg dur = (1+2+1.5)/3 = 1.5
	// expected = 75 / 1.5 = 50.0
	if m.ThroughputPerRequestTPS == nil {
		t.Fatal("throughput_per_request is nil")
	}
	if math.Abs(*m.ThroughputPerRequestTPS-50.0) > 0.01 {
		t.Errorf("throughput_per_request = %f, want 50.0", *m.ThroughputPerRequestTPS)
	}

	if m.TotalDurationSeconds == nil || *m.TotalDurationSeconds != 10.0 {
		t.Errorf("total_duration = %v, want 10.0", m.TotalDurationSeconds)
	}
}

func TestComputeMetrics_AllFailed(t *testing.T) {
	out := &LoadgenOutput{
		Requests: []RequestResult{
			{Success: false},
			{Success: false},
		},
		Summary: Summary{
			TotalRequests:      2,
			SuccessfulRequests: 0,
			FailedRequests:     2,
		},
	}

	m := ComputeMetrics(out)

	if m.TTFTP50Ms != nil {
		t.Error("ttft_p50 should be nil when all requests failed")
	}
	if m.ThroughputPerRequestTPS != nil {
		t.Error("throughput_per_request should be nil when all requests failed")
	}
}

func TestComputeMetrics_Empty(t *testing.T) {
	out := &LoadgenOutput{
		Requests: nil,
		Summary:  Summary{},
	}

	m := ComputeMetrics(out)
	if m.TTFTP50Ms != nil {
		t.Error("expected nil percentiles for empty requests")
	}
}
