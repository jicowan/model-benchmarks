package orchestrator

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PRD-56: the distributed-pool lock. Runs on the shared multi-node pool are
// serialized (one at a time), and a crashed owner must not leak p5 nodes. A
// single ConfigMap in the namespace is the cross-replica lock + liveness
// marker — no schema change (a PRD-56 non-goal), and it reuses PRD-40's
// owner-pod/heartbeat model: the lock names the owning pod, and orphan recovery
// treats a lock whose owner stopped heartbeating as reclaimable.
//
// Why a lock in addition to the LeaderWorkerSet? Because acquireDistributedPool
// scales nodes up BEFORE the LWS is deployed (so the per-AZ capacity fallback
// can try another pool). During that provisioning window there is no LWS yet,
// so "no LWS ⇒ idle" would be wrong. The lock exists for the whole run —
// created before the first scale-out — closing that window.

const distributedLockName = "accelbench-distributed-lock"

// acquireDistributedLock creates the lock ConfigMap owned by this pod. Returns
// an error if a lock already exists AND its owner is still live (another run
// holds the pool). A stale lock (owner not in livePods) is taken over.
func (o *Orchestrator) acquireDistributedLock(ctx context.Context, ns, modelName string, livePods []string) error {
	existing, err := o.client.CoreV1().ConfigMaps(ns).Get(ctx, distributedLockName, metav1.GetOptions{})
	if err == nil {
		owner := existing.Data["owner_pod"]
		if podIsLive(owner, livePods) {
			return fmt.Errorf("distributed pool held by run %q on pod %q", existing.Data["model"], owner)
		}
		// Stale lock — its owner died. Take it over.
		_ = o.client.CoreV1().ConfigMaps(ns).Delete(ctx, distributedLockName, metav1.DeleteOptions{})
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get distributed lock: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      distributedLockName,
			Namespace: ns,
			Labels: map[string]string{
				"accelbench/role": "distributed-lock",
			},
		},
		Data: map[string]string{
			"owner_pod": o.hostname,
			"model":     modelName,
		},
	}
	if _, err := o.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("distributed pool lock contended (raced with another run)")
		}
		return fmt.Errorf("create distributed lock: %w", err)
	}
	return nil
}

// releaseDistributedLock deletes the lock ConfigMap. Best-effort/idempotent.
func (o *Orchestrator) releaseDistributedLock(ctx context.Context, ns string) {
	_ = o.client.CoreV1().ConfigMaps(ns).Delete(ctx, distributedLockName, metav1.DeleteOptions{})
}

// distributedLockOwner returns the owning pod recorded in the lock, and whether
// a lock exists. Used by orphan recovery to decide whether a distributed run is
// still live.
func (o *Orchestrator) distributedLockOwner(ctx context.Context, ns string) (owner string, exists bool) {
	cm, err := o.client.CoreV1().ConfigMaps(ns).Get(ctx, distributedLockName, metav1.GetOptions{})
	if err != nil {
		return "", false
	}
	return cm.Data["owner_pod"], true
}

func podIsLive(pod string, livePods []string) bool {
	if pod == "" {
		return false
	}
	for _, p := range livePods {
		if p == pod {
			return true
		}
	}
	return false
}
