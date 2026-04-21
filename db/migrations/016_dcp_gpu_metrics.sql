-- PRD-22: DCP GPU metrics + scenario_results avg/percentile parity
-- + better memory readings.
--
-- Adds:
--   * DCP metrics (DCGM_FI_PROF_SM_ACTIVE, DCGM_FI_PROF_PIPE_TENSOR_ACTIVE,
--     DCGM_FI_PROF_DRAM_ACTIVE) on benchmark_metrics and scenario_results.
--   * accelerator_memory_avg_gib (average FB used across scrapes) alongside
--     the existing peak-only field.
--   * accelerator_utilization_avg_pct on scenario_results (already on
--     benchmark_metrics via migration 004).
--   * Missing Tier 1 percentile columns on scenario_results that parallel
--     benchmark_metrics (migrations 001 / 007), plus waiting_requests_max.
--
-- All columns nullable; older rows simply have NULLs and the UI renders "—".

ALTER TABLE benchmark_metrics
    ADD COLUMN IF NOT EXISTS sm_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS sm_active_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tensor_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tensor_active_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dram_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dram_active_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS accelerator_memory_avg_gib DOUBLE PRECISION;

ALTER TABLE scenario_results
    ADD COLUMN IF NOT EXISTS ttft_p95_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS e2e_latency_p95_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS itl_p95_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tpot_p50_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tpot_p90_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tpot_p99_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS waiting_requests_max INTEGER,
    ADD COLUMN IF NOT EXISTS accelerator_utilization_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS accelerator_memory_avg_gib DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS sm_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS sm_active_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tensor_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tensor_active_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dram_active_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dram_active_peak_pct DOUBLE PRECISION;
