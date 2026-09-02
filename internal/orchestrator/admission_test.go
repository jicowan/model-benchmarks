package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

// PRD-68 P3 tests: admission semaphore, shared cancel/fencing poller, drain.

func newTestOrch(t *testing.T, maxConcurrent int) (*Orchestrator, *database.MockRepo) {
	t.Helper()
	repo := database.NewMockRepo()
	o := New(fake.NewSimpleClientset(), repo, "pod-a")
	o.SetMaxConcurrentRuns(maxConcurrent)
	return o, repo
}

func seedActiveRun(t *testing.T, repo *database.MockRepo, owner string) string {
	t.Helper()
	p := owner
	id, err := repo.CreateBenchmarkRun(context.Background(), &database.BenchmarkRun{
		ModelID: "m", InstanceTypeID: "i", Status: "running", OwnerPod: &p,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAcquireSlot_QueuesBeyondCap(t *testing.T) {
	o, _ := newTestOrch(t, 1)
	ctx := context.Background()

	rel1, err := o.acquireSlot(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if st := o.Stats(); st.ActiveRuns != 1 || st.QueuedRuns != 0 {
		t.Fatalf("after first acquire: %+v", st)
	}

	// Second acquire must block until the first releases.
	got := make(chan struct{})
	go func() {
		rel2, err := o.acquireSlot(ctx, "run-2")
		if err != nil {
			t.Error(err)
			return
		}
		rel2()
		close(got)
	}()
	time.Sleep(50 * time.Millisecond)
	if st := o.Stats(); st.QueuedRuns != 1 {
		t.Fatalf("expected 1 queued, got %+v", st)
	}
	select {
	case <-got:
		t.Fatal("second acquire should still be blocked")
	default:
	}

	rel1()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire never proceeded after release")
	}
	if st := o.Stats(); st.ActiveRuns != 0 || st.QueuedRuns != 0 || st.Admitted != 2 {
		t.Fatalf("final stats: %+v", st)
	}
}

func TestAcquireSlot_CancelWhileQueued(t *testing.T) {
	o, _ := newTestOrch(t, 1)
	rel, _ := o.acquireSlot(context.Background(), "run-1")
	defer rel()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := o.acquireSlot(ctx, "run-2")
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected ctx error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued acquire did not observe cancel")
	}
	if st := o.Stats(); st.QueuedRuns != 0 {
		t.Fatalf("queued gauge not decremented: %+v", st)
	}
}

// pollCase registers a run's cancel func, mutates the DB row, runs one poll
// pass, and reports whether the cancel fired.
func pollCase(t *testing.T, mutate func(repo *database.MockRepo, id string)) bool {
	t.Helper()
	o, repo := newTestOrch(t, 4)
	id := seedActiveRun(t, repo, "pod-a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !o.beginRun(id, cancel) {
		t.Fatal("beginRun refused")
	}
	defer o.endRun(id)

	mutate(repo, id)
	o.PollCoordinationOnce(context.Background())
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func TestPoll_NoChangeKeepsRunning(t *testing.T) {
	if pollCase(t, func(*database.MockRepo, string) {}) {
		t.Fatal("healthy owned run was cancelled")
	}
}

func TestPoll_CancelRequested(t *testing.T) {
	if !pollCase(t, func(r *database.MockRepo, id string) { _ = r.RequestCancel(context.Background(), id) }) {
		t.Fatal("cancel_requested did not cancel the run")
	}
}

func TestPoll_DeletedRowCancels(t *testing.T) {
	// Scal H4: a cross-pod DELETE removes the row; the owner must stop.
	if !pollCase(t, func(r *database.MockRepo, id string) { _ = r.DeleteRun(context.Background(), id) }) {
		t.Fatal("deleted row did not cancel the run")
	}
}

func TestPoll_ForeignOwnerFences(t *testing.T) {
	// Scal M1: recovery on another pod re-claimed the run; we must yield.
	if !pollCase(t, func(r *database.MockRepo, id string) { _ = r.ClaimRun(context.Background(), id, "pod-b") }) {
		t.Fatal("foreign owner did not fence the run")
	}
}

func TestPoll_TerminalStatusCancels(t *testing.T) {
	if !pollCase(t, func(r *database.MockRepo, id string) { _ = r.UpdateRunFailed(context.Background(), id, "recovered elsewhere") }) {
		t.Fatal("terminal status did not cancel the run")
	}
}

func TestDrain_RefusesNewAndWaitsForActive(t *testing.T) {
	o, _ := newTestOrch(t, 4)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !o.beginRun("run-1", cancel) {
		t.Fatal("beginRun refused before drain")
	}

	drained := make(chan int64, 1)
	go func() {
		ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		drained <- o.Drain(ctx)
	}()
	time.Sleep(30 * time.Millisecond)
	if !o.Draining() {
		t.Fatal("Draining() false after Drain started")
	}
	if o.beginRun("run-2", cancel) {
		t.Fatal("beginRun admitted work while draining")
	}
	o.endRun("run-1")
	select {
	case left := <-drained:
		if left != 0 {
			t.Fatalf("expected clean drain, %d left", left)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Drain did not return after last run ended")
	}
}

func TestMockFencing_TerminalWriteIsConditional(t *testing.T) {
	repo := database.NewMockRepo()
	id := seedActiveRun(t, repo, "pod-a")
	ctx := context.Background()
	if err := repo.UpdateRunFailed(ctx, id, "recovery"); err != nil {
		t.Fatal(err)
	}
	// A stale owner's completion must NOT overwrite the recovery outcome.
	if err := repo.UpdateRunStatus(ctx, id, "completed"); err != database.ErrRunNotActive {
		t.Fatalf("expected ErrRunNotActive, got %v", err)
	}
	run, _ := repo.GetBenchmarkRun(ctx, id)
	if run.Status != "failed" {
		t.Fatalf("status overwritten: %s", run.Status)
	}
}
