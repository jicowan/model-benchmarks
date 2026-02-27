-- AccelBench initial schema

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Models table
CREATE TABLE models (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hf_id TEXT NOT NULL,
    hf_revision TEXT NOT NULL,
    model_family TEXT,
    parameter_count BIGINT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE UNIQUE INDEX idx_models_hf_id_revision ON models (hf_id, hf_revision);

-- Instance types table
CREATE TABLE instance_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    family TEXT NOT NULL,
    accelerator_type TEXT NOT NULL CHECK (accelerator_type IN ('gpu', 'neuron')),
    accelerator_name TEXT NOT NULL,
    accelerator_count INT NOT NULL,
    accelerator_memory_gib INT NOT NULL,
    vcpus INT NOT NULL,
    memory_gib INT NOT NULL
);

-- Benchmark runs table
CREATE TABLE benchmark_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id UUID REFERENCES models(id) NOT NULL,
    instance_type_id UUID REFERENCES instance_types(id) NOT NULL,
    framework TEXT NOT NULL CHECK (framework IN ('vllm', 'vllm-neuron')),
    framework_version TEXT NOT NULL,
    tensor_parallel_degree INT NOT NULL DEFAULT 1,
    quantization TEXT,
    concurrency INT NOT NULL,
    input_sequence_length INT NOT NULL,
    output_sequence_length INT NOT NULL,
    dataset_name TEXT NOT NULL,
    run_type TEXT NOT NULL CHECK (run_type IN ('catalog', 'on_demand')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    superseded BOOLEAN DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_benchmark_runs_model ON benchmark_runs (model_id);
CREATE INDEX idx_benchmark_runs_instance ON benchmark_runs (instance_type_id);
CREATE INDEX idx_benchmark_runs_status ON benchmark_runs (status);

-- Benchmark metrics table (immutable, one row per run)
CREATE TABLE benchmark_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID REFERENCES benchmark_runs(id) UNIQUE NOT NULL,
    ttft_p50_ms NUMERIC,
    ttft_p90_ms NUMERIC,
    ttft_p95_ms NUMERIC,
    ttft_p99_ms NUMERIC,
    e2e_latency_p50_ms NUMERIC,
    e2e_latency_p90_ms NUMERIC,
    e2e_latency_p95_ms NUMERIC,
    e2e_latency_p99_ms NUMERIC,
    itl_p50_ms NUMERIC,
    itl_p90_ms NUMERIC,
    itl_p95_ms NUMERIC,
    itl_p99_ms NUMERIC,
    throughput_per_request_tps NUMERIC,
    throughput_aggregate_tps NUMERIC,
    requests_per_second NUMERIC,
    accelerator_utilization_pct NUMERIC,
    accelerator_memory_peak_gib NUMERIC,
    successful_requests INT,
    failed_requests INT,
    total_duration_seconds NUMERIC,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Pricing table (refreshed daily)
CREATE TABLE pricing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_type_id UUID REFERENCES instance_types(id) NOT NULL,
    region TEXT NOT NULL,
    on_demand_hourly_usd NUMERIC NOT NULL,
    reserved_1yr_hourly_usd NUMERIC,
    reserved_3yr_hourly_usd NUMERIC,
    effective_date DATE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_pricing_instance_region ON pricing (instance_type_id, region);
CREATE INDEX idx_pricing_effective_date ON pricing (effective_date DESC);
