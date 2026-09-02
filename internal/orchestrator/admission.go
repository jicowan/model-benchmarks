package orchestrator

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// PRD-68 P3: admission control, shared cancel/fencing poller, drain.
//
// Before this file every CreateRun spawned an unbounded Execute goroutine
// (a 20×10 catalog seed ⇒ 200 concurrent Karpenter node requests), each run
// polled the DB for cancel on its own 5s ticker, a stale owner could
// overwrite a terminal status written by recovery, and a rolling deploy
// killed every in-flight run on the pod. The pieces here fix each of those:
//
//   - slots:    a per-pod semaphore sized MAX_CONCURRENT_RUNS. Execute /
//               ExecuteSuite take a slot AFTER registering for cancel and
//               BEFORE flipping status to running, so queued runs stay
//               "pending", are cancellable, and never touch the cluster.
//   - pollLoop: ONE goroutine per pod reads the coordination columns for
//               every locally-registered run in a single query every 5s and
//               cancels any that (a) had cancel requested, (b) were deleted,
//               (c) were claimed by another pod (fenced), or (d) were already
//               moved to a terminal status by recovery.
//   - Drain:    on SIGTERM the server stops accepting HTTP, then waits here
//               for active orchestrations to finish (bounded by the pod's
//               termination grace) while the heartbeat keeps running so a
//               sibling does not treat the runs as orphans mid-drain.

const (
	defaultMaxConcurrentRuns = 4
	pollInterval             = 5 * time.Second
)

// ErrDraining is returned by Execute when the pod is shutting down and no
// longer admits new work.
var ErrDraining = errors.New("api pod is draining; run not started")

// admission is the mutable coordination state hung off Orchestrator.
type admission struct {
	slots    chan struct{}
	active   sync.WaitGroup
	draining atomic.Bool

	// gauges / counters for /metrics
	activeRuns    atomic.Int64
	queuedRuns    atomic.Int64
	recovered     atomic.Int64 // orphan runs marked failed/completed by recovery
	fenced        atomic.Int64 // runs cancelled because ownership moved
	pollErrors    atomic.Int64
	admittedTotal atomic.Int64
	rejectedDrain atomic.Int64
}

func newAdmission(maxConcurrent int) *admission {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentRuns
	}
	return &admission{slots: make(chan struct{}, maxConcurrent)}
}

// maxConcurrentRunsFromEnv reads MAX_CONCURRENT_RUNS (default 4).
func maxConcurrentRunsFromEnv() int {
	if v := os.Getenv("MAX_CONCURRENT_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		log.Printf("warning: MAX_CONCURRENT_RUNS=%q invalid; using %d", v, defaultMaxConcurrentRuns)
	}
	return defaultMaxConcurrentRuns
}

// SetMaxConcurrentRuns resizes the admission semaphore. Intended for tests
// and startup wiring; not safe to call while runs are in flight.
func (o *Orchestrator) SetMaxConcurrentRuns(n int) {
	o.adm = newAdmission(n)
}

// Stats is a snapshot of the orchestrator gauges for /metrics.
type Stats struct {
	ActiveRuns, QueuedRuns, MaxConcurrent   int64
	Recovered, Fenced, PollErrors, Admitted int64
	RejectedDraining                        int64
	Draining                                bool
}

// Stats returns the current gauge values.
func (o *Orchestrator) Stats() Stats {
	a := o.adm
	return Stats{
		ActiveRuns:       a.activeRuns.Load(),
		QueuedRuns:       a.queuedRuns.Load(),
		MaxConcurrent:    int64(cap(a.slots)),
		Recovered:        a.recovered.Load(),
		Fenced:           a.fenced.Load(),
		PollErrors:       a.pollErrors.Load(),
		Admitted:         a.admittedTotal.Load(),
		RejectedDraining: a.rejectedDrain.Load(),
		Draining:         a.draining.Load(),
	}
}

// Draining reports whether Drain has begun.
func (o *Orchestrator) Draining() bool { return o.adm.draining.Load() }

// beginRun registers a run's cancel func and marks it active for Drain.
// Returns false (and does nothing) when the pod is draining.
func (o *Orchestrator) beginRun(runID string, cancel context.CancelFunc) bool {
	if o.adm.draining.Load() {
		o.adm.rejectedDrain.Add(1)
		return false
	}
	o.adm.active.Add(1)
	o.mu.Lock()
	o.cancels[runID] = cancel
	o.mu.Unlock()
	return true
}

// endRun is beginRun's deferred counterpart.
func (o *Orchestrator) endRun(runID string) {
	o.mu.Lock()
	delete(o.cancels, runID)
	o.mu.Unlock()
	o.adm.active.Done()
}

// acquireSlot blocks until a concurrency slot is free or ctx is done. The
// returned release func must be called exactly once. While waiting the run
// counts as queued (visible on /metrics); once admitted, as active.
func (o *Orchestrator) acquireSlot(ctx context.Context, runID string) (release func(), err error) {
	a := o.adm
	a.queuedRuns.Add(1)
	defer a.queuedRuns.Add(-1)

	select {
	case a.slots <- struct{}{}:
	default:
		log.Printf("[%s] waiting for a run slot (%d/%d in use)", shortID(runID), len(a.slots), cap(a.slots))
		select {
		case a.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	a.activeRuns.Add(1)
	a.admittedTotal.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			<-a.slots
			a.activeRuns.Add(-1)
		})
	}, nil
}

// StartCancelPollLoop starts the single per-pod coordination poller. It
// replaces the previous per-run cancel pollers. Runs until ctx is done.
func (o *Orchestrator) StartCancelPollLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.pollOnce(ctx)
			}
		}
	}()
}

// pollOnce is one pass of the shared poller (exported for tests via
// PollCoordinationOnce).
func (o *Orchestrator) pollOnce(ctx context.Context) {
	o.mu.Lock()
	ids := make([]string, 0, len(o.cancels))
	for id := range o.cancels {
		ids = append(ids, id)
	}
	o.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	qctx, cancel := context.WithTimeout(ctx, pollInterval)
	defer cancel()
	own, err := o.repo.GetRunOwnership(qctx, ids)
	if err != nil {
		// Transient DB error: keep the runs going. If Postgres is really
		// down the orchestrator's own writes will fail and abort the run.
		o.adm.pollErrors.Add(1)
		return
	}

	for _, id := range ids {
		ro, found := own[id]
		var reason string
		switch {
		case !found:
			reason = "row deleted"
		case ro.CancelRequested:
			reason = "cancel_requested=true"
		case ro.OwnerPod != nil && *ro.OwnerPod != o.hostname:
			reason = "ownership moved to pod " + *ro.OwnerPod + " (fenced)"
			o.adm.fenced.Add(1)
		case ro.Status != "pending" && ro.Status != "running":
			reason = "status already terminal (" + ro.Status + ")"
		default:
			continue
		}
		log.Printf("[%s] %s in DB, cancelling goroutine", shortID(id), reason)
		o.CancelRun(id)
	}
}

// PollCoordinationOnce runs one poller pass synchronously (tests).
func (o *Orchestrator) PollCoordinationOnce(ctx context.Context) { o.pollOnce(ctx) }

// Drain stops admitting new runs and waits for in-flight orchestrations to
// finish, or for ctx to expire. Returns the number of runs still active when
// it gave up (0 on a clean drain). Runs still active after Drain returns
// will be recovered by a sibling pod once this pod's heartbeat goes stale.
func (o *Orchestrator) Drain(ctx context.Context) int64 {
	o.adm.draining.Store(true)
	done := make(chan struct{})
	go func() {
		o.adm.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return 0
	case <-ctx.Done():
		return o.adm.activeRuns.Load() + o.adm.queuedRuns.Load()
	}
}

// shortID is the 8-char log prefix used throughout the orchestrator.
// Tolerates short ids (tests use "run-1").
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
