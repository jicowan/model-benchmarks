package orchestrator

import (
	"context"
	"log"
	"time"
)

// cancelPollInterval is how often the owning pod's goroutine asks the DB
// whether a cancel has been requested. 5 seconds is the accepted worst-case
// cancel latency per PRD-40 — small enough that the UI doesn't feel sluggish,
// large enough to avoid hammering the DB.
const cancelPollInterval = 5 * time.Second

// startCancelPoller runs a background goroutine for the lifetime of the run
// (or suite — works for both because Orchestrator.cancels is keyed by the
// run id regardless of origin). When the DB flag flips true — meaning a
// cancel request arrived at any API replica — it invokes the context cancel
// which propagates through the existing lifecycle teardown path.
//
// The poller exits as soon as ctx is done, so the normal completion path
// cleans it up without any extra bookkeeping.
func (o *Orchestrator) startCancelPoller(ctx context.Context, runID string, cancel context.CancelFunc) {
	go func() {
		ticker := time.NewTicker(cancelPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				requested, err := o.repo.IsCancelRequested(ctx, runID)
				if err != nil {
					// Transient DB error; keep polling. If Postgres is
					// genuinely down, other parts of the orchestrator will
					// fail harder and the context will get cancelled anyway.
					continue
				}
				if requested {
					log.Printf("[%s] cancel_requested=true in DB, cancelling goroutine", runID[:8])
					cancel()
					return
				}
			}
		}
	}()
}
