-- PRD-34: Make vLLM (framework) and inference-perf versions configurable at the
-- platform level instead of hardcoded in code/Helm. Moves framework_version out
-- of catalog_seed_defaults (seeding-specific) into a shared tool_versions
-- singleton that all run types read from.

CREATE TABLE IF NOT EXISTS tool_versions (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    framework_version      TEXT NOT NULL,
    inference_perf_version TEXT NOT NULL,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO tool_versions (id, framework_version, inference_perf_version)
VALUES (1, 'v0.19.0', 'v0.2.0')
ON CONFLICT (id) DO NOTHING;

-- framework_version moves to tool_versions; catalog_seed_defaults is now purely
-- about what to seed (scenario/dataset/min_duration), not which tools to run.
ALTER TABLE catalog_seed_defaults DROP COLUMN IF EXISTS framework_version;
