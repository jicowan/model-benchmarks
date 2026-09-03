// Package oom provides OOM detection for Kubernetes pods.
package oom

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Detector monitors pods for OOM events.
type Detector struct {
	client    kubernetes.Interface
	namespace string
}

// NewDetector creates a new OOM detector.
func NewDetector(client kubernetes.Interface, namespace string) *Detector {
	return &Detector{
		client:    client,
		namespace: namespace,
	}
}

// CheckPod examines a pod for OOM indicators. Returns detected events (may
// be empty if no OOM detected). Fetches the pod by name; callers that
// already hold the Pod object (readiness loops that just listed pods)
// should use CheckPodObject to save an API round-trip per pod per tick.
func (d *Detector) CheckPod(ctx context.Context, podName string) ([]Event, error) {
	pod, err := d.client.CoreV1().Pods(d.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s: %w", podName, err)
	}
	return d.CheckPodObject(ctx, pod)
}

// CheckPodObject is CheckPod on an already-fetched Pod (PRD-68 P2). The
// Events list — the expensive call — is only issued when some container
// has actually terminated or restarted; a pod that is still pulling or
// starting has no OOM to find in Events.
func (d *Detector) CheckPodObject(ctx context.Context, pod *corev1.Pod) ([]Event, error) {
	var events []Event
	suspicious := false

	check := func(cs corev1.ContainerStatus) {
		if cs.RestartCount > 0 || cs.State.Terminated != nil || cs.LastTerminationState.Terminated != nil {
			suspicious = true
		}
		if ev := d.checkContainerStatus(pod, cs); ev != nil {
			events = append(events, *ev)
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		check(cs)
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		check(cs)
	}
	if !suspicious {
		return events, nil
	}

	// Check Kubernetes events for OOMKilling
	evList, err := d.client.CoreV1().Events(d.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", pod.Name),
	})
	if err == nil {
		for _, ev := range evList.Items {
			if ev.Reason == "OOMKilling" || ev.Reason == "OOMKilled" {
				events = append(events, Event{
					PodName:         pod.Name,
					ContainerName:   "", // Event may not specify container
					DetectionMethod: DetectionKubeEvent,
					Message:         ev.Message,
					OccurredAt:      ev.LastTimestamp.Time,
				})
			}
		}
	}

	return events, nil
}

// checkContainerStatus examines a single container's status for OOM indicators.
func (d *Detector) checkContainerStatus(pod *corev1.Pod, cs corev1.ContainerStatus) *Event {
	// Check for OOMKilled termination reason
	if cs.LastTerminationState.Terminated != nil {
		term := cs.LastTerminationState.Terminated
		if term.Reason == "OOMKilled" {
			return &Event{
				PodName:         pod.Name,
				ContainerName:   cs.Name,
				DetectionMethod: DetectionTerminationReason,
				ExitCode:        int(term.ExitCode),
				Message:         fmt.Sprintf("Container %s terminated with reason OOMKilled", cs.Name),
				OccurredAt:      term.FinishedAt.Time,
			}
		}

		// Check for exit code 137 (128 + SIGKILL(9))
		if term.ExitCode == 137 {
			return &Event{
				PodName:         pod.Name,
				ContainerName:   cs.Name,
				DetectionMethod: DetectionExitCode137,
				ExitCode:        137,
				Message:         fmt.Sprintf("Container %s exited with code 137 (SIGKILL, likely OOM)", cs.Name),
				OccurredAt:      term.FinishedAt.Time,
			}
		}
	}

	// Check current state if terminated
	if cs.State.Terminated != nil {
		term := cs.State.Terminated
		if term.Reason == "OOMKilled" {
			return &Event{
				PodName:         pod.Name,
				ContainerName:   cs.Name,
				DetectionMethod: DetectionTerminationReason,
				ExitCode:        int(term.ExitCode),
				Message:         fmt.Sprintf("Container %s terminated with reason OOMKilled", cs.Name),
				OccurredAt:      term.FinishedAt.Time,
			}
		}
		if term.ExitCode == 137 {
			return &Event{
				PodName:         pod.Name,
				ContainerName:   cs.Name,
				DetectionMethod: DetectionExitCode137,
				ExitCode:        137,
				Message:         fmt.Sprintf("Container %s exited with code 137 (SIGKILL, likely OOM)", cs.Name),
				OccurredAt:      term.FinishedAt.Time,
			}
		}
	}

	return nil
}

// MonitorRestarts tracks restart count changes during a benchmark run.
// Call StartMonitor before the benchmark and CheckRestarts periodically.
type RestartMonitor struct {
	podName           string
	initialRestarts   map[string]int32 // container name -> restart count
	startTime         time.Time
}

// StartMonitor initializes restart tracking for a pod.
func (d *Detector) StartMonitor(ctx context.Context, podName string) (*RestartMonitor, error) {
	pod, err := d.client.CoreV1().Pods(d.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s: %w", podName, err)
	}

	restarts := make(map[string]int32)
	for _, cs := range pod.Status.ContainerStatuses {
		restarts[cs.Name] = cs.RestartCount
	}

	return &RestartMonitor{
		podName:         podName,
		initialRestarts: restarts,
		startTime:       time.Now(),
	}, nil
}

// CheckRestarts compares current restart counts against initial values.
func (d *Detector) CheckRestarts(ctx context.Context, mon *RestartMonitor) ([]Event, error) {
	pod, err := d.client.CoreV1().Pods(d.namespace).Get(ctx, mon.podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s: %w", mon.podName, err)
	}

	var events []Event
	for _, cs := range pod.Status.ContainerStatuses {
		initial, ok := mon.initialRestarts[cs.Name]
		if !ok {
			continue
		}
		if cs.RestartCount > initial {
			events = append(events, Event{
				PodName:         mon.podName,
				ContainerName:   cs.Name,
				DetectionMethod: DetectionRestartIncrease,
				Message:         fmt.Sprintf("Container %s restarted %d times during benchmark (was %d, now %d)",
					cs.Name, cs.RestartCount-initial, initial, cs.RestartCount),
				OccurredAt:      time.Now(),
			})
		}
	}

	return events, nil
}

// GetSuggestion provides recommendations based on OOM history.
func GetSuggestion(history History) *Suggestion {
	if len(history.Events) == 0 {
		return nil
	}

	// Find the most recent configuration that OOM'd
	var lastEvent Event
	for _, ev := range history.Events {
		if ev.OccurredAt.After(lastEvent.OccurredAt) {
			lastEvent = ev
		}
	}

	suggestion := &Suggestion{}

	// Suggest reduced concurrency if OOM occurred
	if lastEvent.Concurrency > 1 {
		suggestion.ReduceConcurrency = true
		suggestion.SuggestedValue = lastEvent.Concurrency / 2
		if suggestion.SuggestedValue < 1 {
			suggestion.SuggestedValue = 1
		}
		suggestion.Message = fmt.Sprintf("Previous OOM at concurrency=%d. Consider reducing to %d or increasing runtime overhead.",
			lastEvent.Concurrency, suggestion.SuggestedValue)
	} else {
		suggestion.IncreaseOverhead = true
		suggestion.Message = "Previous OOM at concurrency=1. Consider increasing runtime overhead or using a larger instance."
	}

	return suggestion
}
