package orchestrator

import (
	"strings"
	"testing"
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
