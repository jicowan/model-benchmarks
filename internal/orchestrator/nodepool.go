package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// PRD-56 Layer 3: scale the PRD-55 static multi-node Karpenter NodePool in/out
// per distributed run. The pool sits at spec.replicas:0 at rest; a run brings
// it up to node_count for the run's duration and returns it to 0 on teardown.
//
// One shared pool ⇒ one distributed run at a time (serialized by the DB lock in
// distributed.go). Leaked scale-out burns real p5 money, so scale-in must
// survive crashes — orphan recovery resets replicas to 0 (see distributed.go).

const (
	// draNodeLabel marks nodes where DRANET + the NVIDIA DRA driver have
	// landed (set on the PRD-55 static NodePool template). We only consider
	// a node "ready for a distributed run" once it carries this label AND is
	// Ready — otherwise a pod could schedule before DRA can allocate GPUs.
	draNodeLabel = "accelbench.io/dra"

	// nodePoolLabel is the Karpenter-managed label identifying which NodePool
	// provisioned a node. Used to count nodes belonging to the scaled pool.
	nodePoolLabel = "karpenter.sh/nodepool"

	// nodeProvisionTimeout bounds the 0→N scale-out wait. p5 boot + init +
	// DRA/DRANET readiness takes minutes; this is separate from (and precedes)
	// the model-load readiness budget.
	nodeProvisionTimeout = 20 * time.Minute
	nodeProvisionPoll    = 15 * time.Second

	// capacityEventSkew widens the fail-fast recency window slightly before the
	// wait's start, so an InsufficientCapacityError that lands in the gap between
	// scale-out and waitForNodes beginning still counts (clock skew between the
	// API server's event timestamps and this pod is also absorbed here).
	capacityEventSkew = 30 * time.Second
)

// scaleNodePool patches a static NodePool's spec.replicas via JSON merge patch
// (the same idiom PRD-33 uses for capacity-reservation edits). NodePool is a
// cluster-scoped Karpenter CRD, so no namespace.
func (o *Orchestrator) scaleNodePool(ctx context.Context, name string, replicas int) error {
	if o.dynClient == nil {
		return fmt.Errorf("dynamic client not configured; cannot scale NodePool %q", name)
	}
	body := map[string]any{"spec": map[string]any{"replicas": replicas}}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = o.dynClient.Resource(gvrNodePool).Patch(ctx, name, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-orchestrator"})
	if err != nil {
		return fmt.Errorf("patch NodePool %q replicas=%d: %w", name, replicas, err)
	}
	log.Printf("[nodepool] scaled %s to replicas=%d", name, replicas)
	return nil
}

// setNodePoolInstanceType sets the static multinode pool's
// node.kubernetes.io/instance-type requirement to exactly the run's selected
// type, BEFORE scale-out. A static pool provisions its nodes from the POOL's
// requirements (not the pods' — static pools skip pod-driven scheduling, the
// DRA-compat tradeoff), so this is what makes the run form's instance choice
// actually drive provisioning. The pods carry a matching instance-type
// nodeSelector, so pool and pods agree by construction.
//
// It reads the current requirements, replaces (or appends) the instance-type
// key, and writes the whole spec.template.spec.requirements list back (a merge
// patch can't edit one array element). Other requirement keys (arch, zone,
// capacity-type) are preserved. Best-effort intent: on any read/shape error it
// returns an error so the caller can surface it rather than silently provision
// the wrong type.
func (o *Orchestrator) setNodePoolInstanceType(ctx context.Context, name, instanceType string) error {
	if o.dynClient == nil {
		return fmt.Errorf("dynamic client not configured; cannot set NodePool %q instance type", name)
	}
	if instanceType == "" {
		return nil // nothing to pin
	}
	np, err := o.dynClient.Resource(gvrNodePool).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get NodePool %q: %w", name, err)
	}
	reqs, found, err := unstructured.NestedSlice(np.Object, "spec", "template", "spec", "requirements")
	if err != nil || !found {
		return fmt.Errorf("NodePool %q has no requirements list", name)
	}
	// Rebuild the list: drop any instance-type / instance-category constraint,
	// then append the run's exact type. Preserves arch / zone / capacity-type.
	const itKey = "node.kubernetes.io/instance-type"
	const catKey = "karpenter.k8s.aws/instance-category"
	out := make([]any, 0, len(reqs)+1)
	for _, r := range reqs {
		m, ok := r.(map[string]any)
		if !ok {
			out = append(out, r)
			continue
		}
		if key, _ := m["key"].(string); key == itKey || key == catKey {
			continue // drop old instance-type / family-category — the run pins one type
		}
		out = append(out, r)
	}
	out = append(out, map[string]any{
		"key":      itKey,
		"operator": "In",
		"values":   []any{instanceType},
	})
	// Merge-patch the whole requirements array (not Update) so we only need the
	// "patch" verb the orchestrator SA already holds for the replicas scale — a
	// merge patch replaces the array wholesale, which is exactly what we want.
	body := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{"requirements": out},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = o.dynClient.Resource(gvrNodePool).Patch(ctx, name, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-orchestrator"})
	if err != nil {
		return fmt.Errorf("patch NodePool %q instance type=%s: %w", name, instanceType, err)
	}
	log.Printf("[nodepool] set %s instance-type=%s", name, instanceType)
	return nil
}

// defaultMultinodeCategories is the at-rest instance-category constraint the
// static multinode pools carry between runs. A run narrows the pool to one
// exact instance-type (setNodePoolInstanceType); teardown/recovery restores
// this broad category set so the pool is not left pinned to the last run's
// type (which would silently constrain the NEXT run's auto-provisioning and
// hide capacity in other families). "g" and "p" cover the GPU families the
// multinode pools draw from.
var defaultMultinodeCategories = []any{"g", "p"}

// resetNodePoolInstanceType restores a static multinode pool's requirements to
// the broad at-rest state: it drops the run's pinned instance-type (and any
// stale category key) and re-adds the instance-category In [g,p] constraint.
// This is the inverse of setNodePoolInstanceType and runs on teardown (success
// OR failure) and in orphan recovery, so a pool is never left pinned to the
// last run's exact type. Best-effort — the merge patch replaces the whole
// requirements array (only the "patch" verb the SA already holds is needed).
func (o *Orchestrator) resetNodePoolInstanceType(ctx context.Context, name string) error {
	if o.dynClient == nil {
		return fmt.Errorf("dynamic client not configured; cannot reset NodePool %q", name)
	}
	np, err := o.dynClient.Resource(gvrNodePool).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get NodePool %q: %w", name, err)
	}
	reqs, found, err := unstructured.NestedSlice(np.Object, "spec", "template", "spec", "requirements")
	if err != nil || !found {
		return fmt.Errorf("NodePool %q has no requirements list", name)
	}
	const itKey = "node.kubernetes.io/instance-type"
	const catKey = "karpenter.k8s.aws/instance-category"
	out := make([]any, 0, len(reqs)+1)
	for _, r := range reqs {
		m, ok := r.(map[string]any)
		if !ok {
			out = append(out, r)
			continue
		}
		if key, _ := m["key"].(string); key == itKey || key == catKey {
			continue // drop the run's pinned type / any stale category — re-add below
		}
		out = append(out, r)
	}
	out = append(out, map[string]any{
		"key":      catKey,
		"operator": "In",
		"values":   append([]any(nil), defaultMultinodeCategories...),
	})
	body := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{"requirements": out},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = o.dynClient.Resource(gvrNodePool).Patch(ctx, name, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-orchestrator"})
	if err != nil {
		return fmt.Errorf("patch NodePool %q reset categories: %w", name, err)
	}
	log.Printf("[nodepool] reset %s instance-category=%v", name, defaultMultinodeCategories)
	return nil
}

// errInsufficientCapacity signals that a pool cannot provision its nodes because
// AWS has no capacity for the requested instance type in that AZ. The caller
// (acquireDistributedPool) treats it as a fast fallthrough to the next AZ pool
// rather than waiting out the full provision timeout.
var errInsufficientCapacity = fmt.Errorf("insufficient capacity")

// setNodePoolNetworkMode points the static pool's nodeClassRef at the EC2NodeClass
// matching the run's network mode, BEFORE scale-out. The EFA node class
// (multinode-<az>) declares an efa-only interface → Karpenter launches ONLY
// EFA-capable (scarce, large) instances; the shared TCP node class
// ("multinode-tcp") omits it → any GPU instance (e.g. g6.2xlarge) can launch.
//
// The TCP node class is a SINGLE shared class (not per-AZ): AZ placement is
// enforced by the NodePool's own topology.kubernetes.io/zone requirement, and
// the TCP class uses tag-based subnet discovery (all AZ subnets carry the
// karpenter.sh/discovery tag), so Karpenter picks the subnet matching the pool's
// zone. (The per-AZ EFA classes exist because they pin a specific PG + subnet for
// RDMA co-location; TCP needs neither.) Safe because distributed runs serialize.
func (o *Orchestrator) setNodePoolNetworkMode(ctx context.Context, poolName, networkMode string) error {
	if o.dynClient == nil {
		return fmt.Errorf("dynamic client not configured; cannot set NodePool %q node class", poolName)
	}
	// Default (efa) → the pool's own per-AZ EFA class; tcp → the shared TCP class.
	className := poolName
	if networkMode == NetworkModeTCP {
		className = "multinode-tcp"
	}
	body := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"nodeClassRef": map[string]any{
						"group": "karpenter.k8s.aws",
						"kind":  "EC2NodeClass",
						"name":  className,
					},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = o.dynClient.Resource(gvrNodePool).Patch(ctx, poolName, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-orchestrator"})
	if err != nil {
		return fmt.Errorf("patch NodePool %q nodeClassRef=%s: %w", poolName, className, err)
	}
	log.Printf("[nodepool] set %s nodeClassRef=%s (network=%s)", poolName, className, networkMode)
	return nil
}

// waitForNodes blocks until at least `count` nodes provisioned by NodePool
// `poolName` are Ready and carry the DRA label, or the timeout elapses. It
// FAILS FAST with errInsufficientCapacity when Karpenter reports it can't launch
// the instance type in this AZ — so the caller can try the next AZ in seconds
// instead of grinding through the full nodeProvisionTimeout (a run pins one
// instance type, so a capacity error won't self-resolve within this pool).
func (o *Orchestrator) waitForNodes(ctx context.Context, poolName string, count int) error {
	// Capacity events are only considered relevant if they occur at/after the
	// wait starts, so a stale InsufficientCapacityError from a PRIOR run of this
	// same (static-named) pool can't trip an immediate false fail-fast. A small
	// skew allowance covers an ICE that landed in the moment between scale-out
	// and this wait beginning.
	waitStart := time.Now().Add(-capacityEventSkew)
	deadline := time.Now().Add(nodeProvisionTimeout)
	for time.Now().Before(deadline) {
		ready := o.countReadyDRANodes(ctx, poolName)
		if ready >= count {
			log.Printf("[nodepool] %s: %d/%d nodes ready", poolName, ready, count)
			return nil
		}
		// Fast-fail on an AWS capacity shortage: no point waiting 20 min for a
		// node that can't launch. Only trip when NO node is ready yet (a partial
		// scale-out that's mid-provision shouldn't be aborted on a stale event).
		if ready == 0 && o.poolHitCapacityError(ctx, poolName, waitStart) {
			log.Printf("[nodepool] %s: insufficient capacity — failing fast to try the next AZ", poolName)
			return errInsufficientCapacity
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(nodeProvisionPoll):
		}
	}
	return fmt.Errorf("NodePool %q: only %d of %d nodes became ready after %v",
		poolName, o.countReadyDRANodes(ctx, poolName), count, nodeProvisionTimeout)
}

// poolHitCapacityError reports whether the pool has hit an InsufficientCapacityError
// (Karpenter's signal that the AZ can't launch the requested instance type) at or
// after `since`.
//
// Correlation is by the pool-name PREFIX on the event's involvedObject.name, NOT
// by the current set of NodeClaim names. On a capacity error Karpenter DELETES the
// failed NodeClaim and creates a fresh one with a new random name (observed live:
// <pool>-dsn66 → deleted → <pool>-jcbn6 → deleted → …). Every claim of a static
// pool is named "<poolName>-<rand>", so the prefix is stable across that churn,
// whereas listing "current" claims and matching event.involvedObject.Name against
// them races the delete/recreate and usually misses — the bug this fixes.
//
// The `since` recency window rejects a stale capacity event left over from a PRIOR
// run of the same (persistent, static-named) pool, which would otherwise trip an
// immediate false fail-fast on the next run.
func (o *Orchestrator) poolHitCapacityError(ctx context.Context, poolName string, since time.Time) bool {
	// NodeClaim events for cluster-scoped objects land in the default namespace.
	events, err := o.client.CoreV1().Events("default").List(ctx, metav1.ListOptions{
		FieldSelector: "reason=InsufficientCapacityError",
	})
	if err != nil {
		return false
	}
	prefix := poolName + "-"
	for _, ev := range events.Items {
		if !strings.HasPrefix(ev.InvolvedObject.Name, prefix) {
			continue
		}
		if capacityEventTime(&ev).Before(since) {
			continue // stale event from an earlier run of this static pool
		}
		return true
	}
	return false
}

// capacityEventTime returns the most recent timestamp on an Event, tolerating the
// several time fields Kubernetes may populate (Series.LastObservedTime for
// aggregated events, LastTimestamp/EventTime for the classic path, else the
// object's creation time).
func capacityEventTime(ev *corev1.Event) time.Time {
	if ev.Series != nil && !ev.Series.LastObservedTime.IsZero() {
		return ev.Series.LastObservedTime.Time
	}
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}

// countReadyDRANodes counts Ready nodes from the given NodePool that carry the
// DRA-ready label.
func (o *Orchestrator) countReadyDRANodes(ctx context.Context, poolName string) int {
	nodes, err := o.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=true", nodePoolLabel, poolName, draNodeLabel),
	})
	if err != nil {
		log.Printf("[nodepool] list nodes for %s: %v", poolName, err)
		return 0
	}
	ready := 0
	for _, n := range nodes.Items {
		if isNodeReady(&n) {
			ready++
		}
	}
	return ready
}

func isNodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
