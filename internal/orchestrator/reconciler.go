package orchestrator

import (
	"context"
	"log"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PRD-68 P6: leaked-resource reconciler.
//
// Orphan recovery (PRD-40) only acts on rows still in pending/running. A
// Deployment whose run row is already terminal — or gone — leaks until
// someone notices: e.g. markFailed succeeded but the 30s cleanupResources
// context expired under client-side throttling, or a cross-pod delete
// raced teardown. This loop closes that gap from the cluster side: list
// every object we stamp with accelbench/role, map it to its run through the
// accelbench/run-id label, and delete objects whose run is terminal or
// missing once they are older than reconcileGrace (so a run that finished a
// moment ago and is mid-teardown is left alone).
//
// Legacy objects (pre-PRD-68, no run-id label) are deleted only once older
// than legacyMaxAge — longer than any legitimate run can live
// (readinessTimeout + jobTimeout).

const (
	reconcileInterval = 5 * time.Minute
	reconcileGrace    = 10 * time.Minute
	legacyMaxAge      = readinessTimeout + jobTimeout + 30*time.Minute
	reconcileSelector = LabelRole + " in (model,loadgen,loadgen-config)"
)

// StartLeakReconcilerLoop runs reconcileLeaks every reconcileInterval under
// the reconciler advisory lock. Cancels with ctx.
func (o *Orchestrator) StartLeakReconcilerLoop(ctx context.Context) {
	go func() {
		// First pass after the recovery grace so heartbeats are established.
		select {
		case <-ctx.Done():
			return
		case <-time.After(recoveryGrace + reconcileInterval):
		}
		t := time.NewTicker(reconcileInterval)
		defer t.Stop()
		for {
			if _, err := o.repo.WithAdvisoryLock(ctx, database.LockKeyReconciler, o.reconcileLeaks); err != nil {
				log.Printf("[reconcile] pass failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// leakCandidate is one labelled object found in the namespace.
type leakCandidate struct {
	kind, name string
	runID      string // "" for legacy objects
	created    time.Time
}

// reconcileLeaks is one pass. Exported for tests via ReconcileLeaksOnce.
func (o *Orchestrator) reconcileLeaks(ctx context.Context) error {
	ns := defaultNamespace
	opts := metav1.ListOptions{LabelSelector: reconcileSelector}
	var cands []leakCandidate

	if deps, err := o.client.AppsV1().Deployments(ns).List(ctx, opts); err == nil {
		for _, d := range deps.Items {
			cands = append(cands, leakCandidate{"Deployment", d.Name, d.Labels[LabelRunID], d.CreationTimestamp.Time})
		}
	} else {
		return err
	}
	if svcs, err := o.client.CoreV1().Services(ns).List(ctx, opts); err == nil {
		for _, s := range svcs.Items {
			cands = append(cands, leakCandidate{"Service", s.Name, s.Labels[LabelRunID], s.CreationTimestamp.Time})
		}
	}
	if jobs, err := o.client.BatchV1().Jobs(ns).List(ctx, opts); err == nil {
		for _, j := range jobs.Items {
			cands = append(cands, leakCandidate{"Job", j.Name, j.Labels[LabelRunID], j.CreationTimestamp.Time})
		}
	}
	if cms, err := o.client.CoreV1().ConfigMaps(ns).List(ctx, opts); err == nil {
		for _, c := range cms.Items {
			cands = append(cands, leakCandidate{"ConfigMap", c.Name, c.Labels[LabelRunID], c.CreationTimestamp.Time})
		}
	}
	if len(cands) == 0 {
		return nil
	}

	// One ownership query for every distinct run id.
	idSet := map[string]bool{}
	for _, c := range cands {
		if c.runID != "" {
			idSet[c.runID] = true
		}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	own, err := o.repo.GetRunOwnership(ctx, ids)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, c := range cands {
		var reason string
		switch {
		case c.runID == "":
			if now.Sub(c.created) < legacyMaxAge {
				continue
			}
			reason = "legacy object older than any possible run"
		default:
			if now.Sub(c.created) < reconcileGrace {
				continue // may still be in normal teardown
			}
			ro, found := own[c.runID]
			if !found {
				reason = "run row deleted"
			} else if ro.Status != "pending" && ro.Status != "running" {
				reason = "run " + ro.Status
			} else {
				continue // live run, leave it
			}
		}
		log.Printf("[reconcile] deleting leaked %s/%s (%s)", c.kind, c.name, reason)
		o.deleteByKind(ctx, ns, c.kind, c.name)
	}
	return nil
}

// ReconcileLeaksOnce runs one reconciler pass synchronously (tests).
func (o *Orchestrator) ReconcileLeaksOnce(ctx context.Context) error { return o.reconcileLeaks(ctx) }

func (o *Orchestrator) deleteByKind(ctx context.Context, ns, kind, name string) {
	bg := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &bg}
	var err error
	switch kind {
	case "Deployment":
		err = o.client.AppsV1().Deployments(ns).Delete(ctx, name, opts)
	case "Service":
		err = o.client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
	case "Job":
		err = o.client.BatchV1().Jobs(ns).Delete(ctx, name, opts)
	case "ConfigMap":
		err = o.client.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{})
	}
	if err != nil {
		log.Printf("[reconcile] delete %s/%s: %v", kind, name, err)
	}
}
