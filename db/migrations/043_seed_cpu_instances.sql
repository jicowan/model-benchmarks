-- PRD-67: ARM/CPU (AWS Graviton) inference tier.
--
-- Adds the third accelerator class, 'cpu', alongside 'gpu' and 'neuron', and
-- seeds Graviton3/4/5 instances. CPU rows have NO discrete accelerator:
-- accelerator_count = 0 and accelerator_memory_gib = 0 (host DRAM, in memory_gib,
-- is the serving memory). accelerator_name carries the Graviton generation so the
-- UI/recommender can distinguish ISA tiers.
--
-- vCPU / memory verified against EC2 spec pages 2026-08-10. Graviton is 1
-- thread/core (vcpus == physical cores). NUMA-node count is NOT AWS-documented
-- and is single-socket (likely 1 NUMA node → TP=1); the recommender derives TP
-- from live topology, not from this table. Graviton2 (*6g) is deliberately
-- EXCLUDED — it lacks the SVE/I8MM/BF16 matrix extensions the Arm CPU path needs.

-- 1. Expand the instance_types accelerator_type check to include 'cpu'.
--    Idempotent: drop-and-recreate.
ALTER TABLE instance_types DROP CONSTRAINT IF EXISTS instance_types_accelerator_type_check;
ALTER TABLE instance_types ADD CONSTRAINT instance_types_accelerator_type_check
    CHECK (accelerator_type = ANY (ARRAY['gpu'::text, 'neuron'::text, 'cpu'::text]));

-- 2. Expand the benchmark_runs framework check to include 'vllm-cpu' (keep the
--    existing set from migration 034). Idempotent.
ALTER TABLE benchmark_runs DROP CONSTRAINT IF EXISTS benchmark_runs_framework_check;
ALTER TABLE benchmark_runs ADD CONSTRAINT benchmark_runs_framework_check
    CHECK (framework = ANY (ARRAY['vllm'::text, 'vllm-neuron'::text, 'sglang'::text, 'llm-d'::text, 'vllm-cpu'::text]));

-- 3. Seed Graviton3/4/5 rows. accelerator_count=0, accelerator_memory_gib=0.
INSERT INTO instance_types (name, family, accelerator_type, accelerator_name, accelerator_count, accelerator_memory_gib, vcpus, memory_gib) VALUES
  -- Graviton4 (Neoverse V2, Armv9.0-a: SVE2 + I8MM + BF16). Primary target;
  -- r8g (memory-optimized) is the natural default since CPU inference is
  -- memory-bandwidth bound.
  ('r8g.4xlarge',   'r8g', 'cpu', 'Graviton4', 0, 0,  16,  128),
  ('r8g.8xlarge',   'r8g', 'cpu', 'Graviton4', 0, 0,  32,  256),
  ('r8g.16xlarge',  'r8g', 'cpu', 'Graviton4', 0, 0,  64,  512),
  ('r8g.24xlarge',  'r8g', 'cpu', 'Graviton4', 0, 0,  96,  768),
  ('r8g.48xlarge',  'r8g', 'cpu', 'Graviton4', 0, 0, 192, 1536), -- large / MoE (all experts in DRAM)
  ('m8g.12xlarge',  'm8g', 'cpu', 'Graviton4', 0, 0,  48,  192),
  ('m8g.24xlarge',  'm8g', 'cpu', 'Graviton4', 0, 0,  96,  384), -- balanced
  ('m8g.48xlarge',  'm8g', 'cpu', 'Graviton4', 0, 0, 192,  768),
  ('c8g.12xlarge',  'c8g', 'cpu', 'Graviton4', 0, 0,  48,   96),
  ('c8g.24xlarge',  'c8g', 'cpu', 'Graviton4', 0, 0,  96,  192), -- compute-leaning, tiny models

  -- Graviton3 (Neoverse V1, Armv8.4-a: SVE + I8MM + BF16). Fallback — has the
  -- matrix extensions, misses only the SVE2/Armv9 generation win.
  ('r7g.8xlarge',   'r7g', 'cpu', 'Graviton3', 0, 0,  32,  256),
  ('r7g.16xlarge',  'r7g', 'cpu', 'Graviton3', 0, 0,  64,  512),
  ('m7g.16xlarge',  'm7g', 'cpu', 'Graviton3', 0, 0,  64,  256),
  ('c7g.16xlarge',  'c7g', 'cpu', 'Graviton3', 0, 0,  64,  128),

  -- Graviton5 (Neoverse V3, Armv9+). GA in us-east-2 as m9g/m9gd/c9g
  -- (memory-optimized r9g not yet offered). Strictly >= Graviton4.
  ('m9g.16xlarge',  'm9g', 'cpu', 'Graviton5', 0, 0,  64,  256),
  ('m9g.24xlarge',  'm9g', 'cpu', 'Graviton5', 0, 0,  96,  384),
  ('c9g.16xlarge',  'c9g', 'cpu', 'Graviton5', 0, 0,  64,  128)

ON CONFLICT (name) DO NOTHING;
