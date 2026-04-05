-- Extended metrics for PRD-14
-- Add TPOT percentiles, latency breakdown, throughput breakdown, cache metrics

ALTER TABLE benchmark_metrics
    ADD COLUMN IF NOT EXISTS tpot_p50_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tpot_p90_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tpot_p99_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS prefill_time_p50_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS decode_time_p50_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS queue_time_p50_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS prompt_throughput_tps DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS generation_throughput_tps DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_cache_utilization_avg_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_cache_utilization_peak_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS prefix_cache_hit_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS preemption_count INTEGER,
    ADD COLUMN IF NOT EXISTS running_requests_avg DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS running_requests_max INTEGER;
