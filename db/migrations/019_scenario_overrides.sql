-- PRD-32: Per-scenario inference-perf overrides.
--
-- Each column is nullable; NULL means "inherit the code-defined value from
-- internal/scenario/builtin.go". An empty table means all scenarios use
-- their built-in values — day-1 behavior is unchanged.
--
-- The orchestrator's resolveScenario() loads the row (if any) for the
-- requested scenario_id and merges non-NULL columns over the code-defined
-- scenario before rendering the inference-perf config.

CREATE TABLE IF NOT EXISTS scenario_overrides (
    scenario_id  TEXT PRIMARY KEY,
    num_workers  INTEGER,
    streaming    BOOLEAN,
    input_mean   INTEGER,
    output_mean  INTEGER,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
