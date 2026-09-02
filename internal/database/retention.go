package database

import (
	"context"
	"fmt"
)

// PRD-68 P6: opt-in retention.
//
// benchmark_runs, benchmark_metrics, benchmark_metrics_by_shard, oom_events,
// test_suite_runs and scenario_results grow without bound. Operators who
// want a ceiling set RUN_RETENTION_DAYS; the API then deletes TERMINAL runs
// (and suites) whose completed_at is older than that, in bounded batches so
// a long-neglected table doesn't produce one giant transaction.
//
// Off by default (0). Deleting history is the operator's call, not ours.

// PurgeTerminalRunsOlderThan deletes up to batch completed/failed
// benchmark_runs (with their metrics, shard rows and OOM events) and
// test_suite_runs (with their scenario results) whose completed_at is
// older than days. Returns the number of runs + suites removed.
func (r *Repository) PurgeTerminalRunsOlderThan(ctx context.Context, days, batch int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	if batch <= 0 {
		batch = 500
	}
	interval := fmt.Sprintf("%d days", days)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// benchmark_metrics has no ON DELETE CASCADE (001_initial); shards and
	// oom_events do. Select the batch once into a temp table so every
	// statement deletes exactly the same set.
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE purge_runs ON COMMIT DROP AS
		SELECT id FROM benchmark_runs
		 WHERE status IN ('completed','failed')
		   AND completed_at < now() - $1::interval
		 ORDER BY completed_at
		 LIMIT $2`, interval, batch); err != nil {
		return 0, fmt.Errorf("select purge batch: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM benchmark_metrics WHERE run_id IN (SELECT id FROM purge_runs)`); err != nil {
		return 0, fmt.Errorf("purge metrics: %w", err)
	}
	runTag, err := tx.Exec(ctx, `DELETE FROM benchmark_runs WHERE id IN (SELECT id FROM purge_runs)`)
	if err != nil {
		return 0, fmt.Errorf("purge runs: %w", err)
	}
	suiteTag, err := tx.Exec(ctx, `
		DELETE FROM test_suite_runs
		 WHERE id IN (
		   SELECT id FROM test_suite_runs
		    WHERE status IN ('completed','failed')
		      AND completed_at < now() - $1::interval
		    ORDER BY completed_at
		    LIMIT $2)`, interval, batch)
	if err != nil {
		return 0, fmt.Errorf("purge suites: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return runTag.RowsAffected() + suiteTag.RowsAffected(), nil
}
