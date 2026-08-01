-- PRD-64: per-role scheduler override for prefill/decode disaggregated runs.
-- Additive + idempotent, mirrored to test_suite_runs (same pattern as 036/038).
--
-- Prefill (compute-bound) and decode (memory-bound) want different
-- --max-num-batched-tokens. These optional per-role overrides let a D/P run set
-- them independently. NULL ⇒ that role uses the shared max_num_batched_tokens
-- column (which itself, if NULL, ⇒ vLLM default) — i.e. today's exact behavior.
-- Only meaningful for deployment_mode='disaggregated'; NULL for single, PP
-- co-located distributed, and historical rows.
--
-- The SHARED knobs (max_model_len, max_num_batched_tokens, kv_cache_dtype,
-- quantization) already exist and are already persisted for distributed runs —
-- this PRD only surfaces them in the UI, no schema change for them.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS prefill_max_num_batched_tokens INTEGER,
    ADD COLUMN IF NOT EXISTS decode_max_num_batched_tokens  INTEGER;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS prefill_max_num_batched_tokens INTEGER,
    ADD COLUMN IF NOT EXISTS decode_max_num_batched_tokens  INTEGER;
