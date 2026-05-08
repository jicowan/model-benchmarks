-- PRD-46: persist the three vLLM scheduler flags AccelBench sets
-- at deploy time so historical benchmark rows can be reproduced
-- byte-for-byte from the DB. Only two columns are added here because
-- --max-num-seqs is derived from `concurrency` at manifest-render
-- time (same source of truth, no independent value to persist).
--
-- Landed in PR #1 (max-num-batched-tokens); kv_cache_dtype's DB
-- read/write is wired in PR #3.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS max_num_batched_tokens INTEGER,
    ADD COLUMN IF NOT EXISTS kv_cache_dtype         TEXT;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS max_num_batched_tokens INTEGER,
    ADD COLUMN IF NOT EXISTS kv_cache_dtype         TEXT;
