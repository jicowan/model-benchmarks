package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// iceEvent builds an InsufficientCapacityError event on a NodeClaim, as Karpenter
// emits it (cluster-scoped object → default namespace).
func iceEvent(name, claimName string, ts time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: "default"},
		Reason:         "InsufficientCapacityError",
		InvolvedObject: corev1.ObjectReference{Kind: "NodeClaim", Name: claimName},
		LastTimestamp:  metav1.NewTime(ts),
	}
}

// TestPoolHitCapacityError covers the claim-name-churn race fix (correlate by
// pool-name prefix + recency, not by the current NodeClaim name set).
func TestPoolHitCapacityError(t *testing.T) {
	pool := "multinode-us-east-2a"
	waitStart := time.Now().Add(-capacityEventSkew)

	t.Run("ICE on a churned/deleted claim name trips via prefix", func(t *testing.T) {
		// The event references a claim that Karpenter already deleted+recreated;
		// only the "<pool>-<rand>" prefix is stable. Old code (matching the current
		// claim set) missed this; the prefix match catches it.
		client := k8sfake.NewSimpleClientset(
			iceEvent("ev-1", pool+"-dsn66", time.Now()),
		)
		o := New(client, database.NewMockRepo(), "pod")
		if !o.poolHitCapacityError(context.Background(), pool, waitStart) {
			t.Error("expected fail-fast to trip on an ICE event matching the pool prefix")
		}
	})

	t.Run("stale ICE from a prior run (before since) does NOT trip", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(
			iceEvent("ev-old", pool+"-oldxx", time.Now().Add(-10*time.Minute)),
		)
		o := New(client, database.NewMockRepo(), "pod")
		if o.poolHitCapacityError(context.Background(), pool, waitStart) {
			t.Error("a stale ICE event before the wait start must not trip fail-fast")
		}
	})

	t.Run("ICE for a DIFFERENT pool does not false-trip", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(
			iceEvent("ev-b", "multinode-us-east-2b-zzzzz", time.Now()),
		)
		o := New(client, database.NewMockRepo(), "pod")
		if o.poolHitCapacityError(context.Background(), pool, waitStart) {
			t.Error("an ICE event for another pool must not trip this pool's fail-fast")
		}
	})

	t.Run("no ICE events → no trip", func(t *testing.T) {
		o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
		if o.poolHitCapacityError(context.Background(), pool, waitStart) {
			t.Error("no events must not trip fail-fast")
		}
	})

	t.Run("prefix guards against a pool whose name is a prefix of another", func(t *testing.T) {
		// "multinode-us-east-2a" must not match a claim of "multinode-us-east-2a-extra"
		// style OTHER pool — but it SHOULD match its own "<pool>-<rand>". Here an
		// event for pool "multinode-us-east-2" (hypothetical shorter name) must not
		// trip "multinode-us-east-2a". Guarded by the trailing "-" in the prefix.
		client := k8sfake.NewSimpleClientset(
			iceEvent("ev-x", "multinode-us-east-2xyz", time.Now()),
		)
		o := New(client, database.NewMockRepo(), "pod")
		if o.poolHitCapacityError(context.Background(), pool, waitStart) {
			t.Error("a claim name without the exact '<pool>-' prefix must not trip")
		}
	})
}
