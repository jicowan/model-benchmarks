-- PRD-47 follow-up: rename model_family → model_type so the column
-- carries HuggingFace's canonical model_type value (e.g. llama, qwen2,
-- qwen3, phi3, mistral, gpt_oss). The old name was a misnomer — values
-- like "qwen" conflated architecturally-distinct families (Qwen2 and
-- Qwen3 have different loader profiles, as the Qwen3-14B TP=4 shard
-- pathology showed).
--
-- Existing values are left in place. Rows written after this migration
-- will carry the HF model_type string. The per-family calibration
-- query groups by model_type; old "qwen" values and new "qwen3" values
-- will not share a bucket — acceptable because we've only accumulated
-- a handful of calibration rows and restarting on a clean taxonomy is
-- preferable to a heuristic remap.

-- Source of truth: rename on the models table.
ALTER TABLE models
    RENAME COLUMN model_family TO model_type;

-- Rebuild the catalog materialized view so its output column name
-- matches. The view is pre-joined + filtered, so DROP/CREATE is cheap
-- (catalog_refresh.go repopulates it on a 5-minute ticker).
DROP MATERIALIZED VIEW IF EXISTS catalog_rows CASCADE;

CREATE MATERIALIZED VIEW catalog_rows AS
SELECT
    br.id                                         AS run_id,
    m.hf_id                                       AS hf_id,
    m.model_type                                  AS model_type,
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

CREATE UNIQUE INDEX idx_catalog_rows_run_id
    ON catalog_rows (run_id);

CREATE INDEX idx_catalog_rows_hf_id
    ON catalog_rows (hf_id);

CREATE INDEX idx_catalog_rows_model_type
    ON catalog_rows (model_type);

CREATE INDEX idx_catalog_rows_instance_family
    ON catalog_rows (instance_family);

CREATE INDEX idx_catalog_rows_accelerator_type
    ON catalog_rows (accelerator_type);

CREATE INDEX idx_catalog_rows_completed_at
    ON catalog_rows (completed_at DESC);
