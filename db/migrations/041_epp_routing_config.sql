-- PRD-61: run-tunable EPP EndpointPickerConfig routing knobs for disaggregated
-- runs. Additive + idempotent, mirrored to test_suite_runs (pattern of 036/040).
--
-- The EPP's routing/scheduling config (prefix-cache producer params, per-profile
-- scorer weights, the disaggregation-trigger threshold, decider strategy) was
-- hardcoded in llmd-disaggregated.yaml.tmpl. This PRD makes it a per-run
-- parameter so a user can sweep routing configs and — via the PRD-62 metrics
-- already on the report — measure the effect. All columns NULLABLE; NULL means
-- "use the shipped default", so existing rows and any run that sets nothing
-- render the byte-identical pd-config.yaml PD ships today.
--
--   pd_noncached_tokens: disaggregate only when the uncached prompt suffix >= N
--     tokens. 0 is MEANINGFUL (never disaggregate) — distinct from NULL (default
--     16). Only affects runs with a dedicated prefill pool; a both-only pool has
--     no prefill candidate and never disaggregates regardless (PRD-63).
--   pd_prefix_cache_weight / pd_queue_scorer_weight: SHARED scorer weights applied
--     to both the prefill and decode schedulingProfiles (defaults 2 / 1).
--     Per-profile weights are a documented follow-on.
--   pd_max_prefix_blocks / pd_lru_capacity_per_server: approx-prefix-cache
--     producer match-depth / capacity (defaults 256 / 31250).
--   pd_decider_strategy: 'threshold' (default, shipped) vs 'always'. 'always' is
--     gated on peakPrefillThroughput calibration (out of scope) — persisted for
--     forward-compat, not yet rendered. NULL => threshold.
--
-- Only meaningful for deployment_mode='disaggregated'; NULL for single, PP
-- co-located distributed, and historical rows.

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS pd_noncached_tokens        INTEGER,
    ADD COLUMN IF NOT EXISTS pd_prefix_cache_weight     INTEGER,
    ADD COLUMN IF NOT EXISTS pd_queue_scorer_weight     INTEGER,
    ADD COLUMN IF NOT EXISTS pd_max_prefix_blocks       INTEGER,
    ADD COLUMN IF NOT EXISTS pd_lru_capacity_per_server INTEGER,
    ADD COLUMN IF NOT EXISTS pd_decider_strategy        TEXT;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS pd_noncached_tokens        INTEGER,
    ADD COLUMN IF NOT EXISTS pd_prefix_cache_weight     INTEGER,
    ADD COLUMN IF NOT EXISTS pd_queue_scorer_weight     INTEGER,
    ADD COLUMN IF NOT EXISTS pd_max_prefix_blocks       INTEGER,
    ADD COLUMN IF NOT EXISTS pd_lru_capacity_per_server INTEGER,
    ADD COLUMN IF NOT EXISTS pd_decider_strategy        TEXT;
