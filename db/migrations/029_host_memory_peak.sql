-- PRD-47 PR #4: capture peak host memory (workingSetBytes for the vllm
-- container) during the load phase. PR #5 consumes this column to
-- derive per-family p95 calibration ratios that override the
-- hand-tuned multipliers in the recommender. Nullable; historical rows
-- and runs where the scraper couldn't reach kubelet stay NULL.
ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS host_memory_peak_gib DOUBLE PRECISION;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS host_memory_peak_gib DOUBLE PRECISION;
