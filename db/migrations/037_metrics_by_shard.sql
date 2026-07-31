-- PRD-59: per-node / per-role GPU metrics for distributed runs. Additive +
-- idempotent. TWO changes, both back-compatible:
--
-- 1. benchmark_metrics_by_shard — a child table holding one row per serving
--    shard ({node, role}) of a DISTRIBUTED run's GPU telemetry. Written ONLY for
--    distributed/disaggregated runs; single-instance runs write NOTHING here and
--    keep reporting exactly as before. The flat scalar columns on
--    benchmark_metrics remain the group roll-up (unchanged meaning). A child
--    table (vs JSONB) is chosen for queryability (Compare / future aggregation).
--    role is '' for co-located distributed runs, 'prefill'/'decode' for PD.
--
-- 2. accelerator_memory_total_gib on benchmark_metrics — the HONEST group memory
--    total (sum of per-node peak memory across the group). The existing
--    accelerator_memory_peak_gib is DELIBERATELY left as-is (hottest single node)
--    so no existing column silently changes meaning (PRD-59 risk note prefers the
--    additive fix). NULL on historical + single-node rows (for single-node,
--    total == peak, but we only populate this for distributed runs to keep the
--    single-node write byte-identical).

CREATE TABLE IF NOT EXISTS benchmark_metrics_by_shard (
    id                     BIGSERIAL PRIMARY KEY,
    run_id                 UUID NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    node                   TEXT NOT NULL,            -- serving node identity (HostIP)
    role                   TEXT NOT NULL DEFAULT '', -- '' co-located | 'prefill' | 'decode'
    samples                INTEGER NOT NULL DEFAULT 0,
    utilization_avg_pct    DOUBLE PRECISION,
    utilization_peak_pct   DOUBLE PRECISION,
    memory_avg_gib         DOUBLE PRECISION,
    memory_peak_gib        DOUBLE PRECISION,
    sm_active_avg_pct      DOUBLE PRECISION,
    sm_active_peak_pct     DOUBLE PRECISION,
    tensor_active_avg_pct  DOUBLE PRECISION,
    tensor_active_peak_pct DOUBLE PRECISION,
    dram_active_avg_pct    DOUBLE PRECISION,
    dram_active_peak_pct   DOUBLE PRECISION,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_metrics_by_shard_run_id
    ON benchmark_metrics_by_shard(run_id);

ALTER TABLE benchmark_metrics
    ADD COLUMN IF NOT EXISTS accelerator_memory_total_gib DOUBLE PRECISION;
