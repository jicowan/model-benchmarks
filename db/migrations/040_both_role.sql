-- PRD-63: co-located prefill+decode ("both") pool role for disaggregated runs.
-- Additive + idempotent, mirrored to test_suite_runs (same pattern as 036/039).
--
-- A "both" pod serves short requests locally (no KV hop) AND can decode a
-- remote-prefill request the EPP routes to it — the middle ground between fully
-- co-located (PRD-56, no EPP) and fully disaggregated (PRD-58, every request
-- pays the hop). It is ADDITIVE: a disaggregated run may set any subset of
-- {prefill, decode, both} with a total >= 1 node, so long as some decode-capable
-- pool (decode or both) exists whenever prefill > 0.
--
--   both_replicas: how many co-located pods (the "B" in xPyDzB). NULL/0 ⇒ no
--     both pool — today's exact PD behavior, byte-identical manifest.
--   both_tp: within-node tensor-parallel for a both pod (default 1). NULL for
--     non-disaggregated / no-both runs.
--   both_max_num_batched_tokens: optional per-role scheduler override mirroring
--     PRD-64's prefill/decode knobs. NULL ⇒ the both role uses the shared
--     max_num_batched_tokens. (both_pp is intentionally omitted — per-role
--     PP > 1 is a non-goal, same as prefill/decode.)

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS both_replicas                INTEGER,
    ADD COLUMN IF NOT EXISTS both_tp                      INTEGER,
    ADD COLUMN IF NOT EXISTS both_max_num_batched_tokens  INTEGER;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS both_replicas                INTEGER,
    ADD COLUMN IF NOT EXISTS both_tp                      INTEGER,
    ADD COLUMN IF NOT EXISTS both_max_num_batched_tokens  INTEGER;
