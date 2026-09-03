package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// HostMemScraper samples the model pod's RSS host memory via kubelet's
// /stats/summary endpoint and tracks the peak. It is intended to run
// during the load phase (deploy → readiness), which is when vLLM's
// weight loader produces the host-memory spike the recommender wants
// to calibrate against.
//
// We intentionally use rssBytes rather than workingSetBytes. The S3
// streamer path reads every weight shard through the page cache; on
// hosts with spare RAM those pages linger, inflating workingSet by
// the model's on-disk size even though the pages are reclaimable and
// irrelevant to OOMKill. RSS better reflects "what would push us over
// the memory limit."
//
// The scraper proxies through the apiserver
// (/api/v1/nodes/{node}/proxy/stats/summary) using the existing
// client-go SA — no new RBAC beyond the nodes/proxy verb that Helm
// already grants the orchestrator's ServiceAccount.
type HostMemScraper struct {
	client        kubernetes.Interface
	nodeName      string
	namespace     string
	podLabel      string // app.kubernetes.io/name=<modelName>
	containerName string // usually "vllm"

	mu       sync.Mutex
	peakByte int64
	cancel   context.CancelFunc
	done     chan struct{}

	// wantNames caches the pod names matching podLabel so the per-tick
	// Pods.List is skipped once the pod set is known. Re-resolved when the
	// kubelet summary stops reporting any cached name (pod rolled).
	wantNames map[string]bool
}

// hostMemScrapeInterval is the host-RSS sampling period. The weight-load
// spike the recommender calibrates against lasts minutes, so 15s resolution
// loses nothing while cutting the per-run apiserver proxy traffic (a full
// kubelet /stats/summary payload per sample) by 3x versus the 5s GPU-metrics
// cadence (PRD-68 P2).
const hostMemScrapeInterval = 15 * time.Second

// NewHostMemScraper returns a scraper for the given model pod. The pod
// is identified by its `app.kubernetes.io/name` label rather than a
// specific name, because Deployments roll their replicasets; the label
// stays stable across rolls.
func NewHostMemScraper(client kubernetes.Interface, namespace, podLabel, containerName string) *HostMemScraper {
	return &HostMemScraper{
		client:        client,
		namespace:     namespace,
		podLabel:      podLabel,
		containerName: containerName,
		done:          make(chan struct{}),
	}
}

// Start begins scraping in a background goroutine. Start should be
// called once. Early calls (before the pod is scheduled) log a warning
// and keep retrying; the scraper starts recording as soon as the pod
// lands on a node.
func (s *HostMemScraper) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go s.loop(ctx)
}

// Stop stops the scraper and returns the peak RSS observed, in GiB.
// Returns 0 if no samples were collected.
func (s *HostMemScraper) Stop() float64 {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peakByte == 0 {
		return 0
	}
	return float64(s.peakByte) / (1024 * 1024 * 1024)
}

func (s *HostMemScraper) loop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(hostMemScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Resolve the node name if we don't have one yet.
			if s.nodeName == "" {
				s.nodeName = s.resolveNodeName(ctx)
				if s.nodeName == "" {
					continue // not scheduled yet; try again next tick
				}
			}
			if peak, err := s.sampleOnce(ctx); err != nil {
				log.Printf("hostmem scraper: %v", err)
			} else if peak > 0 {
				s.mu.Lock()
				if peak > s.peakByte {
					s.peakByte = peak
				}
				s.mu.Unlock()
			}
		}
	}
}

// resolveNodeName returns the node name for the pod matching podLabel.
// Empty string if no running/scheduled pod is found yet.
func (s *HostMemScraper) resolveNodeName(ctx context.Context) string {
	pods, err := s.client.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: s.podLabel,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != "" {
			return pod.Spec.NodeName
		}
	}
	return ""
}

// statsSummary is the subset of the kubelet /stats/summary response we
// actually read. Kubelet serves a much larger payload; we decode only
// the pods.containers.memory path.
type statsSummary struct {
	Pods []struct {
		PodRef struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"podRef"`
		Containers []struct {
			Name   string `json:"name"`
			Memory struct {
				// RSSBytes is the container's resident set size — anonymous
				// pages + stack + unreclaimable kernel memory. This is what
				// OOMKiller actually compares against the limit.
				//
				// We previously recorded WorkingSetBytes but that includes
				// active page cache. The Run:ai streamer reads every weight
				// shard through the page cache once; on hosts with spare
				// RAM the kernel doesn't reclaim those pages, so peak
				// workingSet ≈ full model size + RSS, even though none of
				// the cache is OOM-relevant. Observed on Qwen3-14B / 384
				// GiB host: workingSet peaked at 121 GiB, RSS stayed
				// proportional to actual process usage.
				RSSBytes int64 `json:"rssBytes"`
			} `json:"memory"`
		} `json:"containers"`
	} `json:"pods"`
}

// sampleOnce hits the kubelet proxy and returns the largest rssBytes
// across any pod matching our label in the target namespace+container.
// Returns 0 if the pod isn't present in the summary yet.
func (s *HostMemScraper) sampleOnce(ctx context.Context) (int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, scrapeTimeout)
	defer cancel()

	// Resolve which pod names match (kubelet's summary uses concrete
	// names, not labels). Cached after the first successful resolution;
	// re-listed only when the summary no longer contains any cached name
	// (see below), which keeps the scraper resilient across pod rolls
	// without a List per sample.
	if len(s.wantNames) == 0 {
		if err := s.refreshPodNames(reqCtx); err != nil {
			return 0, err
		}
		if len(s.wantNames) == 0 {
			return 0, nil
		}
	}
	wantNames := s.wantNames

	raw, err := s.client.CoreV1().RESTClient().Get().
		AbsPath("api", "v1", "nodes", s.nodeName, "proxy", "stats", "summary").
		DoRaw(reqCtx)
	if err != nil {
		return 0, fmt.Errorf("stats/summary: %w", err)
	}

	var summary statsSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return 0, fmt.Errorf("parse stats/summary: %w", err)
	}

	var peak int64
	seen := false
	for _, p := range summary.Pods {
		if p.PodRef.Namespace != s.namespace || !wantNames[p.PodRef.Name] {
			continue
		}
		seen = true
		for _, c := range p.Containers {
			if c.Name != s.containerName {
				continue
			}
			if c.Memory.RSSBytes > peak {
				peak = c.Memory.RSSBytes
			}
		}
	}
	if !seen {
		// None of the cached pods is on this node any more — the Deployment
		// rolled. Drop the cache (and node) so the next tick re-resolves.
		s.wantNames = nil
		s.nodeName = ""
	}
	return peak, nil
}

// refreshPodNames re-lists pods matching podLabel into wantNames.
func (s *HostMemScraper) refreshPodNames(ctx context.Context) error {
	pods, err := s.client.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: s.podLabel,
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	names := make(map[string]bool, len(pods.Items))
	for _, p := range pods.Items {
		names[p.Name] = true
	}
	s.wantNames = names
	return nil
}
