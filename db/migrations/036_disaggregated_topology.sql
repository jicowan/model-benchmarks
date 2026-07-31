-- PRD-58: prefill/decode (PD) disaggregation. Adds a third deployment sub-mode
-- ('disaggregated') and the per-role topology + KV-transfer columns that a
-- disaggregated run needs, on top of PRD-57's migration 035
-- (deployment_mode/node_count/pipeline_parallel_degree/network_mode). Additive
-- + idempotent, mirrored onto test_suite_runs (same pattern as 033/035).
--
-- Disaggregated runs split inference into two independently-scaled pod groups:
--   prefill (compute-bound, produces the KV cache) and decode (bandwidth-bound,
--   generates tokens from that cache). The KV cache is transferred prefill→decode
--   over NIXL (validated on TCP; EFA is the perf axis via network_mode). Routing
--   is KV/role/load-aware via the Gateway API InferencePool + Endpoint Picker.
--
--   deployment_mode: PRD-57 used NULL/'single'/'distributed'. This PRD adds
--     'disaggregated'. deployment_mode is a plain TEXT column (no CHECK), so
--     no constraint widening is needed — the API validates the allowed set.
--   prefill_replicas / decode_replicas: how many pods per role (the xPyD ratio's
--     x and y). >= 1 each for a disaggregated run.
--   prefill_tp / prefill_pp / decode_tp / decode_pp: per-role parallelism. TP is
--     within-node, PP spans nodes; INDEPENDENT knobs per role (a role may run
--     TP=1 = no tensor sharding, matching the AWS reference prefill×8 TP=1 +
--     decode×2 TP=4). NULL for non-disaggregated runs.
--   kv_connector: the vLLM KV-transfer connector (e.g. 'nixl'). NULL for
--     co-located / single.
--   kv_transfer_backend: the transport backend (e.g. 'tcp' or 'libfabric').
--     NULL for co-located / single.
--
-- Per PRD-58 Design §schema decision (a): prefill and decode share ONE
-- instance_type_id (same hardware, different pod shapes) — no per-role instance
-- type / join table yet. Add that only if a concrete need appears.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS prefill_replicas    INTEGER,
    ADD COLUMN IF NOT EXISTS prefill_tp          INTEGER,
    ADD COLUMN IF NOT EXISTS prefill_pp          INTEGER,
    ADD COLUMN IF NOT EXISTS decode_replicas     INTEGER,
    ADD COLUMN IF NOT EXISTS decode_tp           INTEGER,
    ADD COLUMN IF NOT EXISTS decode_pp           INTEGER,
    ADD COLUMN IF NOT EXISTS kv_connector        TEXT,
    ADD COLUMN IF NOT EXISTS kv_transfer_backend TEXT;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS prefill_replicas    INTEGER,
    ADD COLUMN IF NOT EXISTS prefill_tp          INTEGER,
    ADD COLUMN IF NOT EXISTS prefill_pp          INTEGER,
    ADD COLUMN IF NOT EXISTS decode_replicas     INTEGER,
    ADD COLUMN IF NOT EXISTS decode_tp           INTEGER,
    ADD COLUMN IF NOT EXISTS decode_pp           INTEGER,
    ADD COLUMN IF NOT EXISTS kv_connector        TEXT,
    ADD COLUMN IF NOT EXISTS kv_transfer_backend TEXT;
