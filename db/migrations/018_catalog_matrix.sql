-- PRD-30: Move the benchmark/catalog seeding matrix from the
-- accelbench-catalog-scripts ConfigMap into the DB. Seeded from the
-- current scripts/catalog-matrix.yaml so day-1 behavior is unchanged.

CREATE TABLE IF NOT EXISTS catalog_models (
    id         SERIAL PRIMARY KEY,
    hf_id      TEXT UNIQUE NOT NULL,
    family     TEXT,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS catalog_instance_types (
    id         SERIAL PRIMARY KEY,
    name       TEXT UNIQUE NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS catalog_seed_defaults (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    framework_version    TEXT    NOT NULL,
    scenario             TEXT    NOT NULL,
    dataset              TEXT    NOT NULL,
    min_duration_seconds INTEGER NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tracks the in-process seed goroutine status so the UI polling endpoint
-- and multi-tab coordination can replace the old K8s Job listing.
CREATE TABLE IF NOT EXISTS catalog_seed_status (
    id            TEXT PRIMARY KEY,          -- uuid
    status        TEXT NOT NULL,             -- 'active' | 'completed' | 'failed' | 'interrupted'
    total         INTEGER NOT NULL DEFAULT 0,
    completed     INTEGER NOT NULL DEFAULT 0,
    dry_run       BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_catalog_seed_status_started_at
    ON catalog_seed_status (started_at DESC);

-- Seed matrix contents from scripts/catalog-matrix.yaml.
INSERT INTO catalog_seed_defaults
    (id, framework_version, scenario, dataset, min_duration_seconds)
VALUES
    (1, 'v0.19.0', 'chatbot', 'synthetic', 180)
ON CONFLICT (id) DO NOTHING;

INSERT INTO catalog_models (hf_id, family) VALUES
    ('meta-llama/Llama-3.1-8B-Instruct',            'llama'),
    ('meta-llama/Llama-3.1-70B-Instruct',           'llama'),
    ('meta-llama/Llama-3.3-70B-Instruct',           'llama'),
    ('mistralai/Mistral-7B-Instruct-v0.3',          'mistral'),
    ('mistralai/Mixtral-8x7B-Instruct-v0.1',        'mistral'),
    ('Qwen/Qwen2.5-7B-Instruct',                    'qwen'),
    ('Qwen/Qwen2.5-72B-Instruct',                   'qwen'),
    ('google/gemma-2-9b-it',                        'gemma'),
    ('google/gemma-2-27b-it',                       'gemma'),
    ('deepseek-ai/DeepSeek-R1-Distill-Llama-8B',    'deepseek'),
    ('deepseek-ai/DeepSeek-R1-Distill-Llama-70B',   'deepseek'),
    ('microsoft/Phi-4',                             'phi')
ON CONFLICT (hf_id) DO NOTHING;

INSERT INTO catalog_instance_types (name) VALUES
    ('g5.xlarge'),
    ('g5.2xlarge'),
    ('g5.48xlarge'),
    ('g6.xlarge'),
    ('g6e.xlarge'),
    ('g6e.2xlarge'),
    ('g6e.48xlarge'),
    ('p4d.24xlarge'),
    ('p5.48xlarge'),
    ('p5e.48xlarge')
ON CONFLICT (name) DO NOTHING;
