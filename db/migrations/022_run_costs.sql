-- PRD-35: persist per-run and per-suite EC2 cost at completion. Cost is
-- computed once when the orchestrator marks a run completed/failed, using
-- the pricing row current at that moment. Freezing the value solves price
-- drift (historical runs keep their actual cost) and makes Dashboard
-- aggregates trivial SUMs rather than runtime joins against pricing.
--
-- total_cost_usd   = hourly_rate × (completed_at − started_at) / 3600
--                    — full EC2 node lifetime (image pull + load + bench + teardown)
-- loadgen_cost_usd = hourly_rate × metrics.total_duration_seconds / 3600
--                    — stored now, displayed in a future iteration
--
-- All columns nullable, no default. Historical rows stay NULL and are
-- excluded from aggregates via COALESCE(..., 0). That's accurate — we
-- don't have a reliable way to reprice old runs.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS total_cost_usd   NUMERIC,
    ADD COLUMN IF NOT EXISTS loadgen_cost_usd NUMERIC;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS total_cost_usd NUMERIC;
