import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { createRun } from "../api";

export default function Run() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const [form, setForm] = useState({
    model_hf_id: "",
    model_hf_revision: "main",
    instance_type_name: "",
    framework: "vllm",
    framework_version: "v0.6.6",
    tensor_parallel_degree: 1,
    quantization: "",
    concurrency: 16,
    input_sequence_length: 512,
    output_sequence_length: 256,
    dataset_name: "sharegpt",
    hf_token: "",
  });

  function set(key: string, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      const res = await createRun({
        ...form,
        quantization: form.quantization || undefined,
        hf_token: form.hf_token || undefined,
        run_type: "on_demand",
      });
      navigate(`/results/${res.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Submission failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-2xl">
      <h1 className="text-2xl font-bold mb-6">Run On-Demand Benchmark</h1>

      <form onSubmit={handleSubmit} className="space-y-5">
        {/* Model */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Model (HuggingFace ID)
          </label>
          <input
            type="text"
            required
            value={form.model_hf_id}
            onChange={(e) => set("model_hf_id", e.target.value)}
            placeholder="meta-llama/Llama-3.1-70B-Instruct"
            className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Revision
            </label>
            <input
              type="text"
              value={form.model_hf_revision}
              onChange={(e) => set("model_hf_revision", e.target.value)}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              HF Token (for gated models)
            </label>
            <input
              type="password"
              value={form.hf_token}
              onChange={(e) => set("hf_token", e.target.value)}
              placeholder="hf_..."
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
        </div>

        {/* Instance */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Instance Type
            </label>
            <select
              required
              value={form.instance_type_name}
              onChange={(e) => {
                set("instance_type_name", e.target.value);
                const isNeuron = /^(inf|trn)/.test(e.target.value);
                set("framework", isNeuron ? "vllm-neuron" : "vllm");
              }}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            >
              <option value="">Select...</option>
              <optgroup label="GPU">
                <option>g5.xlarge</option>
                <option>g5.2xlarge</option>
                <option>g5.48xlarge</option>
                <option>g6.xlarge</option>
                <option>g6e.xlarge</option>
                <option>g6e.48xlarge</option>
                <option>p4d.24xlarge</option>
                <option>p5.48xlarge</option>
                <option>p5e.48xlarge</option>
              </optgroup>
              <optgroup label="Neuron">
                <option>inf2.xlarge</option>
                <option>inf2.8xlarge</option>
                <option>inf2.24xlarge</option>
                <option>inf2.48xlarge</option>
                <option>trn1.2xlarge</option>
                <option>trn1.32xlarge</option>
                <option>trn2.48xlarge</option>
              </optgroup>
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Framework
            </label>
            <input
              type="text"
              value={form.framework}
              readOnly
              className="w-full rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-sm"
            />
          </div>
        </div>

        {/* Config */}
        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Tensor Parallel
            </label>
            <input
              type="number"
              min={1}
              max={64}
              value={form.tensor_parallel_degree}
              onChange={(e) =>
                set("tensor_parallel_degree", Number(e.target.value))
              }
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Concurrency
            </label>
            <input
              type="number"
              min={1}
              max={256}
              value={form.concurrency}
              onChange={(e) => set("concurrency", Number(e.target.value))}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Quantization
            </label>
            <select
              value={form.quantization}
              onChange={(e) => set("quantization", e.target.value)}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            >
              <option value="">None (default)</option>
              <option value="fp16">FP16</option>
              <option value="int8">INT8</option>
              <option value="int4">INT4</option>
            </select>
          </div>
        </div>

        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Input Seq Length
            </label>
            <input
              type="number"
              min={1}
              value={form.input_sequence_length}
              onChange={(e) =>
                set("input_sequence_length", Number(e.target.value))
              }
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Output Seq Length
            </label>
            <input
              type="number"
              min={1}
              value={form.output_sequence_length}
              onChange={(e) =>
                set("output_sequence_length", Number(e.target.value))
              }
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Dataset
            </label>
            <input
              type="text"
              value={form.dataset_name}
              onChange={(e) => set("dataset_name", e.target.value)}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
        </div>

        {error && (
          <p className="text-sm text-red-600 bg-red-50 rounded-md px-3 py-2">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={submitting}
          className="rounded-md bg-blue-600 px-6 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {submitting ? "Submitting..." : "Start Benchmark"}
        </button>
      </form>
    </div>
  );
}
