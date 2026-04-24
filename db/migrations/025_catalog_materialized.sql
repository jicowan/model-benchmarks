-- PRD-37: materialized view backing the Catalog page.
--
-- ListCatalog used to run a five-table JOIN on every request; this
-- view pre-joins `benchmark_runs` × `models` × `instance_types` ×
-- `benchmark_metrics` and bakes in the
-- `status = 'completed' AND superseded = FALSE` filter so the API
-- only needs an indexed lookup.
--
-- Column order matches internal/database/catalog.go's rows.Scan(...)
-- sequence 1:1. Callers also reference many of these by filter /
-- sort name (see allowedSortColumns) — preserve the naming when
-- altering the view.
--
-- Refresh is driven by cmd/server/catalog_refresh.go on a 5-minute
-- ticker inside each API pod. REFRESH MATERIALIZED VIEW CONCURRENTLY
-- requires at least one unique index, which idx_catalog_rows_run_id
-- provides.

CREATE MATERIALIZED VIEW IF NOT EXISTS catalog_rows AS
SELECT
    br.id                                         AS run_id,
    m.hf_id                                       AS hf_id,
    m.model_family                                AS model_family,
    m.parameter_count                             AS parameter_count,
    it.name                                       AS instance_type_name,
    it.family                                     AS instance_family,
    it.accelerator_type                           AS accelerator_type,
    it.accelerator_name                           AS accelerator_name,
    it.accelerator_count                          AS accelerator_count,
    it.accelerator_memory_gib                     AS accelerator_memory_gib,
    br.framework                                  AS framework,
    br.framework_version                          AS framework_version,
    br.tensor_parallel_degree                     AS tensor_parallel_degree,
    br.quantization                               AS quantization,
    br.concurrency                                AS concurrency,
    br.input_sequence_length                      AS input_sequence_length,
    br.output_sequence_length                     AS output_sequence_length,
    br.completed_at                               AS completed_at,
    bm.ttft_p50_ms, bm.ttft_p95_ms, bm.ttft_p99_ms,
    bm.e2e_latency_p50_ms, bm.e2e_latency_p95_ms, bm.e2e_latency_p99_ms,
    bm.itl_p50_ms, bm.itl_p95_ms, bm.itl_p99_ms,
    bm.throughput_per_request_tps, bm.throughput_aggregate_tps,
    bm.requests_per_second,
    bm.successful_requests, bm.failed_requests,
    bm.accelerator_utilization_pct, bm.accelerator_utilization_avg_pct,
    bm.accelerator_memory_peak_gib, bm.accelerator_memory_avg_gib,
    bm.sm_active_avg_pct, bm.tensor_active_avg_pct, bm.dram_active_avg_pct
FROM benchmark_runs br
JOIN models m           ON br.model_id          = m.id
JOIN instance_types it  ON br.instance_type_id  = it.id
JOIN benchmark_metrics bm ON bm.run_id          = br.id
WHERE br.status = 'completed' AND br.superseded = FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_rows_run_id
    ON catalog_rows (run_id);

CREATE INDEX IF NOT EXISTS idx_catalog_rows_hf_id
    ON catalog_rows (hf_id);

CREATE INDEX IF NOT EXISTS idx_catalog_rows_model_family
    ON catalog_rows (model_family);

CREATE INDEX IF NOT EXISTS idx_catalog_rows_instance_family
    ON catalog_rows (instance_family);

CREATE INDEX IF NOT EXISTS idx_catalog_rows_accelerator_type
    ON catalog_rows (accelerator_type);

CREATE INDEX IF NOT EXISTS idx_catalog_rows_completed_at
    ON catalog_rows (completed_at DESC);
