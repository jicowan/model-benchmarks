import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { createRun, getRecommendation } from "../api";
import type { RecommendResponse } from "../types";
import { validateToken } from "../hfApi";
import type { HfModelDetail } from "../hfApi";
import ModelCombobox from "../components/ModelCombobox";

type TokenStatus = "idle" | "validating" | "valid" | "invalid";

export default function Run() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [tokenStatus, setTokenStatus] = useState<TokenStatus>("idle");
  const [suggesting, setSuggesting] = useState(false);
  const [recommendation, setRecommendation] =
    useState<RecommendResponse | null>(null);
  const [suggestError, setSuggestError] = useState("");

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
    max_model_len: 0,
    hf_token: "",
  });

  function set(key: string, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  const handleTokenBlur = useCallback(async () => {
    const token = form.hf_token.trim();
    if (!token) {
      setTokenStatus("idle");
      return;
    }
    setTokenStatus("validating");
    try {
      const ok = await validateToken(token);
      setTokenStatus(ok ? "valid" : "invalid");
    } catch {
      setTokenStatus("invalid");
    }
  }, [form.hf_token]);

  // Reset token status when token changes.
  useEffect(() => {
    if (!form.hf_token.trim()) {
      setTokenStatus("idle");
    }
  }, [form.hf_token]);

  function handleModelSelect(detail: HfModelDetail) {
    set("model_hf_revision", detail.sha);
    setRecommendation(null);
  }

  const isNeuronInstance = /^(inf|trn)/.test(form.instance_type_name);
  const canSuggest =
    form.model_hf_id.trim() !== "" && form.instance_type_name !== "";

  async function handleSuggest() {
    setSuggestError("");
    setSuggesting(true);
    setRecommendation(null);
    try {
      const rec = await getRecommendation(
        form.model_hf_id,
        form.instance_type_name,
        form.hf_token || undefined
      );
      setRecommendation(rec);

      if (rec.explanation.feasible) {
        setForm((prev) => ({
          ...prev,
          tensor_parallel_degree: rec.tensor_parallel_degree,
          quantization: rec.quantization ?? "",
          max_model_len: rec.max_model_len,
          concurrency: rec.concurrency,
          input_sequence_length: rec.input_sequence_length,
          output_sequence_length: rec.output_sequence_length,
        }));
      }
    } catch (err) {
      setSuggestError(
        err instanceof Error ? err.message : "Failed to get recommendation"
      );
    } finally {
      setSuggesting(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      const res = await createRun({
        ...form,
        quantization: form.quantization || undefined,
        max_model_len: form.max_model_len || undefined,
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
        {/* HF Token — above model so search can use it */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            HF Token (optional, for gated models)
          </label>
          <div className="flex items-center gap-2">
            <input
              type="password"
              value={form.hf_token}
              onChange={(e) => set("hf_token", e.target.value)}
              onBlur={handleTokenBlur}
              placeholder="hf_..."
              className="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
            {tokenStatus === "validating" && (
              <svg
                className="animate-spin h-4 w-4 text-gray-400"
                viewBox="0 0 24 24"
              >
                <circle
                  className="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  strokeWidth="4"
                  fill="none"
                />
                <path
                  className="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                />
              </svg>
            )}
            {tokenStatus === "valid" && (
              <span className="text-green-600 text-sm font-medium flex items-center gap-1">
                <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                </svg>
                Valid
              </span>
            )}
            {tokenStatus === "invalid" && (
              <span className="text-red-600 text-sm font-medium flex items-center gap-1">
                <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
                Invalid
              </span>
            )}
          </div>
        </div>

        {/* Model */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Model (HuggingFace ID)
          </label>
          <ModelCombobox
            value={form.model_hf_id}
            onChange={(v) => set("model_hf_id", v)}
            onModelSelect={handleModelSelect}
            hfToken={form.hf_token}
          />
        </div>

        {/* Revision */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Revision
          </label>
          <input
            type="text"
            value={form.model_hf_revision}
            onChange={(e) => set("model_hf_revision", e.target.value)}
            className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm font-mono"
          />
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
                setRecommendation(null);
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

        {/* Suggest Config */}
        <div>
          <button
            type="button"
            disabled={!canSuggest || suggesting}
            onClick={handleSuggest}
            className="rounded-md border border-blue-300 bg-blue-50 px-4 py-2 text-sm font-medium text-blue-700 hover:bg-blue-100 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {suggesting ? (
              <span className="flex items-center gap-2">
                <svg
                  className="animate-spin h-4 w-4"
                  viewBox="0 0 24 24"
                  fill="none"
                >
                  <circle
                    className="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    strokeWidth="4"
                  />
                  <path
                    className="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                  />
                </svg>
                Analyzing...
              </span>
            ) : (
              "Suggest Config"
            )}
          </button>
          {isNeuronInstance && canSuggest && (
            <p className="mt-1 text-xs text-amber-600">
              Configuration suggestions are not yet available for Neuron
              instances.
            </p>
          )}
          {suggestError && (
            <p className="mt-1 text-sm text-red-600">{suggestError}</p>
          )}
        </div>

        {/* Explanation panel */}
        {recommendation && recommendation.explanation.feasible && (
          <div className="rounded-md border border-green-200 bg-green-50 p-4 text-sm">
            <p className="font-medium text-green-800 mb-2">
              Recommended Configuration
            </p>
            <p className="text-green-700 mb-2">
              {recommendation.model_info.architecture.toUpperCase()}{" "}
              {(
                recommendation.model_info.parameter_count / 1e9
              ).toFixed(1)}
              B ({recommendation.model_info.native_dtype}) on{" "}
              {recommendation.instance_info.accelerator_count}x{" "}
              {recommendation.instance_info.accelerator_name} (
              {recommendation.instance_info.accelerator_memory_gib} GiB)
            </p>
            <ul className="space-y-1 text-green-700">
              <li>
                Tensor Parallel = {recommendation.tensor_parallel_degree} —{" "}
                {recommendation.explanation.tensor_parallel_degree}
              </li>
              <li>
                Quantization ={" "}
                {recommendation.quantization ?? "None"} —{" "}
                {recommendation.explanation.quantization}
              </li>
              <li>
                Max Model Len = {recommendation.max_model_len} —{" "}
                {recommendation.explanation.max_model_len}
              </li>
              <li>
                Concurrency = {recommendation.concurrency} —{" "}
                {recommendation.explanation.concurrency}
              </li>
            </ul>
          </div>
        )}

        {/* Infeasibility warning with alternatives */}
        {recommendation && !recommendation.explanation.feasible && (
          <div className="rounded-md border border-amber-200 bg-amber-50 p-4 text-sm">
            <p className="font-medium text-amber-800 mb-2">
              Model does not fit on this instance
            </p>
            <p className="text-amber-700 mb-3">
              {recommendation.explanation.reason}
            </p>
            {recommendation.alternatives?.quantization_option && (
              <div className="mb-2">
                <p className="text-amber-800 font-medium">
                  Option A: Use{" "}
                  {recommendation.alternatives.quantization_option.quantization.toUpperCase()}{" "}
                  quantization on {form.instance_type_name}
                </p>
                <p className="text-amber-700">
                  Estimated memory:{" "}
                  {recommendation.alternatives.quantization_option.estimated_mem_gib.toFixed(
                    1
                  )}{" "}
                  GiB
                </p>
              </div>
            )}
            {recommendation.alternatives?.larger_instance && (
              <div>
                <p className="text-amber-800 font-medium">
                  Option{" "}
                  {recommendation.alternatives?.quantization_option
                    ? "B"
                    : "A"}
                  : Switch to {recommendation.alternatives.larger_instance}
                </p>
                <p className="text-amber-700">
                  Full {recommendation.model_info.native_dtype} precision, no
                  quality trade-off
                </p>
              </div>
            )}
          </div>
        )}

        {/* Config */}
        <div className="grid grid-cols-4 gap-4">
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
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Max Model Len
            </label>
            <input
              type="number"
              min={0}
              value={form.max_model_len}
              onChange={(e) => set("max_model_len", Number(e.target.value))}
              placeholder="0 = auto"
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            />
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
