package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// PRD-40: heartbeat + ownership-aware orphan recovery.
//
// Every API pod writes its hostname + last-seen-at into api_pod_heartbeats
// every 10s. Orphan recovery compares each in-flight run/suite/seed's
// owner_pod against the list of pods whose heartbeats are fresh (within
// heartbeatTTL). Stale-owner rows are the recovery set.
//
// Why not startup-only recovery like before? Because every pod's startup
// used to flag every "running" row as orphaned, which wiped out runs owned
// by a live sibling during rolling deploys. Making recovery ownership-aware
// costs ~60s of recovery latency on a hard crash but eliminates the false
// positives.

const (
	heartbeatInterval = 10 * time.Second

	// heartbeatTTL: how stale a pod's heartbeat can be before it's
	// considered dead. 30s = 3 missed heartbeat intervals, which tolerates
	// transient DB blips without prematurely marking siblings dead.
	heartbeatTTL = 30 * time.Second
	// HeartbeatTTL is heartbeatTTL for callers outside the package (the API
	// server's model-cache recovery loop, PRD-68 P4).
	HeartbeatTTL = heartbeatTTL

	// recoveryGrace: wait this long after startup before running any
	// recovery scan. Lets newly-started sibling pods establish their own
	// heartbeats so a late-joining pod doesn't look at a stale snapshot.
	recoveryGrace = 60 * time.Second

	// recoveryInterval: how often the recovery loop scans for orphans
	// after the grace period.
	recoveryInterval = 30 * time.Second
)

// StartHeartbeatLoop writes this pod's heartbeat immediately, then every
// heartbeatInterval. Cancels when ctx is done.
func (o *Orchestrator) StartHeartbeatLoop(ctx context.Context) {
	// Immediate heartbeat so we show up in LiveAPIPods right away — before
	// any sibling pod's recovery scan runs.
	if err := o.repo.Heartbeat(ctx, o.hostname); err != nil {
		log.Printf("[heartbeat] initial write failed: %v", err)
	}
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := o.repo.Heartbeat(ctx, o.hostname); err != nil {
					log.Printf("[heartbeat] failed: %v", err)
				}
			}
		}
	}()
}

// StartOrphanRecoveryLoop runs the recovery scan after a grace period, then
// every recoveryInterval. Only takes action on rows whose owner_pod is NOT
// in the current LiveAPIPods list.
func (o *Orchestrator) StartOrphanRecoveryLoop(ctx context.Context) {
	go func() {
		// Grace period: let sibling pods establish heartbeats before we
		// declare anyone's run an orphan.
		select {
		case <-ctx.Done():
			return
		case <-time.After(recoveryGrace):
		}
		o.recoverOrphans(ctx)
		ticker := time.NewTicker(recoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.recoverOrphans(ctx)
			}
		}
	}()
}

// recoverOrphans is one pass of the scan: fetch live pods, ask the repo for
// every in-flight row whose owner isn't in that list, then mark each failed
// and clean up its Kubernetes resources. Called from the loop; also safe to
// call ad-hoc from tests.
func (o *Orchestrator) recoverOrphans(ctx context.Context) {
	// PRD-68 P3: exactly one replica runs a recovery pass at a time. Without
	// the lock every replica scanned and acted on the same orphans (idempotent
	// deletes, but duplicated cost computation and log noise, and a real race
	// on which pod's "failed" message lands).
	ran, err := o.repo.WithAdvisoryLock(ctx, database.LockKeyOrphanRecovery, o.recoverOrphansLocked)
	if err != nil {
		log.Printf("[recovery] pass failed: %v", err)
	}
	_ = ran
}

func (o *Orchestrator) recoverOrphansLocked(ctx context.Context) error {
	live, err := o.repo.LiveAPIPods(ctx, heartbeatTTL)
	if err != nil {
		return fmt.Errorf("query live pods: %w", err)
	}

	// Benchmark runs. PRD-68 P3: try to salvage a finished loadgen's S3
	// summary before declaring the run failed (the owner may have died in the
	// persist step).
	if orphans, err := o.repo.GetOrphanedRuns(ctx, live); err == nil {
		for _, r := range orphans {
			owner := "unknown"
			if r.OwnerPod != nil {
				owner = *r.OwnerPod
			}
			log.Printf("[recovery] orphan run %s (owner=%s, status=%s) — salvaging or failing",
				r.ID[:8], owner, r.Status)
			o.adm.recovered.Add(1)
			o.salvageOrFail(ctx, r.ID, orphanFailureMessage(owner))
		}
	} else {
		log.Printf("[recovery] query orphan runs: %v", err)
	}

	// Suite runs — same idea, with the suite-specific cleanup helper.
	if orphans, err := o.repo.GetOrphanedSuiteRuns(ctx, live); err == nil {
		for _, s := range orphans {
			owner := "unknown"
			if s.OwnerPod != nil {
				owner = *s.OwnerPod
			}
			// test_suite_runs has no error_message column (only scenario_results
			// does), so the user-facing orphan explanation lives only in the
			// log line above. Status flips to "failed"; currentScenario nil.
			log.Printf("[recovery] orphan suite %s (owner=%s, status=%s) — %s",
				s.ID[:8], owner, s.Status, orphanFailureMessage(owner))
			o.adm.recovered.Add(1)
			_ = o.repo.UpdateSuiteRunStatus(ctx, s.ID, "failed", nil)
			o.CleanupSuiteResources(s.ID)
		}
	} else {
		log.Printf("[recovery] query orphan suites: %v", err)
	}

	// Seeds — no K8s resources to clean up, just flip the status row.
	if orphans, err := o.repo.GetOrphanedSeeds(ctx, live); err == nil {
		for _, s := range orphans {
			owner := ""
			if s.OwnerPod != nil {
				owner = *s.OwnerPod
			}
			log.Printf("[recovery] orphan seed %s (owner=%s) — marking interrupted",
				s.ID, owner)
			_ = o.repo.FailCatalogSeedStatus(ctx, s.ID, "orphaned: owner pod stopped heartbeating")
		}
	} else {
		log.Printf("[recovery] query orphan seeds: %v", err)
	}

	// Belt-and-braces cleanup of ancient heartbeat rows. Rows older than
	// 2×TTL are guaranteed-dead.
	if err := o.repo.DeleteStaleHeartbeats(ctx, 2*heartbeatTTL); err != nil {
		log.Printf("[recovery] delete stale heartbeats: %v", err)
	}

	// PRD-56 (critical): reclaim leaked distributed pool capacity. If the pod
	// that owned a distributed run died, its in-memory distributedState (and
	// thus its scale-in) died with it — the p5 nodes would leak real money.
	// This runs on the surviving sibling.
	o.reapLeakedDistributedPools(ctx, live)
	return nil
}

// reapLeakedDistributedPools scales every static multinode NodePool back to 0
// when no LIVE distributed run holds the lock. Belt-and-suspenders for the
// deferred teardown: it survives owner-pod crashes because it reads live
// cluster state (the lock ConfigMap + heartbeats) rather than in-memory run
// state. The lock — held for the WHOLE run including the pre-LWS provisioning
// window — is what prevents this from racing a legitimate scale-out. Safe to
// run every recovery pass; scaling an already-0 pool to 0 is a no-op.
func (o *Orchestrator) reapLeakedDistributedPools(ctx context.Context, livePods []string) {
	if o.dynClient == nil {
		return
	}
	// A lock owned by a live pod ⇒ a distributed run is legitimately active
	// (serving OR still provisioning nodes). Leave the pools alone.
	if owner, exists := o.distributedLockOwner(ctx, defaultNamespace); exists {
		if podIsLive(owner, livePods) {
			return
		}
		// Dead owner: clear the stale lock so the pool is reclaimable and the
		// next run isn't blocked by a ghost holder.
		log.Printf("[recovery] distributed lock owner %q is dead — releasing stale lock", owner)
		o.releaseDistributedLock(ctx, defaultNamespace)
	}
	pools, err := o.selectMultinodePool(ctx, "")
	if err != nil {
		return // no multinode pools (enable_multinode off) — nothing to reap.
	}
	for _, pool := range pools {
		n := o.countReadyDRANodes(ctx, pool)
		if n == 0 {
			continue // already drained; avoid a pointless patch/log line.
		}
		log.Printf("[recovery] no live distributed run but pool %s has %d node(s) — scaling to 0", pool, n)
		if err := o.scaleNodePool(ctx, pool, 0); err != nil {
			log.Printf("[recovery] scale %s to 0: %v", pool, err)
		}
		// Restore the broad instance-category so an orphaned run's pin doesn't
		// outlive it and narrow the next run's provisioning.
		if err := o.resetNodePoolInstanceType(ctx, pool); err != nil {
			log.Printf("[recovery] reset %s instance-category: %v", pool, err)
		}
	}
}

// orphanFailureMessage is the error_message persisted on a run / suite row
// when recovery marks it failed because the owning pod stopped heartbeating.
// Surfaced verbatim in the UI, so keep it short and user-actionable: the
// important thing is that the user knows the failure wasn't their fault
// and re-submitting is the fix.
func orphanFailureMessage(ownerPod string) string {
	return "API pod " + ownerPod + " stopped responding before the run finished — re-submit to retry"
}
