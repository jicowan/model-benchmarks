-- PRD-37: DB performance — missing indexes.
--
-- The Runs list page and Dashboard sort `benchmark_runs` and
-- `test_suite_runs` by `created_at DESC`. Without an index Postgres
-- seq-scans the whole table and sorts before applying LIMIT 25.
-- Two `(created_at DESC)` indexes fix that.
--
-- The two status indexes below were added in earlier migrations
-- (001 and 009). They are kept here as idempotent no-ops so this
-- file documents the full index surface PRD-37 depends on.
--
-- CREATE INDEX CONCURRENTLY must run outside a transaction. The
-- migration runner (docker/migrate.sh) pipes files to psql without
-- wrapping them in BEGIN/COMMIT, so this is safe as-is.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_benchmark_runs_created_at
    ON benchmark_runs (created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_test_suite_runs_created_at
    ON test_suite_runs (created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_benchmark_runs_status
    ON benchmark_runs (status);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_test_suite_runs_status
    ON test_suite_runs (status);
