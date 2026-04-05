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
	// Peak GPU cache utilization percentage (0-100).
	UtilizationPeakPct float64
	// Average GPU cache utilization percentage (0-100).
	UtilizationAvgPct float64
	// Peak memory usage in GiB, derived from cache utilization × total GPU memory.
	MemoryPeakGiB float64
	// Maximum number of waiting requests observed.
	WaitingRequestsMax int

	// Extended metrics (PRD-14)
	// Throughput breakdown
	PromptThroughputTPS     float64
	GenerationThroughputTPS float64
	// KV cache metrics (separate from utilization for clarity)
	KVCacheUtilizationAvgPct  float64
	KVCacheUtilizationPeakPct float64
	// Prefix cache
	PrefixCacheHitRate float64
	// Preemption count
	PreemptionCount int
	// Running requests
	RunningRequestsAvg float64
	RunningRequestsMax int
}

// GPUScraper periodically polls a vLLM Prometheus metrics endpoint and
// collects GPU utilization and queue depth samples.
type GPUScraper struct {
	metricsURL     string
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
}

// NewGPUScraper creates a scraper targeting the given vLLM service.
// totalMemoryGiB is the total GPU memory for the instance (used to
// derive peak memory from cache utilization percentage).
func NewGPUScraper(serviceHost string, port int, totalMemoryGiB float64) *GPUScraper {
	return &GPUScraper{
		metricsURL:     fmt.Sprintf("http://%s:%d/metrics", serviceHost, port),
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

	// Convert cache utilization to percentage (vLLM reports 0.0-1.0).
	peakPct := peak * 100
	avgPct := avg * 100
	memPeakGiB := peak * s.totalMemoryGiB

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

	return &GPUMetrics{
		UtilizationPeakPct:        peakPct,
		UtilizationAvgPct:         avgPct,
		MemoryPeakGiB:             memPeakGiB,
		WaitingRequestsMax:        maxWaiting,
		PromptThroughputTPS:       promptTPS,
		GenerationThroughputTPS:   genTPS,
		KVCacheUtilizationAvgPct:  avgPct,
		KVCacheUtilizationPeakPct: peakPct,
		PrefixCacheHitRate:        prefixHitRate,
		PreemptionCount:           preemptions,
		RunningRequestsAvg:        runningAvg,
		RunningRequestsMax:        maxRunning,
	}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.metricsURL, nil)
	if err != nil {
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[gpuscraper] scrape failed: %v", err)
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
		case strings.HasPrefix(line, "vllm:gpu_cache_usage_perc"):
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
		case strings.HasPrefix(line, "vllm:prefix_cache_hit_total"):
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
