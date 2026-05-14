-- PRD-50: persist the three Run:ai streamer knobs so the knobs a
-- user sets at submit time round-trip through report detail and
-- manifest export. All three are nullable: NULL means "use the
-- default" (auto mode, concurrency=16, auto-sized memory limit).
--
-- streamer_mode           'auto' | 'off' — 'auto' on for S3 models
-- streamer_concurrency    1..32 threads filling the shared buffer
-- streamer_memory_limit_gib cap on the shared CPU buffer in GiB

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS streamer_mode             TEXT,
    ADD COLUMN IF NOT EXISTS streamer_concurrency      INTEGER,
    ADD COLUMN IF NOT EXISTS streamer_memory_limit_gib INTEGER;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS streamer_mode             TEXT,
    ADD COLUMN IF NOT EXISTS streamer_concurrency      INTEGER,
    ADD COLUMN IF NOT EXISTS streamer_memory_limit_gib INTEGER;
