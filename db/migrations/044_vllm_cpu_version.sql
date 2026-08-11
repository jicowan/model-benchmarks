-- PRD-67 §10a: settable image tag for the ARM/CPU (Graviton) vLLM image,
-- mirroring framework_version / sglang_version / pd_vllm_version (PRD-34/66).
--
--   vllm_cpu_version — the arm64 CPU image tag (vllm/vllm-openai-cpu:<ver>-arm64).
--                      DISTINCT from framework_version: the CPU image is a
--                      different repo (`-cpu`) with its own arm64 release cadence
--                      (framework_version would ask Docker Hub for a `-cpu` tag
--                      that may not track the GPU tag). Default = a validated
--                      recent arm64 tag, v0.27.0 (arm64 images exist v0.18.0+;
--                      the v0.11.2 "since" figure is for wheels, not images).
--
-- Defaults to the value in DefaultVLLMCPUVersion so an un-set row is
-- byte-identical to code behavior.
ALTER TABLE tool_versions
    ADD COLUMN IF NOT EXISTS vllm_cpu_version TEXT NOT NULL DEFAULT 'v0.27.0';
