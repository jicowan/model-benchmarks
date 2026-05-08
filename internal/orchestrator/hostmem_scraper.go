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

// HostMemScraper samples the model pod's workingSet host memory via
// kubelet's /stats/summary endpoint and tracks the peak. It is intended
// to run during the load phase (deploy → readiness), which is when
// vLLM's weight loader produces the host-memory spike the recommender
// wants to calibrate against.
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
}

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

// Stop stops the scraper and returns the peak workingSet observed, in
// GiB. Returns 0 if no samples were collected.
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
	ticker := time.NewTicker(scrapeInterval)
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
				// WorkingSetBytes is the value OOMKiller compares against
				// the container's limit. Exactly the "peak I care about"
				// signal for the recommender.
				WorkingSetBytes int64 `json:"workingSetBytes"`
			} `json:"memory"`
		} `json:"containers"`
	} `json:"pods"`
}

// sampleOnce hits the kubelet proxy and returns the largest
// workingSetBytes across any pod matching our label in the target
// namespace+container. Returns 0 if the pod isn't present in the
// summary yet.
func (s *HostMemScraper) sampleOnce(ctx context.Context) (int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, scrapeTimeout)
	defer cancel()

	// Resolve which pod names match (kubelet's summary uses concrete
	// names, not labels). One HTTP list + one HTTP GET per scrape is
	// cheap and keeps the scraper resilient across pod rolls.
	pods, err := s.client.CoreV1().Pods(s.namespace).List(reqCtx, metav1.ListOptions{
		LabelSelector: s.podLabel,
	})
	if err != nil {
		return 0, fmt.Errorf("list pods: %w", err)
	}
	wantNames := make(map[string]bool, len(pods.Items))
	for _, p := range pods.Items {
		wantNames[p.Name] = true
	}
	if len(wantNames) == 0 {
		return 0, nil
	}

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
	for _, p := range summary.Pods {
		if p.PodRef.Namespace != s.namespace || !wantNames[p.PodRef.Name] {
			continue
		}
		for _, c := range p.Containers {
			if c.Name != s.containerName {
				continue
			}
			if c.Memory.WorkingSetBytes > peak {
				peak = c.Memory.WorkingSetBytes
			}
		}
	}
	return peak, nil
}
