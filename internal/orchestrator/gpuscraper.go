package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	scrapeInterval = 5 * time.Second
	scrapeTimeout  = 3 * time.Second
)

// GPUMetrics holds aggregated GPU metrics collected during a benchmark run.
type GPUMetrics struct {
	// DCGM_FI_DEV_GPU_UTIL ("GPU Busy" — SM any-active ratio). 0 if DCGM unavailable.
	UtilizationPeakPct float64
	UtilizationAvgPct  float64
	// DCGM_FI_DEV_FB_USED framebuffer usage in GiB. 0 if DCGM unavailable.
	MemoryPeakGiB float64
	MemoryAvgGiB  float64
	// Maximum number of waiting requests observed (from vLLM).
	WaitingRequestsMax int

	// Extended metrics (PRD-14)
	// Throughput breakdown (from vLLM)
	PromptThroughputTPS     float64
	GenerationThroughputTPS float64
	// KV cache metrics from vLLM (inference-specific)
	KVCacheUtilizationAvgPct  float64
	KVCacheUtilizationPeakPct float64
	// Prefix cache
	PrefixCacheHitRate float64
	// Preemption count
	PreemptionCount int
	// Running requests
	RunningRequestsAvg float64
	RunningRequestsMax int

	// PRD-22: DCP (DCGM Profiling) metrics. SMActive reflects warp occupancy
	// across SMs; TensorActive reflects tensor-core pipe utilization;
	// DRAMActive reflects memory-bandwidth utilization. All reported 0–100
	// (derived from DCGM's 0.0–1.0 ratios). Pointers are nil when the DCGM
	// exporter didn't emit DCP samples (e.g., SKU without profiling support,
	// or DCP counters disabled).
	SMActiveAvgPct      *float64
	SMActivePeakPct     *float64
	TensorActiveAvgPct  *float64
	TensorActivePeakPct *float64
	DRAMActiveAvgPct    *float64
	DRAMActivePeakPct   *float64
}

// GPUScraper periodically polls a vLLM Prometheus metrics endpoint and
// collects GPU utilization and queue depth samples. Optionally also scrapes
// DCGM exporter for actual GPU hardware metrics.
type GPUScraper struct {
	metricsURL     string
	dcgmURL        string // DCGM exporter endpoint (optional)
	totalMemoryGiB float64
	client         *http.Client

	mu                sync.Mutex
	utilizationSample []float64
	waitingSamples    []int
	runningSamples    []int
	cancel            context.CancelFunc
	done              chan struct{}

	// Counter tracking for rate metrics
	startTime          time.Time
	endTime            time.Time
	firstPromptTokens  float64
	lastPromptTokens   float64
	firstGenTokens     float64
	lastGenTokens      float64
	firstPrefixHits    float64
	lastPrefixHits     float64
	firstPrefixQueries float64
	lastPrefixQueries  float64
	firstPreemptions   float64
	lastPreemptions    float64
	samplesCollected   int

	// DCGM metrics samples
	dcgmUtilSamples []float64
	dcgmMemSamples  []float64 // Memory used in bytes

	// DCP metrics samples (PRD-22). Empty when DCP counters aren't emitted.
	dcgmSMActiveSamples     []float64 // 0–100 (DCGM_FI_PROF_SM_ACTIVE × 100)
	dcgmTensorActiveSamples []float64 // 0–100 (DCGM_FI_PROF_PIPE_TENSOR_ACTIVE × 100)
	dcgmDRAMActiveSamples   []float64 // 0–100 (DCGM_FI_PROF_DRAM_ACTIVE × 100)
}

// NewGPUScraper creates a scraper targeting the given vLLM service.
// totalMemoryGiB is the total GPU memory for the instance (used to
// derive peak memory from cache utilization percentage).
func NewGPUScraper(serviceHost string, port int, totalMemoryGiB float64) *GPUScraper {
	return NewGPUScraperWithDCGM(serviceHost, port, totalMemoryGiB, "")
}

// NewGPUScraperWithDCGM creates a scraper that targets both vLLM metrics and
// DCGM exporter for hardware GPU metrics. If nodeIP is empty, DCGM scraping is disabled.
func NewGPUScraperWithDCGM(serviceHost string, port int, totalMemoryGiB float64, nodeIP string) *GPUScraper {
	var dcgmURL string
	if nodeIP != "" {
		dcgmURL = fmt.Sprintf("http://%s:9400/metrics", nodeIP)
	}
	return &GPUScraper{
		metricsURL:     fmt.Sprintf("http://%s:%d/metrics", serviceHost, port),
		dcgmURL:        dcgmURL,
		totalMemoryGiB: totalMemoryGiB,
		client: &http.Client{
			Timeout: scrapeTimeout,
		},
		done:               make(chan struct{}),
		firstPromptTokens:  -1,
		firstGenTokens:     -1,
		firstPrefixHits:    -1,
		firstPrefixQueries: -1,
		firstPreemptions:   -1,
	}
}

// Start begins scraping in a background goroutine. It is safe to call
// Start only once.
func (s *GPUScraper) Start(ctx context.Context) {
	s.startTime = time.Now()
	ctx, s.cancel = context.WithCancel(ctx)
	go s.loop(ctx)
}

// Stop stops the scraper and returns the aggregated GPU metrics.
// Returns nil if no samples were collected.
func (s *GPUScraper) Stop() *GPUMetrics {
	s.endTime = time.Now()
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.utilizationSample) == 0 {
		return nil
	}

	var sum, peak float64
	for _, v := range s.utilizationSample {
		sum += v
		if v > peak {
			peak = v
		}
	}
	avg := sum / float64(len(s.utilizationSample))

	var maxWaiting int
	for _, w := range s.waitingSamples {
		if w > maxWaiting {
			maxWaiting = w
		}
	}

	// Running requests stats
	var runningSum float64
	var maxRunning int
	for _, r := range s.runningSamples {
		runningSum += float64(r)
		if r > maxRunning {
			maxRunning = r
		}
	}
	var runningAvg float64
	if len(s.runningSamples) > 0 {
		runningAvg = runningSum / float64(len(s.runningSamples))
	}

	// Convert KV cache utilization to percentage (vLLM reports 0.0-1.0).
	kvCachePeakPct := peak * 100
	kvCacheAvgPct := avg * 100

	// Compute throughput from counter deltas
	duration := s.endTime.Sub(s.startTime).Seconds()
	var promptTPS, genTPS float64
	if duration > 0 {
		if s.firstPromptTokens >= 0 && s.lastPromptTokens >= 0 {
			promptTPS = (s.lastPromptTokens - s.firstPromptTokens) / duration
		}
		if s.firstGenTokens >= 0 && s.lastGenTokens >= 0 {
			genTPS = (s.lastGenTokens - s.firstGenTokens) / duration
		}
	}

	// Compute prefix cache hit rate
	var prefixHitRate float64
	if s.firstPrefixQueries >= 0 && s.lastPrefixQueries >= 0 {
		queries := s.lastPrefixQueries - s.firstPrefixQueries
		hits := s.lastPrefixHits - s.firstPrefixHits
		if queries > 0 {
			prefixHitRate = (hits / queries) * 100
		}
	}

	// Preemption count (delta from first to last)
	var preemptions int
	if s.firstPreemptions >= 0 && s.lastPreemptions >= 0 {
		preemptions = int(s.lastPreemptions - s.firstPreemptions)
	}

	// Compute DCGM GPU utilization stats
	var dcgmUtilAvg, dcgmUtilPeak float64
	if len(s.dcgmUtilSamples) > 0 {
		var sum float64
		for _, v := range s.dcgmUtilSamples {
			sum += v
			if v > dcgmUtilPeak {
				dcgmUtilPeak = v
			}
		}
		dcgmUtilAvg = sum / float64(len(s.dcgmUtilSamples))
	}

	// Compute DCGM memory peak and average (convert MB to GiB)
	var dcgmMemPeakGiB, dcgmMemSum float64
	for _, v := range s.dcgmMemSamples {
		gib := v / 1024 // DCGM reports memory in MB
		if gib > dcgmMemPeakGiB {
			dcgmMemPeakGiB = gib
		}
		dcgmMemSum += gib
	}
	var dcgmMemAvgGiB float64
	if len(s.dcgmMemSamples) > 0 {
		dcgmMemAvgGiB = dcgmMemSum / float64(len(s.dcgmMemSamples))
	}

	smAvg, smPeak := aggregatePctSamples(s.dcgmSMActiveSamples)
	tensorAvg, tensorPeak := aggregatePctSamples(s.dcgmTensorActiveSamples)
	dramAvg, dramPeak := aggregatePctSamples(s.dcgmDRAMActiveSamples)

	return &GPUMetrics{
		// Primary metrics from DCGM (0 if unavailable)
		UtilizationPeakPct: dcgmUtilPeak,
		UtilizationAvgPct:  dcgmUtilAvg,
		MemoryPeakGiB:      dcgmMemPeakGiB,
		MemoryAvgGiB:       dcgmMemAvgGiB,
		// Request queue metrics from vLLM
		WaitingRequestsMax: maxWaiting,
		// Throughput from vLLM
		PromptThroughputTPS:     promptTPS,
		GenerationThroughputTPS: genTPS,
		// KV cache metrics from vLLM (inference-specific)
		KVCacheUtilizationAvgPct:  kvCacheAvgPct,
		KVCacheUtilizationPeakPct: kvCachePeakPct,
		PrefixCacheHitRate:        prefixHitRate,
		PreemptionCount:           preemptions,
		RunningRequestsAvg:        runningAvg,
		RunningRequestsMax:        maxRunning,
		// PRD-22: DCP metrics (nil pointers when no samples collected)
		SMActiveAvgPct:      smAvg,
		SMActivePeakPct:     smPeak,
		TensorActiveAvgPct:  tensorAvg,
		TensorActivePeakPct: tensorPeak,
		DRAMActiveAvgPct:    dramAvg,
		DRAMActivePeakPct:   dramPeak,
	}
}

// aggregatePctSamples returns (avg, peak) pointers over a sample slice. Returns
// (nil, nil) on empty — lets callers preserve the "no DCP data" signal through
// to the DB as a NULL, rather than storing 0 which would be ambiguous with a
// real zero reading.
func aggregatePctSamples(samples []float64) (*float64, *float64) {
	if len(samples) == 0 {
		return nil, nil
	}
	var sum, peak float64
	for _, v := range samples {
		sum += v
		if v > peak {
			peak = v
		}
	}
	avg := sum / float64(len(samples))
	return &avg, &peak
}

func (s *GPUScraper) loop(ctx context.Context) {
	defer close(s.done)

	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()

	// Scrape immediately on start.
	s.scrape(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scrape(ctx)
		}
	}
}

func (s *GPUScraper) scrape(ctx context.Context) {
	// Scrape vLLM metrics
	s.scrapeVLLM(ctx)

	// Scrape DCGM metrics if configured
	if s.dcgmURL != "" {
		s.scrapeDCGM(ctx)
	}
}

func (s *GPUScraper) scrapeVLLM(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.metricsURL, nil)
	if err != nil {
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[gpuscraper] vLLM scrape failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return
	}

	metrics := parsePrometheusMetricsExtended(resp.Body)

	s.mu.Lock()
	defer s.mu.Unlock()

	if metrics.utilization >= 0 {
		s.utilizationSample = append(s.utilizationSample, metrics.utilization)
	}
	if metrics.waiting >= 0 {
		s.waitingSamples = append(s.waitingSamples, metrics.waiting)
	}
	if metrics.running >= 0 {
		s.runningSamples = append(s.runningSamples, metrics.running)
	}

	// Track counter values (first and last)
	if metrics.promptTokens >= 0 {
		if s.firstPromptTokens < 0 {
			s.firstPromptTokens = metrics.promptTokens
		}
		s.lastPromptTokens = metrics.promptTokens
	}
	if metrics.genTokens >= 0 {
		if s.firstGenTokens < 0 {
			s.firstGenTokens = metrics.genTokens
		}
		s.lastGenTokens = metrics.genTokens
	}
	if metrics.prefixHits >= 0 {
		if s.firstPrefixHits < 0 {
			s.firstPrefixHits = metrics.prefixHits
		}
		s.lastPrefixHits = metrics.prefixHits
	}
	if metrics.prefixQueries >= 0 {
		if s.firstPrefixQueries < 0 {
			s.firstPrefixQueries = metrics.prefixQueries
		}
		s.lastPrefixQueries = metrics.prefixQueries
	}
	if metrics.preemptions >= 0 {
		if s.firstPreemptions < 0 {
			s.firstPreemptions = metrics.preemptions
		}
		s.lastPreemptions = metrics.preemptions
	}
	s.samplesCollected++
}

func (s *GPUScraper) scrapeDCGM(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.dcgmURL, nil)
	if err != nil {
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// DCGM may not be available on all nodes, don't log every failure
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return
	}

	dcgmMetrics := parseDCGMMetrics(resp.Body)

	s.mu.Lock()
	defer s.mu.Unlock()

	if dcgmMetrics.gpuUtil >= 0 {
		s.dcgmUtilSamples = append(s.dcgmUtilSamples, dcgmMetrics.gpuUtil)
	}
	if dcgmMetrics.memUsed >= 0 {
		s.dcgmMemSamples = append(s.dcgmMemSamples, dcgmMetrics.memUsed)
	}
	if dcgmMetrics.smActive >= 0 {
		s.dcgmSMActiveSamples = append(s.dcgmSMActiveSamples, dcgmMetrics.smActive)
	}
	if dcgmMetrics.tensorActive >= 0 {
		s.dcgmTensorActiveSamples = append(s.dcgmTensorActiveSamples, dcgmMetrics.tensorActive)
	}
	if dcgmMetrics.dramActive >= 0 {
		s.dcgmDRAMActiveSamples = append(s.dcgmDRAMActiveSamples, dcgmMetrics.dramActive)
	}
}

// promScrapeResult holds all metrics parsed from a single scrape.
type promScrapeResult struct {
	utilization   float64
	waiting       int
	running       int
	promptTokens  float64
	genTokens     float64
	prefixHits    float64
	prefixQueries float64
	preemptions   float64
}

// parsePrometheusMetricsExtended does a line-by-line parse of Prometheus
// text format to extract vLLM metrics. Returns -1 for values not found.
func parsePrometheusMetricsExtended(r io.Reader) promScrapeResult {
	result := promScrapeResult{
		utilization:   -1,
		waiting:       -1,
		running:       -1,
		promptTokens:  -1,
		genTokens:     -1,
		prefixHits:    -1,
		prefixQueries: -1,
		preemptions:   -1,
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		// vLLM exposes these metrics with possible label suffixes.
		// Match the metric name prefix.
		switch {
		case strings.HasPrefix(line, "vllm:kv_cache_usage_perc"),
			strings.HasPrefix(line, "vllm:gpu_cache_usage_perc"): // legacy name
			if v, err := parsePromValue(line); err == nil {
				result.utilization = v
			}
		case strings.HasPrefix(line, "vllm:num_requests_waiting"):
			if v, err := parsePromValue(line); err == nil {
				result.waiting = int(v)
			}
		case strings.HasPrefix(line, "vllm:num_requests_running"):
			if v, err := parsePromValue(line); err == nil {
				result.running = int(v)
			}
		case strings.HasPrefix(line, "vllm:prompt_tokens_total"):
			if v, err := parsePromValue(line); err == nil {
				result.promptTokens = v
			}
		case strings.HasPrefix(line, "vllm:generation_tokens_total"):
			if v, err := parsePromValue(line); err == nil {
				result.genTokens = v
			}
		case strings.HasPrefix(line, "vllm:prefix_cache_hits_total"),
			strings.HasPrefix(line, "vllm:prefix_cache_hit_total"): // legacy name
			if v, err := parsePromValue(line); err == nil {
				result.prefixHits = v
			}
		case strings.HasPrefix(line, "vllm:prefix_cache_queries_total"):
			if v, err := parsePromValue(line); err == nil {
				result.prefixQueries = v
			}
		case strings.HasPrefix(line, "vllm:num_preemptions_total"):
			if v, err := parsePromValue(line); err == nil {
				result.preemptions = v
			}
		}
	}
	return result
}

// parsePromValue extracts the float64 value from a Prometheus text line.
// The value is the last space-separated field (ignoring optional timestamp).
func parsePromValue(line string) (float64, error) {
	// Format: metric_name{labels} value [timestamp]
	// or:     metric_name value [timestamp]
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("too few fields")
	}
	// The value is always the second-to-last or last field.
	// Try the field right after the metric name (index 1).
	return strconv.ParseFloat(fields[len(fields)-1], 64)
}

// dcgmScrapeResult holds metrics parsed from DCGM exporter.
type dcgmScrapeResult struct {
	gpuUtil      float64 // DCGM_FI_DEV_GPU_UTIL (0-100)
	memUsed      float64 // DCGM_FI_DEV_FB_USED (bytes)
	smActive     float64 // DCGM_FI_PROF_SM_ACTIVE (0-100, scaled from 0.0-1.0)
	tensorActive float64 // DCGM_FI_PROF_PIPE_TENSOR_ACTIVE (0-100, scaled)
	dramActive   float64 // DCGM_FI_PROF_DRAM_ACTIVE (0-100, scaled)
}

// parseDCGMMetrics parses DCGM exporter Prometheus output.
// DCGM exposes per-GPU metrics with labels like gpu="0".
// We aggregate across all GPUs by averaging each metric.
func parseDCGMMetrics(r io.Reader) dcgmScrapeResult {
	result := dcgmScrapeResult{
		gpuUtil:      -1,
		memUsed:      -1,
		smActive:     -1,
		tensorActive: -1,
		dramActive:   -1,
	}

	var utilSum, memSum, smSum, tensorSum, dramSum float64
	var utilCount, smCount, tensorCount, dramCount int

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		// DCGM metrics we care about:
		// DCGM_FI_DEV_GPU_UTIL{gpu="0",...} 45.0  (percentage 0-100)
		// DCGM_FI_DEV_FB_USED{gpu="0",...} 12345678901  (bytes)
		// DCGM_FI_PROF_SM_ACTIVE{gpu="0",...} 0.72  (ratio 0.0-1.0)
		// DCGM_FI_PROF_PIPE_TENSOR_ACTIVE{gpu="0",...} 0.58  (ratio 0.0-1.0)
		// DCGM_FI_PROF_DRAM_ACTIVE{gpu="0",...} 0.34  (ratio 0.0-1.0)
		switch {
		case strings.HasPrefix(line, "DCGM_FI_DEV_GPU_UTIL"):
			if v, err := parsePromValue(line); err == nil {
				utilSum += v
				utilCount++
			}
		case strings.HasPrefix(line, "DCGM_FI_DEV_FB_USED"):
			if v, err := parsePromValue(line); err == nil {
				memSum += v
			}
		case strings.HasPrefix(line, "DCGM_FI_PROF_SM_ACTIVE"):
			if v, err := parsePromValue(line); err == nil {
				smSum += v * 100 // scale ratio → percent
				smCount++
			}
		case strings.HasPrefix(line, "DCGM_FI_PROF_PIPE_TENSOR_ACTIVE"):
			if v, err := parsePromValue(line); err == nil {
				tensorSum += v * 100
				tensorCount++
			}
		case strings.HasPrefix(line, "DCGM_FI_PROF_DRAM_ACTIVE"):
			if v, err := parsePromValue(line); err == nil {
				dramSum += v * 100
				dramCount++
			}
		}
	}

	// Average utilization across GPUs
	if utilCount > 0 {
		result.gpuUtil = utilSum / float64(utilCount)
	}
	if smCount > 0 {
		result.smActive = smSum / float64(smCount)
	}
	if tensorCount > 0 {
		result.tensorActive = tensorSum / float64(tensorCount)
	}
	if dramCount > 0 {
		result.dramActive = dramSum / float64(dramCount)
	}
	// Total memory used across GPUs
	if memSum > 0 {
		result.memUsed = memSum
	}

	return result
}
