package orchestrator

import (
	"context"
	"log"
	"time"
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
	live, err := o.repo.LiveAPIPods(ctx, heartbeatTTL)
	if err != nil {
		log.Printf("[recovery] query live pods: %v", err)
		return
	}

	// Benchmark runs — reuse the same markFailed + cleanupResources path
	// used by single-run failures today.
	if orphans, err := o.repo.GetOrphanedRuns(ctx, live); err == nil {
		for _, r := range orphans {
			owner := ""
			if r.OwnerPod != nil {
				owner = *r.OwnerPod
			}
			log.Printf("[recovery] orphan run %s (owner=%s, status=%s) — marking failed",
				r.ID[:8], owner, r.Status)
			o.markFailed(ctx, r.ID, "orphaned run: owner pod stopped heartbeating")
			o.cleanupResources(ctx, r.ID)
		}
	} else {
		log.Printf("[recovery] query orphan runs: %v", err)
	}

	// Suite runs — same idea, with the suite-specific cleanup helper.
	if orphans, err := o.repo.GetOrphanedSuiteRuns(ctx, live); err == nil {
		for _, s := range orphans {
			owner := ""
			if s.OwnerPod != nil {
				owner = *s.OwnerPod
			}
			log.Printf("[recovery] orphan suite %s (owner=%s, status=%s) — marking failed",
				s.ID[:8], owner, s.Status)
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
}
