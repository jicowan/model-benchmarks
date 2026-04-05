-- Add scenario_id column to benchmark_runs
-- References a predefined scenario from internal/scenario/builtin.go

ALTER TABLE benchmark_runs ADD COLUMN IF NOT EXISTS scenario_id VARCHAR(50);

COMMENT ON COLUMN benchmark_runs.scenario_id IS 'Scenario identifier (chatbot, batch, stress, production, long-context)';

-- Index for filtering by scenario
CREATE INDEX IF NOT EXISTS idx_benchmark_runs_scenario ON benchmark_runs(scenario_id);
