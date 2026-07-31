-- PRD-56: add 'llm-d' as a valid framework — the multi-node distributed
-- inference runtime (vLLM under a LeaderWorkerSet across N GPU nodes). The
-- orchestrator selects the distributed deploy path when framework='llm-d' and
-- node_count>1. Idempotent: drop-and-recreate the check constraint.
ALTER TABLE benchmark_runs DROP CONSTRAINT IF EXISTS benchmark_runs_framework_check;
ALTER TABLE benchmark_runs ADD CONSTRAINT benchmark_runs_framework_check
    CHECK (framework = ANY (ARRAY['vllm'::text, 'vllm-neuron'::text, 'sglang'::text, 'llm-d'::text]));
