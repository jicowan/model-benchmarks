-- Add sglang_version to the tool_versions singleton. SGLang is wired in
-- alongside vLLM as a third generative-LLM serving framework on GPU
-- instance types. framework_version (vLLM) and sglang_version are
-- per-framework; the orchestrator picks the right one based on the
-- benchmark run's framework field.

ALTER TABLE tool_versions
    ADD COLUMN IF NOT EXISTS sglang_version TEXT NOT NULL DEFAULT 'v0.4.10.post2-cu126';

-- Expand the framework check constraint to include sglang.
ALTER TABLE benchmark_runs DROP CONSTRAINT IF EXISTS benchmark_runs_framework_check;
ALTER TABLE benchmark_runs ADD CONSTRAINT benchmark_runs_framework_check
    CHECK (framework = ANY (ARRAY['vllm'::text, 'vllm-neuron'::text, 'sglang'::text]));
