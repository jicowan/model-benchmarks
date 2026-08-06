-- PRD-66 Part 2: settable image tags for the two multi-node images, mirroring
-- the single-node vLLM framework_version / sglang_version pattern (PRD-34).
--
--   llmd_version    — the co-located PP image (ghcr.io/llm-d/llm-d-aws). Versions
--                     INDEPENDENTLY of the bundled vLLM engine, so it needs its
--                     own field (framework_version would ask GHCR for a vLLM tag
--                     that doesn't exist). Default = the current pin, v0.8.1.
--   pd_vllm_version — the disaggregated D/P image (vllm/vllm-openai). DISTINCT
--                     from single-node framework_version: D/P is pinned to a
--                     cu13/NIXL-specific vLLM (v0.25.0) that legitimately differs
--                     from whatever single-node runs. Default = v0.25.0.
--
-- Both default to the values hardcoded in the code today, so an un-set row is
-- byte-identical to pre-migration behavior.
ALTER TABLE tool_versions
    ADD COLUMN IF NOT EXISTS llmd_version TEXT NOT NULL DEFAULT 'v0.8.1';
ALTER TABLE tool_versions
    ADD COLUMN IF NOT EXISTS pd_vllm_version TEXT NOT NULL DEFAULT 'v0.25.0';
