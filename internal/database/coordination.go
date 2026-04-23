package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PRD-40: multi-replica coordination. Three primitives:
//   * Ownership claim (ClaimRun / ClaimSuiteRun / ClaimSeed) — the pod
//     running Execute writes its hostname to owner_pod so orphan recovery
//     on sibling pods can tell "mine" from "someone else's".
//   * Cross-pod cancel (RequestCancel / IsCancelRequested) — the cancel
//     handler sets a DB flag; the owning pod's goroutine polls it.
//   * Heartbeats (Heartbeat / LiveAPIPods) — a 10s upsert driven loop that
//     orphan recovery reads to distinguish "owner alive" from "owner dead".

// ClaimRun stamps owner_pod for a single benchmark_runs row. Idempotent:
// calling again with the same (runID, pod) is a no-op; with a different
// pod it overwrites (orphan-recovery takeover).
func (r *Repository) ClaimRun(ctx context.Context, runID, pod string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE benchmark_runs SET owner_pod = $1 WHERE id = $2`, pod, runID)
	if err != nil {
		return fmt.Errorf("claim run: %w", err)
	}
	return nil
}

// ClaimSuiteRun is the test_suite_runs counterpart.
func (r *Repository) ClaimSuiteRun(ctx context.Context, suiteRunID, pod string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE test_suite_runs SET owner_pod = $1 WHERE id = $2`, pod, suiteRunID)
	if err != nil {
		return fmt.Errorf("claim suite run: %w", err)
	}
	return nil
}

// ClaimSeed is the catalog_seed_status counterpart.
func (r *Repository) ClaimSeed(ctx context.Context, seedID, pod string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE catalog_seed_status SET owner_pod = $1 WHERE id = $2`, pod, seedID)
	if err != nil {
		return fmt.Errorf("claim seed: %w", err)
	}
	return nil
}

// RequestCancel sets cancel_requested=TRUE on whichever table holds the id.
// Tries benchmark_runs first, then test_suite_runs. Returns an error only
// when the DB fails — it's NOT an error for the id to match neither (the
// handler already verified the run exists and is cancelable before calling).
func (r *Repository) RequestCancel(ctx context.Context, runID string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE benchmark_runs SET cancel_requested = TRUE WHERE id = $1`, runID)
	if err != nil {
		return fmt.Errorf("request cancel (benchmark_runs): %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE test_suite_runs SET cancel_requested = TRUE WHERE id = $1`, runID)
	if err != nil {
		return fmt.Errorf("request cancel (test_suite_runs): %w", err)
	}
	return nil
}

// IsCancelRequested returns true when the cancel flag has been set on either
// the benchmark_runs or test_suite_runs row matching runID. The orchestrator
// goroutine polls this every 5s.
func (r *Repository) IsCancelRequested(ctx context.Context, runID string) (bool, error) {
	var flag bool
	err := r.pool.QueryRow(ctx,
		`SELECT cancel_requested FROM benchmark_runs WHERE id = $1`, runID).Scan(&flag)
	if err == nil {
		return flag, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("is cancel requested (benchmark_runs): %w", err)
	}
	err = r.pool.QueryRow(ctx,
		`SELECT cancel_requested FROM test_suite_runs WHERE id = $1`, runID).Scan(&flag)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is cancel requested (test_suite_runs): %w", err)
	}
	return flag, nil
}

// Heartbeat upserts this pod's last-seen timestamp. Called every 10s.
func (r *Repository) Heartbeat(ctx context.Context, pod string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO api_pod_heartbeats (pod_name, last_seen_at)
		VALUES ($1, now())
		ON CONFLICT (pod_name) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at`,
		pod)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}

// LiveAPIPods returns the hostnames of every pod whose heartbeat is within
// the given TTL. A pod whose row is older than TTL is considered dead and
// its in-flight runs become orphan-recoverable.
func (r *Repository) LiveAPIPods(ctx context.Context, ttl time.Duration) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT pod_name FROM api_pod_heartbeats
		  WHERE last_seen_at > now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("live api pods: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteStaleHeartbeats is belt-and-braces cleanup: rows older than 2×TTL
// are irreversibly dead. Called periodically from the recovery loop.
func (r *Repository) DeleteStaleHeartbeats(ctx context.Context, olderThan time.Duration) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM api_pod_heartbeats WHERE last_seen_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return fmt.Errorf("delete stale heartbeats: %w", err)
	}
	return nil
}

// GetOrphanedRuns returns benchmark_runs rows whose:
//   - status is not yet terminal (pending | running)
//   - owner_pod is NOT NULL (pre-migration rows are skipped — we can't
//     attribute ownership, so we don't touch them)
//   - owner_pod is NOT in livePods (i.e. the owning pod stopped heartbeating)
//
// Returned rows are candidates for the markFailed + cleanup path.
func (r *Repository) GetOrphanedRuns(ctx context.Context, livePods []string) ([]BenchmarkRun, error) {
	// pgx handles []string → text[] via $1 = ANY($2) pattern. When livePods
	// is empty (unlikely but possible at cluster bootstrap), every non-NULL
	// owner counts as dead.
	rows, err := r.pool.Query(ctx, `
		SELECT id, status, owner_pod
		  FROM benchmark_runs
		 WHERE status IN ('pending','running')
		   AND owner_pod IS NOT NULL
		   AND NOT (owner_pod = ANY($1))`,
		livePods)
	if err != nil {
		return nil, fmt.Errorf("get orphaned runs: %w", err)
	}
	defer rows.Close()
	var out []BenchmarkRun
	for rows.Next() {
		var br BenchmarkRun
		if err := rows.Scan(&br.ID, &br.Status, &br.OwnerPod); err != nil {
			return nil, err
		}
		out = append(out, br)
	}
	return out, rows.Err()
}

// GetOrphanedSuiteRuns is the test_suite_runs counterpart.
func (r *Repository) GetOrphanedSuiteRuns(ctx context.Context, livePods []string) ([]TestSuiteRun, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, status, owner_pod
		  FROM test_suite_runs
		 WHERE status IN ('pending','running')
		   AND owner_pod IS NOT NULL
		   AND NOT (owner_pod = ANY($1))`,
		livePods)
	if err != nil {
		return nil, fmt.Errorf("get orphaned suite runs: %w", err)
	}
	defer rows.Close()
	var out []TestSuiteRun
	for rows.Next() {
		var s TestSuiteRun
		if err := rows.Scan(&s.ID, &s.Status, &s.OwnerPod); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetOrphanedSeeds is the catalog_seed_status counterpart. Seeds that are
// active with a dead owner get marked 'interrupted' by the recovery loop.
func (r *Repository) GetOrphanedSeeds(ctx context.Context, livePods []string) ([]CatalogSeedStatus, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, status, owner_pod
		  FROM catalog_seed_status
		 WHERE status = 'active'
		   AND owner_pod IS NOT NULL
		   AND NOT (owner_pod = ANY($1))`,
		livePods)
	if err != nil {
		return nil, fmt.Errorf("get orphaned seeds: %w", err)
	}
	defer rows.Close()
	var out []CatalogSeedStatus
	for rows.Next() {
		var s CatalogSeedStatus
		if err := rows.Scan(&s.ID, &s.Status, &s.OwnerPod); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
