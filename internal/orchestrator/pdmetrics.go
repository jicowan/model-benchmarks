package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PRD-62: prefill/decode + KV-transfer + EPP routing metrics. This file is the
// DISAGGREGATED-ONLY metric surface. It is entirely separate from the shared
// parsePrometheusMetricsExtended path so that single-node and co-located runs
// are byte-for-byte unaffected — none of this code runs for them (the
// orchestrator only invokes it when cfg.IsDisaggregated()). No new parse arm is
// added to the single-node path.
//
// All metric names/types are source-verified at vLLM v0.25.0 and llm-d EPP /
// GAIE v1.5.0 (see PRD-62 tables). Absent series stay -1 → NULL downstream, so
// an older NIXL image (nixl_* empty) or an unreachable EPP degrades cleanly.

// pdVLLMResult holds the NEW per-pod vLLM PD/KV counters from one scrape of a
// prefill or decode pod's /metrics. -1 = the series was absent this scrape.
// Histograms are summarized as _sum + _count so a mean is derivable; cumulative
// counters are captured raw for run-window first/last delta tracking.
type pdVLLMResult struct {
	prefillTimeSum   float64 // vllm:request_prefill_time_seconds_sum
	prefillTimeCount float64 // vllm:request_prefill_time_seconds_count
	decodeTimeSum    float64 // vllm:request_decode_time_seconds_sum
	decodeTimeCount  float64 // vllm:request_decode_time_seconds_count

	nixlXferTimeSum   float64 // vllm:nixl_xfer_time_seconds_sum
	nixlXferTimeCount float64 // vllm:nixl_xfer_time_seconds_count
	nixlBytesSum      float64 // vllm:nixl_bytes_transferred_sum
	nixlBytesCount    float64 // vllm:nixl_bytes_transferred_count
	nixlFailures      float64 // Σ of the three nixl failure/expiry counters; -1 if none seen

	extPrefixHits    float64 // vllm:external_prefix_cache_hits_total
	extPrefixQueries float64 // vllm:external_prefix_cache_queries_total

	// vllm:prompt_tokens_by_source{source="external_kv_transfer"} — decode-side
	// prompt tokens that arrived via remote KV (per-pod proof of disaggregation).
	externalKVPromptTokens float64
}

func newPDVLLMResult() pdVLLMResult {
	return pdVLLMResult{
		prefillTimeSum: -1, prefillTimeCount: -1,
		decodeTimeSum: -1, decodeTimeCount: -1,
		nixlXferTimeSum: -1, nixlXferTimeCount: -1,
		nixlBytesSum: -1, nixlBytesCount: -1,
		nixlFailures:  -1,
		extPrefixHits: -1, extPrefixQueries: -1,
		externalKVPromptTokens: -1,
	}
}

// parsePDVLLMMetrics parses the disaggregation-specific vLLM counters from a
// prefill/decode pod's /metrics text. Only the NEW PD/KV series — the existing
// util/tokens/prefix counters are still parsed by parsePrometheusMetricsExtended
// on the same scrape; this is additive and independent.
func parsePDVLLMMetrics(r io.Reader) pdVLLMResult {
	res := newPDVLLMResult()
	var nixlFail float64
	nixlFailSeen := false

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		// Phase wall-time histograms (_sum / _count).
		case strings.HasPrefix(line, "vllm:request_prefill_time_seconds_sum"):
			setIfOK(line, &res.prefillTimeSum)
		case strings.HasPrefix(line, "vllm:request_prefill_time_seconds_count"):
			setIfOK(line, &res.prefillTimeCount)
		case strings.HasPrefix(line, "vllm:request_decode_time_seconds_sum"):
			setIfOK(line, &res.decodeTimeSum)
		case strings.HasPrefix(line, "vllm:request_decode_time_seconds_count"):
			setIfOK(line, &res.decodeTimeCount)

		// NIXL KV-transfer histograms (_sum / _count).
		case strings.HasPrefix(line, "vllm:nixl_xfer_time_seconds_sum"):
			setIfOK(line, &res.nixlXferTimeSum)
		case strings.HasPrefix(line, "vllm:nixl_xfer_time_seconds_count"):
			setIfOK(line, &res.nixlXferTimeCount)
		case strings.HasPrefix(line, "vllm:nixl_bytes_transferred_sum"):
			setIfOK(line, &res.nixlBytesSum)
		case strings.HasPrefix(line, "vllm:nixl_bytes_transferred_count"):
			setIfOK(line, &res.nixlBytesCount)

		// NIXL failure/expiry counters — summed into one "transfer problems"
		// figure. A genuine 0 (transfers ran, none failed) stays distinct from
		// absent (-1) via nixlFailSeen.
		case strings.HasPrefix(line, "vllm:nixl_num_failed_transfers_total"),
			strings.HasPrefix(line, "vllm:nixl_num_failed_notifications_total"),
			strings.HasPrefix(line, "vllm:nixl_num_kv_expired_reqs_total"):
			if v, err := parsePromValue(line); err == nil {
				nixlFail += v
				nixlFailSeen = true
			}

		// External (cross-instance / connector) prefix cache.
		case strings.HasPrefix(line, "vllm:external_prefix_cache_hits_total"):
			setIfOK(line, &res.extPrefixHits)
		case strings.HasPrefix(line, "vllm:external_prefix_cache_queries_total"):
			setIfOK(line, &res.extPrefixQueries)

		// prompt_tokens_by_source, external_kv_transfer variant only. On the wire
		// this counter emits BOTH a `_total` value line and a `_created`
		// timestamp line, and BOTH carry the source= label — so exclude
		// `_created` or its ~1.7e9 timestamp would clobber the real count.
		case strings.HasPrefix(line, "vllm:prompt_tokens_by_source"):
			if strings.Contains(line, `source="external_kv_transfer"`) &&
				!strings.Contains(line, "_created") {
				setIfOK(line, &res.externalKVPromptTokens)
			}
		}
	}
	if nixlFailSeen {
		res.nixlFailures = nixlFail
	}
	return res
}

// setIfOK parses a Prometheus line's value and writes it to dst on success.
func setIfOK(line string, dst *float64) {
	if v, err := parsePromValue(line); err == nil {
		*dst = v
	}
}

// ── EPP metrics (Layer 1b) ──────────────────────────────────────────────────

// pdEPPResult holds the cluster-level EPP metrics from one scrape of the EPP's
// :9090/metrics. The EPP is a single shared instance per run, so these are
// run-level, NOT per-role. -1 = absent this scrape.
type pdEPPResult struct {
	// Disaggregation decisions by decision_type. The headline signal:
	// engaged rate = (total - decodeOnly) / total.
	decisionTotal      float64 // Σ over all decision_type
	decisionDecodeOnly float64 // decision_type="decode-only"
	decisionPD         float64 // decision_type="prefill-decode"

	// Pool-pressure gauges as the router sees them.
	poolKVUtil    float64 // inference_pool_average_kv_cache_utilization
	poolQueueSize float64 // inference_pool_average_queue_size
	poolReadyPods float64 // inference_pool_ready_pods
}

func newPDEPPResult() pdEPPResult {
	return pdEPPResult{
		decisionTotal: -1, decisionDecodeOnly: -1, decisionPD: -1,
		poolKVUtil: -1, poolQueueSize: -1, poolReadyPods: -1,
	}
}

// eppDecisionType extracts the decision_type label value from a metric line.
func eppDecisionType(line string) string {
	// …{...,decision_type="prefill-decode",...} value
	const key = `decision_type="`
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// parsePDEPPMetrics parses the EPP's :9090/metrics. Scrapes BOTH the llm-d
// (llm_d_epp_*) and the underlying GAIE (inference_pool_*/inference_extension_*)
// prefixes defensively — name drift across llm-d/GAIE versions is expected, the
// same way the code already tolerates vllm:/sglang:. The disagg-decision counter
// is aggregated across decision_type-labeled lines within a single scrape.
func parsePDEPPMetrics(r io.Reader) pdEPPResult {
	res := newPDEPPResult()
	var decTotal, decDecodeOnly, decPD float64
	decSeen := false

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		// Disaggregation-decision counter. Count ONLY the canonical llm_d_epp_*
		// name: the EPP emits BOTH llm_d_epp_disagg_decision_total AND the
		// deprecated llm_d_inference_scheduler_disagg_decision_total with
		// IDENTICAL values (confirmed live) — matching both would double-count.
		// The deprecated alias is deliberately ignored here. (llm_d_epp_pd_decision
		// is a distinct PD-only variant not emitted in our build; if a future
		// version drops the disagg name for it, revisit.)
		case strings.HasPrefix(line, "llm_d_epp_disagg_decision_total"):
			v, err := parsePromValue(line)
			if err != nil {
				continue
			}
			decSeen = true
			decTotal += v
			switch eppDecisionType(line) {
			case "decode-only":
				decDecodeOnly += v
			case "prefill-decode":
				decPD += v
			}

		// Pool-pressure gauges — llm_d_epp_* preferred, inference_pool_* underlying.
		case strings.HasPrefix(line, "llm_d_epp_average_kv_cache_utilization"),
			strings.HasPrefix(line, "inference_pool_average_kv_cache_utilization"):
			setIfOK(line, &res.poolKVUtil)
		case strings.HasPrefix(line, "llm_d_epp_average_queue_size"),
			strings.HasPrefix(line, "inference_pool_average_queue_size"):
			setIfOK(line, &res.poolQueueSize)
		case strings.HasPrefix(line, "llm_d_epp_ready_pods"),
			strings.HasPrefix(line, "inference_pool_ready_pods"):
			setIfOK(line, &res.poolReadyPods)
		}
	}
	if decSeen {
		res.decisionTotal = decTotal
		res.decisionDecodeOnly = decDecodeOnly
		res.decisionPD = decPD
	}
	return res
}


// ── PD metrics scraper (Layer 2) ────────────────────────────────────────────

// PDMetrics is the run-level disaggregation summary the scraper produces at
// Stop(). All fields are pointers so "not collected" (older NIXL image, EPP
// unreachable, non-disaggregated run) persists as NULL rather than a misleading
// zero. Per-role phase-time means are also surfaced here for the report; the
// per-role GPU shards themselves come from PRD-59's keyed GPU scraper.
type PDMetrics struct {
	// KV transfer (decode-side NIXL; run-window means/deltas).
	KVTransferTimeAvgMs *float64
	KVTransferBytesTotal *float64
	KVTransferFailures   *float64
	// Per-phase server wall-time means (seconds → ms).
	PrefillTimeAvgMs *float64
	DecodeTimeAvgMs  *float64
	// External (cross-instance) prefix cache reuse rate (0–100).
	ExternalPrefixCacheHitRate *float64
	// EPP disaggregation decisions (run totals) + derived engaged rate (0–100).
	DisaggPrefillDecodeCount *float64
	DisaggDecodeOnlyCount    *float64
	DisaggEngagedRatePct     *float64
	// EPP pool-pressure (last observed).
	PoolKVCacheUtilPct *float64
	PoolQueueSizeAvg   *float64
}

// pdScrapeTarget is one per-role vLLM /metrics endpoint (pod IP + the role's
// vLLM port: prefill=8000, decode=8200 behind the sidecar).
type pdScrapeTarget struct {
	url  string
	role string
}

// PDScraper polls the per-role vLLM /metrics + the EPP /metrics for a
// disaggregated run and reduces them to a run-level PDMetrics. It is created and
// started ONLY for cfg.IsDisaggregated() runs — nothing here touches the
// single-node or co-located path.
type PDScraper struct {
	vllmTargets []pdScrapeTarget
	eppURL      string
	client      *http.Client

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// Per-role first/last snapshots for run-window deltas (counters) + latest
	// histogram sums/counts (for means). Keyed by role.
	first map[string]pdVLLMResult
	last  map[string]pdVLLMResult
	// EPP first/last (decision counters) + latest gauges.
	eppFirst pdEPPResult
	eppLast  pdEPPResult
}

// NewPDScraper builds a PD metrics scraper. vllmTargets are (podIP,role,port)
// resolved by the orchestrator; eppURL is the EPP :9090/metrics endpoint (empty
// disables the EPP scrape).
func NewPDScraper(vllmTargets []pdScrapeTarget, eppURL string) *PDScraper {
	return &PDScraper{
		vllmTargets: vllmTargets,
		eppURL:      eppURL,
		client:      &http.Client{Timeout: scrapeTimeout},
		done:        make(chan struct{}),
		first:       map[string]pdVLLMResult{},
		last:        map[string]pdVLLMResult{},
		eppFirst:    newPDEPPResult(),
		eppLast:     newPDEPPResult(),
	}
}

func (p *PDScraper) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	go p.loop(ctx)
}

func (p *PDScraper) loop(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()
	p.scrape(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.scrape(ctx)
		}
	}
}

func (p *PDScraper) scrape(ctx context.Context) {
	for _, t := range p.vllmTargets {
		body, ok := p.get(ctx, t.url)
		if !ok {
			continue
		}
		res := parsePDVLLMMetrics(strings.NewReader(body))
		p.mu.Lock()
		if _, seen := p.first[t.role]; !seen {
			p.first[t.role] = res
		}
		p.last[t.role] = res
		p.mu.Unlock()
	}
	if p.eppURL != "" {
		if body, ok := p.get(ctx, p.eppURL); ok {
			res := parsePDEPPMetrics(strings.NewReader(body))
			p.mu.Lock()
			if p.eppFirst.decisionTotal < 0 {
				p.eppFirst = res
			}
			p.eppLast = res
			p.mu.Unlock()
		}
	}
}

func (p *PDScraper) get(ctx context.Context, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false // non-fatal: a missing endpoint just yields no samples
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return "", false
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Stop halts scraping and returns the run-level PD summary. Returns nil if no
// disaggregation signal was collected at all (keeps the row clean).
func (p *PDScraper) Stop() *PDMetrics {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()

	m := &PDMetrics{}
	any := false

	// Per-role: decode role carries the NIXL/external signals; prefill carries
	// prefill_time. Fall back to whichever role reported a given series.
	prefillLast := p.last["prefill"]
	decodeLast := p.last["decode"]

	// Phase-time means (seconds → ms) from the latest histogram sum/count.
	if v, ok := histMeanMs(prefillLast.prefillTimeSum, prefillLast.prefillTimeCount); ok {
		m.PrefillTimeAvgMs = &v
		any = true
	} else if v, ok := histMeanMs(decodeLast.prefillTimeSum, decodeLast.prefillTimeCount); ok {
		m.PrefillTimeAvgMs = &v
		any = true
	}
	if v, ok := histMeanMs(decodeLast.decodeTimeSum, decodeLast.decodeTimeCount); ok {
		m.DecodeTimeAvgMs = &v
		any = true
	}

	// KV transfer (decode side executes the pull → its NIXL series are the
	// relevant ones; fall back to prefill if only that reported).
	xr := decodeLast
	if xr.nixlXferTimeCount < 0 {
		xr = prefillLast
	}
	if v, ok := histMeanMs(xr.nixlXferTimeSum, xr.nixlXferTimeCount); ok {
		m.KVTransferTimeAvgMs = &v
		any = true
	}
	if xr.nixlBytesSum >= 0 {
		b := xr.nixlBytesSum
		m.KVTransferBytesTotal = &b
		any = true
	}
	if xr.nixlFailures >= 0 {
		f := xr.nixlFailures
		m.KVTransferFailures = &f
		any = true
	}

	// External prefix-cache reuse rate over the run window (decode side).
	if r, ok := rateOverWindow(p.first["decode"].extPrefixHits, decodeLast.extPrefixHits,
		p.first["decode"].extPrefixQueries, decodeLast.extPrefixQueries); ok {
		m.ExternalPrefixCacheHitRate = &r
		any = true
	}

	// EPP decisions (run totals via delta) + engaged rate.
	if p.eppLast.decisionTotal >= 0 && p.eppFirst.decisionTotal >= 0 {
		total := p.eppLast.decisionTotal - p.eppFirst.decisionTotal
		pd := p.eppLast.decisionPD - p.eppFirst.decisionPD
		decodeOnly := p.eppLast.decisionDecodeOnly - p.eppFirst.decisionDecodeOnly
		if total > 0 {
			m.DisaggPrefillDecodeCount = &pd
			m.DisaggDecodeOnlyCount = &decodeOnly
			rate := (total - decodeOnly) / total * 100
			m.DisaggEngagedRatePct = &rate
			any = true
		}
	}
	if p.eppLast.poolKVUtil >= 0 {
		v := p.eppLast.poolKVUtil * 100 // gauge is 0–1
		m.PoolKVCacheUtilPct = &v
		any = true
	}
	if p.eppLast.poolQueueSize >= 0 {
		v := p.eppLast.poolQueueSize
		m.PoolQueueSizeAvg = &v
		any = true
	}

	if !any {
		return nil
	}
	return m
}

// histMeanMs returns (sum/count)*1000 as milliseconds when both are present and
// count>0. Absent (-1) or zero count → not ok.
func histMeanMs(sum, count float64) (float64, bool) {
	if sum < 0 || count <= 0 {
		return 0, false
	}
	return sum / count * 1000, true
}

// rateOverWindow computes a 0–100 hit rate from counter deltas; guards absent/
// zero denominators.
func rateOverWindow(firstHits, lastHits, firstQ, lastQ float64) (float64, bool) {
	if firstHits < 0 || lastHits < 0 || firstQ < 0 || lastQ < 0 {
		return 0, false
	}
	q := lastQ - firstQ
	if q <= 0 {
		return 0, false
	}
	return (lastHits - firstHits) / q * 100, true
}

// pdVLLMTargetURL builds the /metrics URL for a role's vLLM: prefill serves on
// :8000, decode's vLLM is on :8200 (the sidecar occupies :8000).
func pdVLLMTargetURL(podIP, role string) string {
	port := 8000
	if role == "decode" {
		port = 8200
	}
	return fmt.Sprintf("http://%s:%d/metrics", podIP, port)
}

var _ = log.Printf
