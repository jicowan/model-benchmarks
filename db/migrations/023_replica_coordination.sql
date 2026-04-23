-- PRD-40: multi-replica coordination.
--
-- Three pieces of state used to live only in each API pod's memory:
--   1. Orchestrator.cancels map — cross-pod cancel was broken because the
--      load balancer might route POST /runs/{id}/cancel to the non-owning
--      pod, which had no cancel function for the run.
--   2. Startup orphan recovery — every pod marked every running row as
--      orphaned on startup, which wiped out sibling's in-flight runs
--      during rolling deploys.
--   3. Same shape for the seeder via InterruptActiveCatalogSeeds.
--
-- This migration moves coordination into Postgres:
--   - `owner_pod` records which API pod is orchestrating each run/suite/seed.
--   - `cancel_requested` is a flag the cancel handler sets; the owning pod
--     polls it every 5s and self-cancels when true.
--   - `api_pod_heartbeats` is an upserted-every-10s liveness table used by
--     ownership-aware orphan recovery: only take action on rows whose
--     owner_pod is no longer heartbeating.
--
-- All new columns nullable; historical rows stay NULL. Orphan recovery
-- excludes NULL owners (pre-migration) so the deploy doesn't wipe state.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS owner_pod        TEXT,
    ADD COLUMN IF NOT EXISTS cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS owner_pod        TEXT,
    ADD COLUMN IF NOT EXISTS cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE catalog_seed_status
    ADD COLUMN IF NOT EXISTS owner_pod TEXT;

CREATE TABLE IF NOT EXISTS api_pod_heartbeats (
    pod_name     TEXT PRIMARY KEY,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Orphan-recovery scans use the owner_pod column; index it on the two
-- tables where it matters for perf. The heartbeats table is small enough
-- that the primary key is sufficient.
CREATE INDEX IF NOT EXISTS idx_benchmark_runs_owner_pod
    ON benchmark_runs (owner_pod)
    WHERE owner_pod IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_test_suite_runs_owner_pod
    ON test_suite_runs (owner_pod)
    WHERE owner_pod IS NOT NULL;
