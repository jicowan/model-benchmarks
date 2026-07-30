package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// waitForNodes blocks until at least `count` nodes provisioned by NodePool
// `poolName` are Ready and carry the DRA label, or the timeout elapses.
func (o *Orchestrator) waitForNodes(ctx context.Context, poolName string, count int) error {
	deadline := time.Now().Add(nodeProvisionTimeout)
	for time.Now().Before(deadline) {
		ready := o.countReadyDRANodes(ctx, poolName)
		if ready >= count {
			log.Printf("[nodepool] %s: %d/%d nodes ready", poolName, ready, count)
			return nil
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
