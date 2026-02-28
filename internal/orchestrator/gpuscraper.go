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
	cancel            context.CancelFunc
	done              chan struct{}
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
		done: make(chan struct{}),
	}
}

// Start begins scraping in a background goroutine. It is safe to call
// Start only once.
func (s *GPUScraper) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go s.loop(ctx)
}

// Stop stops the scraper and returns the aggregated GPU metrics.
// Returns nil if no samples were collected.
func (s *GPUScraper) Stop() *GPUMetrics {
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

	// Convert cache utilization to percentage (vLLM reports 0.0-1.0).
	peakPct := peak * 100
	avgPct := avg * 100
	memPeakGiB := peak * s.totalMemoryGiB

	return &GPUMetrics{
		UtilizationPeakPct: peakPct,
		UtilizationAvgPct:  avgPct,
		MemoryPeakGiB:      memPeakGiB,
		WaitingRequestsMax: maxWaiting,
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

	utilization, waiting := parsePrometheusMetrics(resp.Body)

	s.mu.Lock()
	defer s.mu.Unlock()

	if utilization >= 0 {
		s.utilizationSample = append(s.utilizationSample, utilization)
	}
	if waiting >= 0 {
		s.waitingSamples = append(s.waitingSamples, waiting)
	}
}

// parsePrometheusMetrics does a simple line-by-line parse of Prometheus
// text format to extract vllm:gpu_cache_usage_perc and
// vllm:num_requests_waiting. Returns -1 for values not found.
func parsePrometheusMetrics(r io.Reader) (utilization float64, waiting int) {
	utilization = -1
	waiting = -1

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		// vLLM exposes these metrics with possible label suffixes.
		// Match the metric name prefix.
		if strings.HasPrefix(line, "vllm:gpu_cache_usage_perc") {
			if v, err := parsePromValue(line); err == nil {
				utilization = v
			}
		} else if strings.HasPrefix(line, "vllm:num_requests_waiting") {
			if v, err := parsePromValue(line); err == nil {
				waiting = int(v)
			}
		}
	}
	return utilization, waiting
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
