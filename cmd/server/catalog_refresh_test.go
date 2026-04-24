package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// refreshOnlyRepo wraps database.MockRepo so we can count RefreshCatalogRows
// calls and optionally inject an error on the first call. Only the methods
// StartCatalogRefreshLoop uses are overridden; everything else delegates to
// the embedded MockRepo.
type refreshOnlyRepo struct {
	*database.MockRepo
	calls    atomic.Int32
	failOnce atomic.Bool
}

func (r *refreshOnlyRepo) RefreshCatalogRows(_ context.Context) error {
	n := r.calls.Add(1)
	if n == 1 && r.failOnce.Load() {
		return errors.New("injected refresh failure")
	}
	return nil
}

func newRefreshOnlyRepo() *refreshOnlyRepo {
	return &refreshOnlyRepo{MockRepo: database.NewMockRepo()}
}

func TestStartCatalogRefreshLoop_InitialRefreshIsSynchronous(t *testing.T) {
	repo := newRefreshOnlyRepo()

	// Cancel the context immediately after the initial refresh so the
	// background ticker goroutine exits on the next select.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartCatalogRefreshLoop(ctx, repo)

	// The synchronous initial refresh must have completed by the time
	// StartCatalogRefreshLoop returns.
	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("initial refresh call count = %d, want 1", got)
	}
}

func TestStartCatalogRefreshLoop_SurvivesInitialFailure(t *testing.T) {
	repo := newRefreshOnlyRepo()
	repo.failOnce.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic, must not block — a startup DB outage is logged
	// and the loop still starts.
	StartCatalogRefreshLoop(ctx, repo)

	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("initial refresh call count = %d, want 1 (even after error)", got)
	}
}

func TestStartCatalogRefreshLoop_GoroutineExitsOnContextDone(t *testing.T) {
	repo := newRefreshOnlyRepo()

	ctx, cancel := context.WithCancel(context.Background())
	StartCatalogRefreshLoop(ctx, repo)

	// Cancel before the 5-minute tick can fire. Give the goroutine a
	// moment to notice. We're asserting that no panics escape and the
	// call count never increments past the synchronous initial refresh.
	cancel()
	time.Sleep(50 * time.Millisecond)

	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("refresh call count after cancel = %d, want 1", got)
	}
}

// TestCatalogRefreshInterval pins the constant. Changing the cadence
// is a product decision — the test forces a conscious update rather
// than a silent drift.
func TestCatalogRefreshInterval(t *testing.T) {
	if catalogRefreshInterval != 5*time.Minute {
		t.Errorf("catalogRefreshInterval = %v, want 5m", catalogRefreshInterval)
	}
}
