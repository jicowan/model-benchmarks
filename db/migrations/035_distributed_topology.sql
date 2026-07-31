-- PRD-57: persist a distributed run's topology so multi-node benchmarks are
-- first-class, launchable, and reproducible from the DB (PRD-56 carried this
-- only transiently on RunConfig). Additive + idempotent, mirrored onto
-- test_suite_runs (same pattern as migration 033).
--
--   deployment_mode: NULL/'single' (default = today's single-instance behavior)
--     or 'distributed' (multi-node llm-d). Lets read paths distinguish the two
--     without inferring from other columns. UI displays it verbatim and
--     tolerates unknown values (CLAUDE.md), so surfacing it is low-risk.
--   node_count: LWS group size (number of GPU nodes). NULL/1 for single.
--     Maps to RunConfig.NodeCount.
--   pipeline_parallel_degree: PP across nodes. NULL/1 for single. TP continues
--     to use the existing tensor_parallel_degree column, now interpreted as
--     WITHIN-node TP for distributed runs.
--   network_mode: 'efa' (default, preferred RDMA fabric) or 'tcp' (NCCL over
--     sockets, no EFA). NULL for single-instance runs. node_pool_override is an
--     operational knob, NOT a run property, so it stays transient (not persisted).

ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS deployment_mode          TEXT,
    ADD COLUMN IF NOT EXISTS node_count               INTEGER,
    ADD COLUMN IF NOT EXISTS pipeline_parallel_degree INTEGER,
    ADD COLUMN IF NOT EXISTS network_mode             TEXT;

ALTER TABLE test_suite_runs
    ADD COLUMN IF NOT EXISTS deployment_mode          TEXT,
    ADD COLUMN IF NOT EXISTS node_count               INTEGER,
    ADD COLUMN IF NOT EXISTS pipeline_parallel_degree INTEGER,
    ADD COLUMN IF NOT EXISTS network_mode             TEXT;
