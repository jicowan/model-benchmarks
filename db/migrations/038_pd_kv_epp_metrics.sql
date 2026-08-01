-- PRD-62: prefill/decode + KV-transfer + EPP-routing metrics. Additive +
-- idempotent, mirrored to scenario_results (same pattern as prior migrations).
-- All columns NULL on single-instance, co-located, and historical runs — only
-- disaggregated runs whose series populated write non-NULL values. Run-level
-- summaries only (per-role phase/GPU detail lives in PRD-59's
-- benchmark_metrics_by_shard); no new metrics for single-node runs.
--
--   kv_transfer_*        : NIXL KV hand-off cost (decode side). Populate only
--                          when the vLLM image ships NIXL >= 0.7.1 (else NULL).
--   *_time_server_avg_ms : vLLM server-measured prefill/decode phase wall-time.
--   external_prefix_cache_hit_rate : cross-instance (connector) cache reuse %.
--   disagg_*             : EPP disaggregation-decision counts + engaged rate %
--                          (prefill-decode vs decode-only) — "did PD fire".
--   pool_*               : EPP-observed pool KV pressure / queue depth.

ALTER TABLE benchmark_metrics
    ADD COLUMN IF NOT EXISTS kv_transfer_time_avg_ms         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_transfer_bytes_total         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_transfer_failures            DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS prefill_time_server_avg_ms      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS decode_time_server_avg_ms       DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS external_prefix_cache_hit_rate  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_prefill_decode_count     DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_decode_only_count        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_engaged_rate_pct         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS pool_kv_cache_util_pct          DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS pool_queue_size_avg             DOUBLE PRECISION;

ALTER TABLE scenario_results
    ADD COLUMN IF NOT EXISTS kv_transfer_time_avg_ms         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_transfer_bytes_total         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS kv_transfer_failures            DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS prefill_time_server_avg_ms      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS decode_time_server_avg_ms       DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS external_prefix_cache_hit_rate  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_prefill_decode_count     DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_decode_only_count        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disagg_engaged_rate_pct         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS pool_kv_cache_util_pct          DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS pool_queue_size_avg             DOUBLE PRECISION;
