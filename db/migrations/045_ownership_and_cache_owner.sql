-- PRD-68 P4: per-object ownership + model-cache job ownership.
-- Additive + idempotent.
--
--   created_by: Cognito `sub` of the principal that submitted the run /
--     suite. NULL on historical rows and on runs created while
--     AUTH_DISABLED (the synthetic admin has no stable sub). Cancel/delete
--     enforce owner-or-admin; reads stay open to every role (PRD-48).
--   model_cache.owner_pod: the API pod watching the cache Job (mirrors
--     benchmark_runs.owner_pod from PRD-40) so a pod restart mid-cache no
--     longer strands the row in 'caching' forever — the recovery loop on a
--     live sibling adopts or fails it.
--
-- The partial indexes keep the 30s orphan scans O(active) instead of
-- O(all rows with that status) as history grows (Perf L2 / Scal M4).
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction; migrate.sh
-- pipes each file to psql without one (same as 024).

ALTER TABLE benchmark_runs  ADD COLUMN IF NOT EXISTS created_by TEXT;
ALTER TABLE test_suite_runs ADD COLUMN IF NOT EXISTS created_by TEXT;
ALTER TABLE model_cache     ADD COLUMN IF NOT EXISTS owner_pod  TEXT;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_benchmark_runs_active_owner
    ON benchmark_runs (owner_pod) WHERE status IN ('pending', 'running');
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_test_suite_runs_active_owner
    ON test_suite_runs (owner_pod) WHERE status IN ('pending', 'running');
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_model_cache_active_owner
    ON model_cache (owner_pod) WHERE status = 'caching';
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_benchmark_runs_created_by
    ON benchmark_runs (created_by) WHERE created_by IS NOT NULL;
