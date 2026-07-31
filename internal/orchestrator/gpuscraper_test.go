package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParsePrometheusMetricsExtended(t *testing.T) {
	input := `# HELP vllm:gpu_cache_usage_perc GPU cache usage percentage
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="llama"} 0.75
# HELP vllm:num_requests_waiting Number of waiting requests
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="llama"} 5
# HELP vllm:num_requests_running Number of running requests
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="llama"} 8
# HELP vllm:prompt_tokens_total Total prompt tokens
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="llama"} 10000
# HELP vllm:generation_tokens_total Total generation tokens
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="llama"} 5000
# HELP vllm:prefix_cache_hit_total Prefix cache hits
# TYPE vllm:prefix_cache_hit_total counter
vllm:prefix_cache_hit_total{model_name="llama"} 250
# HELP vllm:prefix_cache_queries_total Prefix cache queries
# TYPE vllm:prefix_cache_queries_total counter
vllm:prefix_cache_queries_total{model_name="llama"} 1000
# HELP vllm:num_preemptions_total Number of preemptions
# TYPE vllm:num_preemptions_total counter
vllm:num_preemptions_total{model_name="llama"} 3
`

	result := parsePrometheusMetricsExtended(strings.NewReader(input))

	if result.utilization != 0.75 {
		t.Errorf("utilization = %v, want 0.75", result.utilization)
	}
	if result.waiting != 5 {
		t.Errorf("waiting = %v, want 5", result.waiting)
	}
	if result.running != 8 {
		t.Errorf("running = %v, want 8", result.running)
	}
	if result.promptTokens != 10000 {
		t.Errorf("promptTokens = %v, want 10000", result.promptTokens)
	}
	if result.genTokens != 5000 {
		t.Errorf("genTokens = %v, want 5000", result.genTokens)
	}
	if result.prefixHits != 250 {
		t.Errorf("prefixHits = %v, want 250", result.prefixHits)
	}
	if result.prefixQueries != 1000 {
		t.Errorf("prefixQueries = %v, want 1000", result.prefixQueries)
	}
	if result.preemptions != 3 {
		t.Errorf("preemptions = %v, want 3", result.preemptions)
	}
}

func TestParsePrometheusMetricsExtended_SGLang(t *testing.T) {
	input := `# HELP sglang:token_usage The token usage
# TYPE sglang:token_usage gauge
sglang:token_usage{} 0.62
# HELP sglang:num_queue_reqs The number of requests in the waiting queue
# TYPE sglang:num_queue_reqs gauge
sglang:num_queue_reqs{} 3
# HELP sglang:num_running_reqs The number of running requests
# TYPE sglang:num_running_reqs gauge
sglang:num_running_reqs{} 4
# HELP sglang:prompt_tokens_total Number of prefill tokens processed
# TYPE sglang:prompt_tokens_total counter
sglang:prompt_tokens_total{} 8000
# HELP sglang:generation_tokens_total Number of generation tokens processed
# TYPE sglang:generation_tokens_total counter
sglang:generation_tokens_total{} 4000
# HELP sglang:cache_hit_rate The cache hit rate
# TYPE sglang:cache_hit_rate gauge
sglang:cache_hit_rate{} 0.35
`

	result := parsePrometheusMetricsExtended(strings.NewReader(input))

	if result.utilization != 0.62 {
		t.Errorf("utilization = %v, want 0.62", result.utilization)
	}
	if result.waiting != 3 {
		t.Errorf("waiting = %v, want 3", result.waiting)
	}
	if result.running != 4 {
		t.Errorf("running = %v, want 4", result.running)
	}
	if result.promptTokens != 8000 {
		t.Errorf("promptTokens = %v, want 8000", result.promptTokens)
	}
	if result.genTokens != 4000 {
		t.Errorf("genTokens = %v, want 4000", result.genTokens)
	}
	// SGLang cache_hit_rate is a gauge (0.0–1.0) scaled to percentage
	if result.prefixHits != 35 {
		t.Errorf("prefixHits = %v, want 35 (0.35 * 100)", result.prefixHits)
	}
	if result.prefixQueries != 100 {
		t.Errorf("prefixQueries = %v, want 100", result.prefixQueries)
	}
}

// dcgmBody builds a minimal DCGM exporter response for one GPU.
func dcgmBody(utilPct, fbUsedMiB float64) string {
	return "DCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} " + strconv.FormatFloat(utilPct, 'f', -1, 64) + "\n" +
		"DCGM_FI_DEV_FB_USED{gpu=\"0\"} " + strconv.FormatFloat(fbUsedMiB, 'f', -1, 64) + "\n"
}

// TestGPUScraperKeyed_PerNodeRoleAttribution drives the keyed scraper against
// two fake DCGM endpoints tagged prefill/decode and asserts the roll-up +
// per-shard breakdown. This is PRD-59 Layer 1's attribution guarantee.
func TestGPUScraperKeyed_PerNodeRoleAttribution(t *testing.T) {
	// prefill node: util 80%, 20 GiB (20480 MiB). decode node: util 20%, 10 GiB.
	prefillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(dcgmBody(80, 20480)))
	}))
	defer prefillSrv.Close()
	decodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(dcgmBody(20, 10240)))
	}))
	defer decodeSrv.Close()

	// Build a keyed scraper and point its two targets at the fake servers.
	// (metricsURL points nowhere valid — vLLM scrape just no-ops.)
	s := newGPUScraper("127.0.0.1", 1, 0, []dcgmTarget{
		{url: prefillSrv.URL, node: "10.0.0.1", role: "prefill"},
		{url: decodeSrv.URL, node: "10.0.0.2", role: "decode"},
	}, true)

	// Start the loop (waitForDCGM succeeds immediately — the fake servers 200),
	// let it scrape at least once, then Stop() reduces via the aggregation layer.
	s.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.keyedSamples) >= 2
		s.mu.Unlock()
		if got {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m := s.Stop()
	if m == nil {
		t.Fatal("expected metrics")
	}

	// Roll-up: util mean (80+20)/2=50, peak 80. Memory total = 20+10 = 30 GiB
	// (sum of per-node peaks); memory peak = 20 (hottest node).
	if !approx(m.UtilizationAvgPct, 50) || !approx(m.UtilizationPeakPct, 80) {
		t.Errorf("util avg/peak = %.2f/%.2f, want 50/80", m.UtilizationAvgPct, m.UtilizationPeakPct)
	}
	if !approx(m.MemoryTotalGiB, 30) {
		t.Errorf("mem total = %.2f, want 30", m.MemoryTotalGiB)
	}
	if !approx(m.MemoryPeakGiB, 20) {
		t.Errorf("mem peak = %.2f, want 20 (hottest node, not summed)", m.MemoryPeakGiB)
	}

	// Breakdown: two shards, roles attributed correctly.
	if len(m.Shards) != 2 {
		t.Fatalf("want 2 shards, got %d", len(m.Shards))
	}
	byRole := map[string]ShardMetrics{}
	for _, sh := range m.Shards {
		byRole[sh.Role] = sh
	}
	if p, ok := byRole["prefill"]; !ok || !approx(p.MemoryPeakGiB, 20) || !approx(p.UtilizationAvgPct, 80) {
		t.Errorf("prefill shard wrong: %+v", byRole["prefill"])
	}
	if d, ok := byRole["decode"]; !ok || !approx(d.MemoryPeakGiB, 10) || !approx(d.UtilizationAvgPct, 20) {
		t.Errorf("decode shard wrong: %+v", byRole["decode"])
	}
}

func TestParsePrometheusMetricsExtended_Missing(t *testing.T) {
	input := `# Only GPU cache metric
vllm:gpu_cache_usage_perc{model_name="llama"} 0.5
`

	result := parsePrometheusMetricsExtended(strings.NewReader(input))

	if result.utilization != 0.5 {
		t.Errorf("utilization = %v, want 0.5", result.utilization)
	}
	// All other values should be -1 (not found)
	if result.waiting != -1 {
		t.Errorf("waiting = %v, want -1", result.waiting)
	}
	if result.running != -1 {
		t.Errorf("running = %v, want -1", result.running)
	}
	if result.promptTokens != -1 {
		t.Errorf("promptTokens = %v, want -1", result.promptTokens)
	}
}
