-- Test suite runs (parent table)
CREATE TABLE IF NOT EXISTS test_suite_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id UUID NOT NULL REFERENCES models(id),
    instance_type_id UUID NOT NULL REFERENCES instance_types(id),
    suite_id VARCHAR(50) NOT NULL,  -- 'quick', 'standard', 'comprehensive', etc.

    -- Config from recommendation engine
    tensor_parallel_degree INTEGER NOT NULL,
    quantization VARCHAR(20),
    max_model_len INTEGER,

    -- Status tracking
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, running, completed, failed
    current_scenario VARCHAR(50),  -- which scenario is currently running

    -- Timestamps
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Scenario results (child table - one per scenario in the suite)
CREATE TABLE IF NOT EXISTS scenario_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    suite_run_id UUID NOT NULL REFERENCES test_suite_runs(id) ON DELETE CASCADE,
    scenario_id VARCHAR(50) NOT NULL,  -- 'chatbot', 'batch', etc.

    -- Execution info
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, running, completed, failed, skipped
    error_message TEXT,  -- error details if failed
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Metrics (denormalized from benchmark_metrics for convenience)
    ttft_p50_ms DOUBLE PRECISION,
    ttft_p90_ms DOUBLE PRECISION,
    ttft_p99_ms DOUBLE PRECISION,
    e2e_latency_p50_ms DOUBLE PRECISION,
    e2e_latency_p90_ms DOUBLE PRECISION,
    e2e_latency_p99_ms DOUBLE PRECISION,
    itl_p50_ms DOUBLE PRECISION,
    itl_p90_ms DOUBLE PRECISION,
    itl_p99_ms DOUBLE PRECISION,
    throughput_tps DOUBLE PRECISION,
    requests_per_second DOUBLE PRECISION,
    successful_requests INTEGER,
    failed_requests INTEGER,

    -- Loadgen config used for this scenario
    loadgen_config TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_test_suite_runs_status ON test_suite_runs(status);
CREATE INDEX IF NOT EXISTS idx_test_suite_runs_model ON test_suite_runs(model_id);
CREATE INDEX IF NOT EXISTS idx_scenario_results_suite ON scenario_results(suite_run_id);

COMMENT ON TABLE test_suite_runs IS 'Parent table for test suite executions';
COMMENT ON TABLE scenario_results IS 'Results for each scenario within a test suite run';
