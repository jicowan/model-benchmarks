package database

import (
	"context"
	"testing"
	"time"
)

// TestRequestCancel_FindsTableAutomatically ensures RequestCancel on the mock
// sets cancel_requested on whichever table holds the id.
func TestRequestCancel_FindsTableAutomatically(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepo()

	// Seed a single-run and a suite-run with the same-shaped ids.
	repo.runs["r1"] = &BenchmarkRun{ID: "r1", Status: "running"}
	repo.suiteRuns["s1"] = &TestSuiteRun{ID: "s1", Status: "running"}

	if err := repo.RequestCancel(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if !repo.runs["r1"].CancelRequested {
		t.Errorf("run r1 cancel_requested not set")
	}
	if repo.suiteRuns["s1"].CancelRequested {
		t.Errorf("suite s1 cancel_requested unexpectedly set")
	}

	if err := repo.RequestCancel(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if !repo.suiteRuns["s1"].CancelRequested {
		t.Errorf("suite s1 cancel_requested not set after second call")
	}
}

// TestIsCancelRequested_BothTables ensures lookups return the flag from
// whichever table contains the id.
func TestIsCancelRequested_BothTables(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepo()
	repo.runs["r1"] = &BenchmarkRun{ID: "r1", CancelRequested: true}
	repo.suiteRuns["s1"] = &TestSuiteRun{ID: "s1", CancelRequested: false}

	if v, _ := repo.IsCancelRequested(ctx, "r1"); !v {
		t.Errorf("run r1: expected true")
	}
	if v, _ := repo.IsCancelRequested(ctx, "s1"); v {
		t.Errorf("suite s1: expected false")
	}
	if v, _ := repo.IsCancelRequested(ctx, "unknown"); v {
		t.Errorf("unknown id: expected false")
	}
}

// TestHeartbeat_LiveAPIPods_TTL verifies TTL filtering on the mock.
func TestHeartbeat_LiveAPIPods_TTL(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepo()

	_ = repo.Heartbeat(ctx, "pod-a")
	_ = repo.Heartbeat(ctx, "pod-b")

	// Sleep just enough to exceed a 10ms TTL window.
	time.Sleep(20 * time.Millisecond)
	// Within a 1s TTL, both should still be live.
	live, _ := repo.LiveAPIPods(ctx, 1*time.Second)
	if len(live) != 2 {
		t.Errorf("with 1s TTL got %d live pods, want 2", len(live))
	}
	// With a 5ms TTL (shorter than our sleep), both should be dead.
	live, _ = repo.LiveAPIPods(ctx, 5*time.Millisecond)
	if len(live) != 0 {
		t.Errorf("with 5ms TTL got %d live pods, want 0", len(live))
	}
}

// TestGetOrphanedRuns_OwnershipRules — the core of PRD-40. Rows are
// "orphaned" only when:
//   - status is non-terminal
//   - owner_pod is not NULL (pre-migration rows are skipped)
//   - owner_pod is not in livePods (dead owner)
func TestGetOrphanedRuns_OwnershipRules(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepo()

	deadOwner := "pod-dead"
	liveOwner := "pod-live"
	// Pre-migration run: no owner. Must NOT be recovered.
	repo.runs["pre"] = &BenchmarkRun{ID: "pre", Status: "running", OwnerPod: nil}
	// Live-owner run: owner appears in livePods. Must NOT be recovered.
	repo.runs["live"] = &BenchmarkRun{ID: "live", Status: "running", OwnerPod: &liveOwner}
	// Dead-owner run: owner NOT in livePods. MUST be recovered.
	repo.runs["dead"] = &BenchmarkRun{ID: "dead", Status: "running", OwnerPod: &deadOwner}
	// Completed with dead owner: terminal state, must not be recovered.
	repo.runs["done"] = &BenchmarkRun{ID: "done", Status: "completed", OwnerPod: &deadOwner}

	orphans, err := repo.GetOrphanedRuns(ctx, []string{liveOwner})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("got %d orphans, want 1", len(orphans))
	}
	if orphans[0].ID != "dead" {
		t.Errorf("expected orphan 'dead', got %q", orphans[0].ID)
	}
}

// TestClaim_Idempotent ensures the same pod re-claiming a run is a no-op,
// and a different pod claiming it overwrites (takeover semantics).
func TestClaim_Idempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepo()
	repo.runs["r1"] = &BenchmarkRun{ID: "r1"}

	_ = repo.ClaimRun(ctx, "r1", "pod-a")
	if *repo.runs["r1"].OwnerPod != "pod-a" {
		t.Errorf("after claim by pod-a: owner = %v", repo.runs["r1"].OwnerPod)
	}
	_ = repo.ClaimRun(ctx, "r1", "pod-a")
	if *repo.runs["r1"].OwnerPod != "pod-a" {
		t.Errorf("re-claim by pod-a changed owner: %v", repo.runs["r1"].OwnerPod)
	}
	_ = repo.ClaimRun(ctx, "r1", "pod-b")
	if *repo.runs["r1"].OwnerPod != "pod-b" {
		t.Errorf("takeover by pod-b did not update owner: %v", repo.runs["r1"].OwnerPod)
	}
}
