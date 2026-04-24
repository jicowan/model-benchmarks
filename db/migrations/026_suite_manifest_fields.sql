-- PRD-41: suite manifest export.
--
-- test_suite_runs needs framework / framework_version / model_s3_uri
-- to produce a Kubernetes manifest matching what was actually deployed.
-- These fields exist on SuiteRunRequest at creation time but were never
-- persisted — suites that completed before this migration will have
-- NULL values. The handler falls back to accelerator-type-derived
-- defaults in that case.

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS framework         TEXT,
    ADD COLUMN IF NOT EXISTS framework_version TEXT,
    ADD COLUMN IF NOT EXISTS model_s3_uri      TEXT;
