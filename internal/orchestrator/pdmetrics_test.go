package orchestrator

import (
	"strings"
	"testing"
)

// Representative vLLM /metrics text for a DECODE pod in a disaggregated run:
// NIXL transfer series populate, external-KV prompt tokens present, phase-time
// histograms present. Shapes match vLLM v0.25.0 (histograms expose _sum/_count/
// _bucket; counters get _total on the wire).
const decodePDMetrics = `# HELP vllm:request_prefill_time_seconds prefill time
# TYPE vllm:request_prefill_time_seconds histogram
vllm:request_prefill_time_seconds_bucket{le="0.5",model_name="q"} 3
vllm:request_prefill_time_seconds_sum{model_name="q"} 1.5
vllm:request_prefill_time_seconds_count{model_name="q"} 10
vllm:request_decode_time_seconds_sum{model_name="q"} 40.0
vllm:request_decode_time_seconds_count{model_name="q"} 10
vllm:nixl_xfer_time_seconds_sum{model_name="q",engine="0"} 0.8
vllm:nixl_xfer_time_seconds_count{model_name="q",engine="0"} 10
vllm:nixl_bytes_transferred_sum{model_name="q",engine="0"} 1048576
vllm:nixl_bytes_transferred_count{model_name="q",engine="0"} 10
vllm:nixl_num_failed_transfers_total{model_name="q",engine="0"} 1
vllm:nixl_num_failed_notifications_total{model_name="q",engine="0"} 0
vllm:nixl_num_kv_expired_reqs_total{model_name="q",engine="0"} 2
vllm:external_prefix_cache_hits_total{model_name="q"} 120
vllm:external_prefix_cache_queries_total{model_name="q"} 200
vllm:prompt_tokens_total{model_name="q"} 5000
vllm:prompt_tokens_by_source{model_name="q",source="local_compute"} 100
vllm:prompt_tokens_by_source{model_name="q",source="external_kv_transfer"} 4900
`

func TestParsePDVLLMMetrics_Decode(t *testing.T) {
	r := parsePDVLLMMetrics(strings.NewReader(decodePDMetrics))

	if !approx(r.prefillTimeSum, 1.5) || !approx(r.prefillTimeCount, 10) {
		t.Errorf("prefill time sum/count = %.3f/%.0f, want 1.5/10", r.prefillTimeSum, r.prefillTimeCount)
	}
	// derived mean decode time = 40/10 = 4.0s
	if !approx(r.decodeTimeSum, 40) || !approx(r.decodeTimeCount, 10) {
		t.Errorf("decode time sum/count = %.3f/%.0f, want 40/10", r.decodeTimeSum, r.decodeTimeCount)
	}
	if !approx(r.nixlXferTimeSum, 0.8) || !approx(r.nixlXferTimeCount, 10) {
		t.Errorf("nixl xfer sum/count = %.3f/%.0f, want 0.8/10", r.nixlXferTimeSum, r.nixlXferTimeCount)
	}
	if !approx(r.nixlBytesSum, 1048576) {
		t.Errorf("nixl bytes sum = %.0f, want 1048576", r.nixlBytesSum)
	}
	// failures = 1 + 0 + 2 = 3 (0 notifications must not make it "absent")
	if !approx(r.nixlFailures, 3) {
		t.Errorf("nixl failures = %.0f, want 3 (1+0+2)", r.nixlFailures)
	}
	if !approx(r.extPrefixHits, 120) || !approx(r.extPrefixQueries, 200) {
		t.Errorf("external prefix hits/queries = %.0f/%.0f, want 120/200", r.extPrefixHits, r.extPrefixQueries)
	}
	// ONLY the external_kv_transfer variant, not the local_compute one.
	if !approx(r.externalKVPromptTokens, 4900) {
		t.Errorf("external_kv_transfer prompt tokens = %.0f, want 4900", r.externalKVPromptTokens)
	}
}

// A prefill/single-node style scrape with NO nixl_* / external series — every
// PD field must stay -1 (absent), so downstream persists NULL. Guards graceful
// degradation on older NIXL images.
func TestParsePDVLLMMetrics_AbsentSeries(t *testing.T) {
	plain := `vllm:prompt_tokens_total{model_name="q"} 5000
vllm:generation_tokens_total{model_name="q"} 2000
vllm:kv_cache_usage_perc{model_name="q"} 0.4
`
	r := parsePDVLLMMetrics(strings.NewReader(plain))
	for name, v := range map[string]float64{
		"nixlXferTimeSum": r.nixlXferTimeSum, "nixlBytesSum": r.nixlBytesSum,
		"nixlFailures": r.nixlFailures, "extPrefixHits": r.extPrefixHits,
		"externalKVPromptTokens": r.externalKVPromptTokens, "prefillTimeSum": r.prefillTimeSum,
	} {
		if v != -1 {
			t.Errorf("%s = %.3f, want -1 (absent)", name, v)
		}
	}
}

// nixl failures genuinely 0 (transfers ran, none failed) must be 0, not -1.
func TestParsePDVLLMMetrics_ZeroFailuresDistinctFromAbsent(t *testing.T) {
	txt := `vllm:nixl_num_failed_transfers_total{engine="0"} 0
vllm:nixl_num_failed_notifications_total{engine="0"} 0
vllm:nixl_num_kv_expired_reqs_total{engine="0"} 0
`
	r := parsePDVLLMMetrics(strings.NewReader(txt))
	if r.nixlFailures != 0 {
		t.Errorf("nixl failures = %.0f, want 0 (seen, all zero — not absent)", r.nixlFailures)
	}
}

// EPP :9090 metrics — both the llm_d_epp_* names and a decision counter split
// across decision_type labels.
const eppMetrics = `# HELP llm_d_epp_disagg_decision_total decisions
# TYPE llm_d_epp_disagg_decision_total counter
llm_d_epp_disagg_decision_total{decision_type="decode-only",model_name="q"} 30
llm_d_epp_disagg_decision_total{decision_type="prefill-decode",model_name="q"} 70
llm_d_epp_average_kv_cache_utilization{name="pd-pool"} 0.55
llm_d_epp_average_queue_size{name="pd-pool"} 2
llm_d_epp_ready_pods{name="pd-pool"} 2
`

func TestParsePDEPPMetrics(t *testing.T) {
	r := parsePDEPPMetrics(strings.NewReader(eppMetrics))
	if !approx(r.decisionTotal, 100) {
		t.Errorf("decision total = %.0f, want 100", r.decisionTotal)
	}
	if !approx(r.decisionDecodeOnly, 30) || !approx(r.decisionPD, 70) {
		t.Errorf("decode-only/pd = %.0f/%.0f, want 30/70", r.decisionDecodeOnly, r.decisionPD)
	}
	// engaged rate = (100-30)/100 = 0.70
	engaged := (r.decisionTotal - r.decisionDecodeOnly) / r.decisionTotal
	if !approx(engaged, 0.70) {
		t.Errorf("engaged rate = %.3f, want 0.70", engaged)
	}
	if !approx(r.poolKVUtil, 0.55) || !approx(r.poolQueueSize, 2) || !approx(r.poolReadyPods, 2) {
		t.Errorf("pool gauges wrong: kv=%.2f q=%.0f ready=%.0f", r.poolKVUtil, r.poolQueueSize, r.poolReadyPods)
	}
}

// Defensive prefix tolerance: the underlying GAIE inference_pool_* names must
// also be recognized (version drift).
func TestParsePDEPPMetrics_GAIEPrefixes(t *testing.T) {
	txt := `inference_pool_average_kv_cache_utilization{name="p"} 0.33
inference_pool_average_queue_size{name="p"} 5
inference_pool_ready_pods{name="p"} 3
`
	r := parsePDEPPMetrics(strings.NewReader(txt))
	if !approx(r.poolKVUtil, 0.33) || !approx(r.poolQueueSize, 5) || !approx(r.poolReadyPods, 3) {
		t.Errorf("GAIE-prefixed pool gauges not parsed: %+v", r)
	}
}

// EPP unreachable / empty → all -1, engaged rate not computable (guard div-by-zero downstream).
func TestParsePDEPPMetrics_Empty(t *testing.T) {
	r := parsePDEPPMetrics(strings.NewReader(""))
	if r.decisionTotal != -1 || r.poolKVUtil != -1 {
		t.Errorf("empty EPP scrape should be all -1, got %+v", r)
	}
}
