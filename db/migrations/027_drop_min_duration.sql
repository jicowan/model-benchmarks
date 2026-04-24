-- PRD-42: drop min_duration_seconds.
--
-- Holdover from PRD-05 (custom loadgen era). PRD-11 replaced the custom
-- loadgen with inference-perf, which drives duration entirely from the
-- scenario's load stages. Nothing on the live code path reads this
-- column anymore:
--   - UI always sends scenario_id
--   - Catalog seeder always sends ScenarioID from matrix defaults
--   - Suite runs drive duration from scenarios
--   - The orchestrator fallback branch that consumed this field is
--     being deleted in the same PR
--
-- Actual run duration is captured in benchmark_metrics.total_duration_seconds,
-- so no user-visible historical data is lost.

ALTER TABLE benchmark_runs DROP COLUMN IF EXISTS min_duration_seconds;
ALTER TABLE catalog_seed_defaults DROP COLUMN IF EXISTS min_duration_seconds;
