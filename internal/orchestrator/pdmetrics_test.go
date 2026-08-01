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
vllm:prompt_tokens_by_source_total{model_name="q",source="local_compute"} 100
vllm:prompt_tokens_by_source_total{model_name="q",source="external_kv_transfer"} 4900
vllm:prompt_tokens_by_source_created{model_name="q",source="external_kv_transfer"} 1785619026.5
vllm:external_prefix_cache_hits_created{model_name="q"} 1785619026.5
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

// The EPP emits BOTH the canonical llm_d_epp_disagg_decision_total AND the
// deprecated llm_d_inference_scheduler_disagg_decision_total alias with
// identical values (confirmed live). The parser must count only the canonical
// one — else the total double-counts.
func TestParsePDEPPMetrics_NoDoubleCountDeprecatedAlias(t *testing.T) {
	txt := `llm_d_epp_disagg_decision_total{decision_type="decode-only"} 30
llm_d_epp_disagg_decision_total{decision_type="prefill-decode"} 70
llm_d_inference_scheduler_disagg_decision_total{decision_type="decode-only"} 30
llm_d_inference_scheduler_disagg_decision_total{decision_type="prefill-decode"} 70
`
	r := parsePDEPPMetrics(strings.NewReader(txt))
	if !approx(r.decisionTotal, 100) {
		t.Errorf("decision total = %.0f, want 100 (deprecated alias must NOT double-count to 200)", r.decisionTotal)
	}
	if !approx(r.decisionPD, 70) {
		t.Errorf("prefill-decode = %.0f, want 70", r.decisionPD)
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

// TestActiveAverage_ExcludesIdleTail: the pool-gauge reduction must average only
// over scrapes where the pool was serving (>0), so warmup + post-loadgen idle
// zero-readings don't drag it toward 0 (the "pool KV util shows 0%" bug). And an
// all-idle series yields not-ok → NULL, not a false 0.
func TestActiveAverage_ExcludesIdleTail(t *testing.T) {
	var sum float64
	var n int
	// scrape sequence: 0 (warmup), 0.4, 0.6, 0.5 (serving), 0, 0 (idle tail)
	for _, v := range []float64{0, 0.4, 0.6, 0.5, 0, 0} {
		accumulateActive(v, &sum, &n)
	}
	avg, ok := activeAverage(sum, n)
	if !ok {
		t.Fatal("expected an active average")
	}
	if !approx(avg, 0.5) { // (0.4+0.6+0.5)/3, NOT /6
		t.Errorf("active avg = %.3f, want 0.5 (idle scrapes excluded)", avg)
	}
}

func TestActiveAverage_AllIdleIsNull(t *testing.T) {
	var sum float64
	var n int
	for _, v := range []float64{0, 0, 0} {
		accumulateActive(v, &sum, &n)
	}
	if _, ok := activeAverage(sum, n); ok {
		t.Error("all-idle series should yield not-ok (NULL), not a false 0")
	}
}

// TestAggregateRole_MultiPod: two decode pods must AGGREGATE — bytes/failures
// sum, transfer-time pools to a group mean, external-cache deltas sum — not
// collapse to one pod (the multi-replica xPyD fix).
func TestAggregateRole_MultiPod(t *testing.T) {
	p := NewPDScraper(nil, "")
	// pod A: 100 bytes over 2 xfers (sum 0.2s), 1 failure, ext hits 10→30 q 40→100
	a := newPDVLLMResult()
	a.nixlBytesSum, a.nixlBytesCount = 100, 2
	a.nixlXferTimeSum, a.nixlXferTimeCount = 0.2, 2
	a.nixlFailures = 1
	a.extPrefixHits, a.extPrefixQueries = 30, 100
	aFirst := newPDVLLMResult()
	aFirst.extPrefixHits, aFirst.extPrefixQueries = 10, 40
	// pod B: 300 bytes over 4 xfers (sum 0.6s), 0 failures, ext hits 0→20 q 0→60
	b := newPDVLLMResult()
	b.nixlBytesSum, b.nixlBytesCount = 300, 4
	b.nixlXferTimeSum, b.nixlXferTimeCount = 0.6, 4
	b.nixlFailures = 0
	b.extPrefixHits, b.extPrefixQueries = 20, 60
	bFirst := newPDVLLMResult()
	bFirst.extPrefixHits, bFirst.extPrefixQueries = 0, 0

	p.urlRole = map[string]string{"a": "decode", "b": "decode"}
	p.first = map[string]pdVLLMResult{"a": aFirst, "b": bFirst}
	p.last = map[string]pdVLLMResult{"a": a, "b": b}

	agg := p.aggregateRole("decode")
	if !approx(agg.nixlBytesSum, 400) { // 100+300
		t.Errorf("bytes sum = %.0f, want 400", agg.nixlBytesSum)
	}
	if !approx(agg.nixlFailures, 1) { // 1+0
		t.Errorf("failures = %.0f, want 1", agg.nixlFailures)
	}
	// pooled transfer-time mean = (0.2+0.6)/(2+4) = 0.1333s = 133.3ms
	if v, ok := histMeanMs(agg.nixlXferTimeSum, agg.nixlXferTimeCount); !ok || !approx(v, 800.0/6.0) {
		t.Errorf("pooled xfer mean ms = %.3f, want %.3f", v, 800.0/6.0)
	}
	// external cache: hit-deltas (20+20)=40 over query-deltas (60+60)=120 = 33.3%
	if r, ok := rateOverWindow(0, agg.extPrefixHits, 0, agg.extPrefixQueries); !ok || !approx(r, 100.0/3.0) {
		t.Errorf("pooled ext hit rate = %.3f, want %.3f", r, 100.0/3.0)
	}
}
