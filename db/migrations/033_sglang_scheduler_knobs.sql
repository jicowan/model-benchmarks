-- SGLang scheduler knobs. These are SGLang-specific equivalents of vLLM's
-- max_num_batched_tokens and memory management flags.
--
-- chunked_prefill_size: SGLang's --chunked-prefill-size flag. Controls the
--   per-iteration token budget for chunked prefill. NULL = SGLang default.
--
-- mem_fraction_static: SGLang's --mem-fraction-static flag. Fraction of GPU
--   memory allocated to the KV cache (0.0–1.0). NULL = SGLang default (0.88).

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS chunked_prefill_size    INTEGER,
    ADD COLUMN IF NOT EXISTS mem_fraction_static     REAL;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS chunked_prefill_size    INTEGER,
    ADD COLUMN IF NOT EXISTS mem_fraction_static     REAL;
