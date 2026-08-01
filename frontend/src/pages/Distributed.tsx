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

  // PRD-58: co-located (PRD-57) vs disaggregated (prefill/decode split).
  const [mode, setMode] = useState<"distributed" | "disaggregated">("distributed");

  const [form, setForm] = useState({
    model_hf_id: "",
    model_hf_revision: "main",
    instance_type_name: "",
    node_count: 2,
    // Tensor-parallel is an INDEPENDENT, user-specified knob (within-node),
    // NOT forced to fill the node. Default 1 = pipeline-parallel WITHOUT
    // tensor-parallel (a first-class topology). Editable up to GPUs/node.
    tensor_parallel_degree: 1,
    network_mode: "efa" as "efa" | "tcp",
    concurrency: 16,
    input_sequence_length: 512,
    output_sequence_length: 256,
    scenario_id: "chatbot",
    hf_token: "",
    // PRD-58 disaggregated per-role shape (the xPyD ratio + within-node TP).
    prefill_replicas: 1,
    prefill_tp: 1,
    decode_replicas: 1,
    decode_tp: 1,
    // PRD-64: shared vLLM runtime knobs (both modes). 0/"" = vLLM default.
    max_model_len: 0,
    max_num_batched_tokens: 0,
    kv_cache_dtype: "" as "" | "auto" | "fp8",
    quantization: "" as "" | "fp16" | "int8" | "int4",
    // PRD-64: optional per-role scheduler override (D/P only). 0 = inherit shared.
    prefill_max_num_batched_tokens: 0,
    decode_max_num_batched_tokens: 0,
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

  // TP is user-specified (within-node); PP spans nodes (== node count for a
  // single co-located group). Total serving GPUs = TP × node_count.
  const gpusPerNode = selectedInstance?.accelerator_count ?? 0;
  const tp = Math.min(form.tensor_parallel_degree, gpusPerNode || form.tensor_parallel_degree);
  const pp = form.node_count; // across-node pipeline parallel
  const totalGPUs = tp * form.node_count;

  // PRD-58 disaggregated: each role pod is one node (TP within-node, PP=1), so
  // the node total is prefill + decode replicas. GPUs = Σ replicas × role TP.
  const pTP = Math.min(form.prefill_tp, gpusPerNode || form.prefill_tp);
  const dTP = Math.min(form.decode_tp, gpusPerNode || form.decode_tp);
  const disaggNodes = form.prefill_replicas + form.decode_replicas;
  const disaggGPUs = form.prefill_replicas * pTP + form.decode_replicas * dTP;

  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  async function submit() {
    setError("");
    if (!form.model_hf_id) return setError("Select a model.");
    if (!form.instance_type_name) return setError("Select a GPU instance type.");
    if (gpusPerNode < 1) return setError("Selected instance has no GPUs.");

    const base: RunRequest = {
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
      network_mode: form.network_mode,
      // PRD-64: shared vLLM runtime knobs (both modes). Omit at 0/"" so vLLM
      // defaults apply — keeps a run that sets nothing byte-identical to before.
      max_model_len: form.max_model_len || undefined,
      max_num_batched_tokens: form.max_num_batched_tokens || undefined,
      kv_cache_dtype: form.kv_cache_dtype || undefined,
      quantization: form.quantization || undefined,
    };

    let req: RunRequest;
    if (mode === "disaggregated") {
      if (form.prefill_replicas < 1 || form.decode_replicas < 1)
        return setError("Prefill and decode each need at least 1 replica.");
      if (form.prefill_replicas + form.decode_replicas < 2)
        return setError("Disaggregated runs need at least 2 nodes (prefill + decode).");
      if (form.prefill_tp < 1 || form.prefill_tp > gpusPerNode)
        return setError(`Prefill TP must be between 1 and ${gpusPerNode} (GPUs per node).`);
      if (form.decode_tp < 1 || form.decode_tp > gpusPerNode)
        return setError(`Decode TP must be between 1 and ${gpusPerNode} (GPUs per node).`);
      req = {
        ...base,
        deployment_mode: "disaggregated",
        prefill_replicas: form.prefill_replicas,
        prefill_tp: pTP,
        decode_replicas: form.decode_replicas,
        decode_tp: dTP,
        // PRD-64: per-role scheduler override (0 ⇒ inherit shared).
        prefill_max_num_batched_tokens: form.prefill_max_num_batched_tokens || undefined,
        decode_max_num_batched_tokens: form.decode_max_num_batched_tokens || undefined,
      };
    } else {
      if (form.node_count < 2) return setError("Distributed runs need at least 2 nodes.");
      if (form.tensor_parallel_degree < 1 || form.tensor_parallel_degree > gpusPerNode)
        return setError(`Tensor-parallel must be between 1 and ${gpusPerNode} (GPUs per node).`);
      req = {
        ...base,
        deployment_mode: "distributed",
        node_count: form.node_count,
        pipeline_parallel_degree: pp,
      };
    }

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
          <span className="text-ink-2">— multi-node llm-d {mode === "disaggregated" ? "· prefill/decode" : "· co-located"}</span>
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

        {/* PRD-58: deployment-mode toggle — co-located vs disaggregated. */}
        <div className="flex flex-col gap-1.5">
          <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">Deployment mode</span>
          <div className="flex gap-2">
            {([
              ["distributed", "Co-located", "One serving group split across nodes (PP)"],
              ["disaggregated", "Disaggregated", "Separate prefill + decode groups (KV transfer)"],
            ] as const).map(([val, label, desc]) => (
              <button
                key={val}
                type="button"
                onClick={() => setMode(val)}
                className={`flex-1 border px-3 py-2 text-left font-mono text-[11.5px] transition-colors ${
                  mode === val
                    ? "border-accent bg-accent/10 text-ink-0"
                    : "border-line bg-surface-1 text-ink-2 hover:text-ink-1"
                }`}
              >
                <div className="tracking-mech uppercase text-[11px]">{label}</div>
                <div className="text-[10px] text-ink-2 mt-0.5">{desc}</div>
              </button>
            ))}
          </div>
        </div>

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

        {/* Network fabric (shared by both modes) */}
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

        {mode === "distributed" ? (
          <>
            {/* Co-located: node count + within-node TP (PP == node count). */}
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
              <span className="font-mono text-[11.5px] tracking-mech text-ink-1 uppercase">
                Tensor-parallel (GPUs per node used)
              </span>
              <input
                type="number"
                min={1}
                max={gpusPerNode || undefined}
                className="input w-full"
                value={form.tensor_parallel_degree}
                onChange={(e) => set("tensor_parallel_degree", Math.max(1, Number(e.target.value) || 1))}
              />
              <span className="font-mono text-[10.5px] text-ink-2">
                1 = pipeline-parallel only (no tensor sharding). Max {gpusPerNode || "?"} (GPUs per node).
              </span>
            </label>

            {/* Derived topology summary */}
            <div className="border border-line bg-surface-1 px-3 py-2.5 font-mono text-[11.5px] text-ink-1">
              <div className="text-ink-2 tracking-mech uppercase text-[10.5px] mb-1">Topology</div>
              {gpusPerNode > 0 ? (
                <span className="text-ink-0">
                  {form.node_count} nodes · TP={tp} (within node) · PP={pp} (across nodes) · {totalGPUs} GPUs serving
                </span>
              ) : (
                <span className="text-ink-2">Select an instance type to compute the topology.</span>
              )}
            </div>
          </>
        ) : (
          <>
            {/* Disaggregated: prefill + decode sub-sections (the xPyD ratio). */}
            <div className="grid grid-cols-2 gap-4">
              <div className="border border-line bg-surface-1 p-3 flex flex-col gap-3">
                <div className="font-mono text-[11px] tracking-mech uppercase text-ink-0">Prefill</div>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Replicas</span>
                  <input
                    type="number"
                    min={1}
                    className="input w-full"
                    value={form.prefill_replicas}
                    onChange={(e) => set("prefill_replicas", Math.max(1, Number(e.target.value) || 1))}
                  />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Tensor-parallel</span>
                  <input
                    type="number"
                    min={1}
                    max={gpusPerNode || undefined}
                    className="input w-full"
                    value={form.prefill_tp}
                    onChange={(e) => set("prefill_tp", Math.max(1, Number(e.target.value) || 1))}
                  />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Max batched tokens</span>
                  <input
                    type="number"
                    min={0}
                    placeholder="shared"
                    className="input w-full"
                    value={form.prefill_max_num_batched_tokens || ""}
                    onChange={(e) => set("prefill_max_num_batched_tokens", Math.max(0, Number(e.target.value) || 0))}
                  />
                  <span className="font-mono text-[9.5px] text-ink-2">blank = shared · larger favors prefill (compute-bound)</span>
                </label>
              </div>
              <div className="border border-line bg-surface-1 p-3 flex flex-col gap-3">
                <div className="font-mono text-[11px] tracking-mech uppercase text-ink-0">Decode</div>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Replicas</span>
                  <input
                    type="number"
                    min={1}
                    className="input w-full"
                    value={form.decode_replicas}
                    onChange={(e) => set("decode_replicas", Math.max(1, Number(e.target.value) || 1))}
                  />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Tensor-parallel</span>
                  <input
                    type="number"
                    min={1}
                    max={gpusPerNode || undefined}
                    className="input w-full"
                    value={form.decode_tp}
                    onChange={(e) => set("decode_tp", Math.max(1, Number(e.target.value) || 1))}
                  />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="font-mono text-[10.5px] text-ink-2 uppercase">Max batched tokens</span>
                  <input
                    type="number"
                    min={0}
                    placeholder="shared"
                    className="input w-full"
                    value={form.decode_max_num_batched_tokens || ""}
                    onChange={(e) => set("decode_max_num_batched_tokens", Math.max(0, Number(e.target.value) || 0))}
                  />
                  <span className="font-mono text-[9.5px] text-ink-2">blank = shared · decode is memory-bound</span>
                </label>
              </div>
            </div>{/* END prefill/decode role boxes */}

            {/* KV connector (fixed) + guidance */}
            <div className="font-mono text-[10.5px] text-ink-2">
              KV connector: NIXL over {form.network_mode === "tcp" ? "TCP" : "EFA/libfabric"} · routed via the
              InferencePool + Endpoint Picker (KV/role/load-aware). Tune the prefill:decode ratio to your
              input:output sequence-length ratio.
            </div>

            {/* Derived xPyD summary */}
            <div className="border border-line bg-surface-1 px-3 py-2.5 font-mono text-[11.5px] text-ink-1">
              <div className="text-ink-2 tracking-mech uppercase text-[10.5px] mb-1">Topology (xPyD)</div>
              {gpusPerNode > 0 ? (
                <span className="text-ink-0">
                  {form.prefill_replicas}P{form.decode_replicas}D · prefill {form.prefill_replicas}× TP={pTP} ·
                  decode {form.decode_replicas}× TP={dTP} · {disaggNodes} nodes · {disaggGPUs} GPUs serving
                </span>
              ) : (
                <span className="text-ink-2">Select an instance type to compute the topology.</span>
              )}
            </div>
          </>
        )}

        {/* PRD-64: shared vLLM model runtime parameters (both modes). These
            already flow through BuildArgs into the rendered pods; blank/default
            = vLLM default. In D/P they apply to BOTH roles (model-identity knobs
            must match across prefill+decode); the per-role Max-batched-tokens
            override above is the only knob that may differ by role. */}
        <div className="border border-line bg-surface-1 p-3 flex flex-col gap-3">
          <div className="font-mono text-[11px] tracking-mech uppercase text-ink-0">Model runtime parameters</div>
          <div className="grid grid-cols-2 gap-4">
            <label className="flex flex-col gap-1">
              <span className="font-mono text-[10.5px] text-ink-2 uppercase">Max model len</span>
              <input
                type="number"
                min={0}
                placeholder="auto"
                className="input w-full"
                value={form.max_model_len || ""}
                onChange={(e) => set("max_model_len", Math.max(0, Number(e.target.value) || 0))}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="font-mono text-[10.5px] text-ink-2 uppercase">
                Max batched tokens{mode === "disaggregated" ? " (shared)" : ""}
              </span>
              <input
                type="number"
                min={0}
                placeholder="vLLM default"
                className="input w-full"
                value={form.max_num_batched_tokens || ""}
                onChange={(e) => set("max_num_batched_tokens", Math.max(0, Number(e.target.value) || 0))}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="font-mono text-[10.5px] text-ink-2 uppercase">KV cache dtype</span>
              <select
                className="input w-full"
                value={form.kv_cache_dtype}
                onChange={(e) => set("kv_cache_dtype", e.target.value as "" | "auto" | "fp8")}
              >
                <option value="">auto</option>
                <option value="fp8">fp8</option>
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="font-mono text-[10.5px] text-ink-2 uppercase">Quantization</span>
              <select
                className="input w-full"
                value={form.quantization}
                onChange={(e) => set("quantization", e.target.value as "" | "fp16" | "int8" | "int4")}
              >
                <option value="">none</option>
                <option value="fp16">fp16</option>
                <option value="int8">int8</option>
                <option value="int4">int4</option>
              </select>
            </label>
          </div>
          <span className="font-mono text-[9.5px] text-ink-2">
            Manual knobs — the recommender/estimator is single-node-only for now (not offered for distributed runs).
            {mode === "disaggregated" ? " Model-identity knobs apply identically to prefill + decode." : ""}
          </span>
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
