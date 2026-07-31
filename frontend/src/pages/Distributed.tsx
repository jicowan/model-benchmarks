import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { createRun, listInstanceTypes, listScenarios } from "../api";
import type { InstanceType, Scenario, RunRequest } from "../types";
import ModelCombobox from "../components/ModelCombobox";

// PRD-57: dedicated composer for a multi-node DISTRIBUTED (llm-d) benchmark.
// Deliberately separate from Run.tsx — topology is user-specified (no
// recommender), framework is fixed to llm-d, and the deployment maps to the
// PRD-56 orchestrator path. vLLM multi-node mapping: TP = GPUs per node
// (within-node), PP = node count (across-node).
export default function Distributed() {
  const navigate = useNavigate();
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const [form, setForm] = useState({
    model_hf_id: "",
    model_hf_revision: "main",
    instance_type_name: "",
    node_count: 2,
    network_mode: "efa" as "efa" | "tcp",
    concurrency: 16,
    input_sequence_length: 512,
    output_sequence_length: 256,
    scenario_id: "chatbot",
    hf_token: "",
  });

  useEffect(() => {
    listInstanceTypes()
      .then((types) => setInstanceTypes(types.filter((t) => t.accelerator_type === "gpu")))
      .catch(() => setInstanceTypes([]));
    listScenarios()
      .then(setScenarios)
      .catch(() => setScenarios([]));
  }, []);

  const selectedInstance = useMemo(
    () => instanceTypes.find((t) => t.name === form.instance_type_name),
    [instanceTypes, form.instance_type_name],
  );

  // Topology is derived from the doc's rule: TP = GPUs/node, PP = node count.
  const gpusPerNode = selectedInstance?.accelerator_count ?? 0;
  const tp = gpusPerNode; // within-node tensor parallel
  const pp = form.node_count; // across-node pipeline parallel
  const totalGPUs = gpusPerNode * form.node_count;

  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  async function submit() {
    setError("");
    if (!form.model_hf_id) return setError("Select a model.");
    if (!form.instance_type_name) return setError("Select a GPU instance type.");
    if (form.node_count < 2) return setError("Distributed runs need at least 2 nodes.");
    if (gpusPerNode < 1) return setError("Selected instance has no GPUs.");

    const req: RunRequest = {
      model_hf_id: form.model_hf_id,
      model_hf_revision: form.model_hf_revision,
      instance_type_name: form.instance_type_name,
      framework: "llm-d",
      framework_version: "",
      tensor_parallel_degree: tp,
      concurrency: form.concurrency,
      input_sequence_length: form.input_sequence_length,
      output_sequence_length: form.output_sequence_length,
      scenario_id: form.scenario_id,
      run_type: "on_demand",
      hf_token: form.hf_token || undefined,
      deployment_mode: "distributed",
      node_count: form.node_count,
      pipeline_parallel_degree: pp,
      network_mode: form.network_mode,
    };

    setSubmitting(true);
    try {
      const { id } = await createRun(req);
      navigate(`/results/${id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to launch distributed run.");
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col">
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-0">DISTRIBUTED BENCHMARK</span>
          <span className="text-ink-2">— multi-node llm-d</span>
        </div>
      </div>

      <div className="p-6 max-w-2xl flex flex-col gap-5">
        {error && (
          <div className="border border-danger/50 bg-danger/5 text-danger font-mono text-[12px] px-3 py-2">
            {error}
          </div>
        )}

        {/* Model */}
        <label className="flex flex-col gap-1.5">
          <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Model</span>
          <ModelCombobox
            value={form.model_hf_id}
            onChange={(v) => set("model_hf_id", v)}
          />
        </label>

        {/* Instance type (GPU only) */}
        <label className="flex flex-col gap-1.5">
          <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">
            Instance type (per node)
          </span>
          <select
            className="input w-full"
            value={form.instance_type_name}
            onChange={(e) => set("instance_type_name", e.target.value)}
          >
            <option value="">Select a GPU instance…</option>
            {instanceTypes.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name} — {t.accelerator_count}× {t.accelerator_name}
              </option>
            ))}
          </select>
        </label>

        {/* Node count + network mode */}
        <div className="grid grid-cols-2 gap-4">
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Node count</span>
            <input
              type="number"
              min={2}
              className="input w-full"
              value={form.node_count}
              onChange={(e) => set("node_count", Math.max(2, Number(e.target.value) || 2))}
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Network fabric</span>
            <select
              className="input w-full"
              value={form.network_mode}
              onChange={(e) => set("network_mode", e.target.value as "efa" | "tcp")}
            >
              <option value="efa">EFA / RDMA (preferred)</option>
              <option value="tcp">TCP sockets (no EFA)</option>
            </select>
          </label>
        </div>

        {/* Derived topology summary */}
        <div className="border border-line bg-surface-1 px-3 py-2.5 font-mono text-[11.5px] text-ink-1">
          <div className="text-ink-2 tracking-mech uppercase text-[10.5px] mb-1">Topology (derived)</div>
          {gpusPerNode > 0 ? (
            <span className="text-ink-0">
              {form.node_count} nodes × {gpusPerNode} GPU = {totalGPUs} GPUs · TP={tp} (within node) · PP={pp} (across nodes)
            </span>
          ) : (
            <span className="text-ink-2">Select an instance type to compute the topology.</span>
          )}
        </div>

        {/* Scenario + load knobs */}
        <div className="grid grid-cols-2 gap-4">
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Scenario</span>
            <select
              className="input w-full"
              value={form.scenario_id}
              onChange={(e) => set("scenario_id", e.target.value)}
            >
              {scenarios.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Concurrency</span>
            <input
              type="number"
              min={1}
              className="input w-full"
              value={form.concurrency}
              onChange={(e) => set("concurrency", Math.max(1, Number(e.target.value) || 1))}
            />
          </label>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Input seq length</span>
            <input
              type="number"
              min={1}
              className="input w-full"
              value={form.input_sequence_length}
              onChange={(e) => set("input_sequence_length", Math.max(1, Number(e.target.value) || 1))}
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Output seq length</span>
            <input
              type="number"
              min={1}
              className="input w-full"
              value={form.output_sequence_length}
              onChange={(e) => set("output_sequence_length", Math.max(1, Number(e.target.value) || 1))}
            />
          </label>
        </div>

        {/* HF token (optional) */}
        <label className="flex flex-col gap-1.5">
          <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">
            HuggingFace token (optional)
          </span>
          <input
            type="password"
            className="input w-full"
            placeholder="hf_… (leave blank to use the platform token)"
            value={form.hf_token}
            onChange={(e) => set("hf_token", e.target.value)}
          />
        </label>

        <div className="flex items-center gap-3 pt-1">
          <button
            className="btn-primary font-mono text-[12px] tracking-mech px-4 py-2"
            disabled={submitting}
            onClick={submit}
          >
            {submitting ? "Launching…" : "Launch distributed run"}
          </button>
          <span className="font-mono text-[11px] text-ink-2">
            Framework fixed to llm-d · topology user-specified
          </span>
        </div>
      </div>
    </div>
  );
}
