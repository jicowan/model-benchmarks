import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { createRun, createSuiteRun, getRecommendation, listInstanceTypes, listScenarios, listTestSuites, getMemoryBreakdown, getOOMHistory } from "../api";
import type { InstanceType, RecommendResponse, MemoryBreakdownResponse, OOMHistory, Scenario, TestSuite } from "../types";
import { validateToken } from "../hfApi";
import type { HfModelDetail } from "../hfApi";
import ModelCombobox from "../components/ModelCombobox";
import MemoryBreakdown from "../components/MemoryBreakdown";
import OOMWarning from "../components/OOMWarning";
import RecommendationCards from "../components/RecommendationCards";

type RunMode = "single" | "suite";
type TokenStatus = "idle" | "validating" | "valid" | "invalid";

export default function Run() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [tokenStatus, setTokenStatus] = useState<TokenStatus>("idle");
  const [suggesting, setSuggesting] = useState(false);
  const [recommendation, setRecommendation] =
    useState<RecommendResponse | null>(null);
  const [suggestError, setSuggestError] = useState("");
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [validTPOptions, setValidTPOptions] = useState<number[]>([]);
  const overheadDebounceRef = useRef<number | null>(null);
  const autoRecommendRef = useRef<number | null>(null);
  const memoryBreakdownRef = useRef<number | null>(null);
  // PRD-15: Memory breakdown and OOM state
  const [memoryBreakdown, setMemoryBreakdown] = useState<MemoryBreakdownResponse | null>(null);
  const [memoryBreakdownLoading, setMemoryBreakdownLoading] = useState(false);
  const [oomHistory, setOOMHistory] = useState<OOMHistory | null>(null);


  // PRD-12/13: Scenarios and test suites
  const [runMode, setRunMode] = useState<RunMode>("single");
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [testSuites, setTestSuites] = useState<TestSuite[]>([]);
  const [selectedScenario, setSelectedScenario] = useState<string>("chatbot");
  const [selectedSuite, setSelectedSuite] = useState<string>("quick");
  const [selectedDataset, setSelectedDataset] = useState<string>("synthetic");

  // Supported inference-perf dataset types
  const datasetOptions = [
    { value: "synthetic", label: "Synthetic", description: "Controlled input/output distributions" },
    { value: "sharegpt", label: "ShareGPT", description: "Real-world conversational data" },
    { value: "random", label: "Random", description: "Random token data" },
    { value: "shared_prefix", label: "Shared Prefix", description: "Prefix caching scenarios" },
    { value: "cnn_dailymail", label: "CNN DailyMail", description: "Summarization use cases" },
    { value: "billsum_conversations", label: "Billsum", description: "Long context prefill-heavy" },
    { value: "infinity_instruct", label: "Infinity Instruct", description: "Long context decode-heavy" },
  ];

  // Initialize form with URL params (from Estimate page) or defaults
  const [form, setForm] = useState(() => {
    const instance = searchParams.get("instance") || "";
    const isNeuron = /^(inf|trn)/.test(instance);
    return {
      model_hf_id: searchParams.get("model") || "",
      model_hf_revision: "main",
      instance_type_name: instance,
      framework: isNeuron ? "vllm-neuron" : "vllm",
      framework_version: "v0.19.0",
      tensor_parallel_degree: Number(searchParams.get("tp")) || 1,
      quantization: searchParams.get("quantization") || "",
      concurrency: Number(searchParams.get("concurrency")) || 16,
      input_sequence_length: Number(searchParams.get("input_seq")) || 512,
      output_sequence_length: Number(searchParams.get("output_seq")) || 256,
      max_model_len: Number(searchParams.get("max_model_len")) || 0,
      min_duration_seconds: 180,
      hf_token: searchParams.get("hf_token") || "",
      overhead_gib: 0, // 0 = auto-calculated
    };
  });

  function set(key: string, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  // Load instance types, scenarios, and test suites on mount
  useEffect(() => {
    listInstanceTypes().then(setInstanceTypes).catch(() => {});
    listScenarios().then(setScenarios).catch(() => {});
    listTestSuites().then(setTestSuites).catch(() => {});
  }, []);

  // PRD-15: Auto-recommend when model and instance are both selected
  useEffect(() => {
    const model = form.model_hf_id.trim();
    const instance = form.instance_type_name;

    if (!model || !instance) {
      return;
    }

    // Clear previous debounce
    if (autoRecommendRef.current) {
      clearTimeout(autoRecommendRef.current);
    }

    // Debounce to avoid rapid API calls while user is still selecting
    autoRecommendRef.current = window.setTimeout(async () => {
      setSuggestError("");
      setSuggesting(true);
      setRecommendation(null);
      setValidTPOptions([]);
      setMemoryBreakdown(null);
      setOOMHistory(null);

      try {
        // Fetch recommendation and OOM history in parallel
        const [rec, oomHist] = await Promise.all([
          getRecommendation(model, instance, form.hf_token || undefined),
          getOOMHistory(model, instance).catch(() => null),
        ]);

        setRecommendation(rec);
        setValidTPOptions(rec.valid_tp_options ?? []);
        setOOMHistory(oomHist);

        if (rec.explanation.feasible) {
          setForm((prev) => ({
            ...prev,
            tensor_parallel_degree: rec.tensor_parallel_degree,
            quantization: rec.quantization ?? "",
            max_model_len: rec.max_model_len,
            concurrency: rec.concurrency,
            input_sequence_length: rec.input_sequence_length,
            output_sequence_length: rec.output_sequence_length,
            overhead_gib: rec.overhead_gib,
          }));
        }
      } catch (err) {
        setSuggestError(
          err instanceof Error ? err.message : "Failed to get recommendation"
        );
      } finally {
        setSuggesting(false);
      }
    }, 500);

    return () => {
      if (autoRecommendRef.current) {
        clearTimeout(autoRecommendRef.current);
      }
    };
  }, [form.model_hf_id, form.instance_type_name, form.hf_token]);

  // PRD-15: Update memory breakdown when parameters change
  useEffect(() => {
    if (!recommendation || !recommendation.explanation.feasible) {
      setMemoryBreakdown(null);
      return;
    }

    // Clear previous debounce
    if (memoryBreakdownRef.current) {
      clearTimeout(memoryBreakdownRef.current);
    }

    // Debounce memory breakdown updates
    memoryBreakdownRef.current = window.setTimeout(async () => {
      setMemoryBreakdownLoading(true);
      try {
        const breakdown = await getMemoryBreakdown({
          model: form.model_hf_id,
          instanceType: form.instance_type_name,
          tp: form.tensor_parallel_degree,
          quantization: form.quantization || undefined,
          maxModelLen: form.max_model_len || undefined,
          inputSeqLen: form.input_sequence_length,
          outputSeqLen: form.output_sequence_length,
          concurrency: form.concurrency,
          overheadGiB: form.overhead_gib || undefined,
          hfToken: form.hf_token || undefined,
        });
        setMemoryBreakdown(breakdown);
      } catch (err) {
        console.error("Memory breakdown failed:", err);
      } finally {
        setMemoryBreakdownLoading(false);
      }
    }, 300);

    return () => {
      if (memoryBreakdownRef.current) {
        clearTimeout(memoryBreakdownRef.current);
      }
    };
  }, [
    recommendation,
    form.model_hf_id,
    form.instance_type_name,
    form.tensor_parallel_degree,
    form.quantization,
    form.max_model_len,
    form.concurrency,
    form.overhead_gib,
    form.hf_token,
  ]);

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
    setMemoryBreakdown(null);
    setOOMHistory(null);
  }

  // Recalculate recommendation when TP is changed by user
  async function handleTPChange(newTP: number) {
    set("tensor_parallel_degree", newTP);
    if (!recommendation || !recommendation.explanation.feasible) return;

    try {
      const rec = await getRecommendation(
        form.model_hf_id,
        form.instance_type_name,
        form.hf_token || undefined,
        newTP,
        form.overhead_gib || undefined
      );
      setRecommendation(rec);
      if (rec.explanation.feasible) {
        setForm((prev) => ({
          ...prev,
          max_model_len: rec.max_model_len,
          concurrency: rec.concurrency,
        }));
      }
    } catch {
      // Silently fail - user can still proceed with manual values
    }
  }

  // Recalculate recommendation when overhead is changed by user (debounced)
  function handleOverheadChange(newOverhead: number) {
    // Always update slider immediately for responsive feel
    set("overhead_gib", newOverhead);

    // If no recommendation yet, no need to recalculate
    if (!recommendation || !recommendation.explanation.feasible) {
      return;
    }

    // Clear any pending debounce
    if (overheadDebounceRef.current) {
      clearTimeout(overheadDebounceRef.current);
    }

    // Debounce the API call
    overheadDebounceRef.current = window.setTimeout(async () => {
      try {
        const rec = await getRecommendation(
          form.model_hf_id,
          form.instance_type_name,
          form.hf_token || undefined,
          form.tensor_parallel_degree,
          newOverhead > 0 ? newOverhead : undefined
        );
        setRecommendation(rec);
        setForm((prev) => ({
          ...prev,
          max_model_len: rec.explanation.feasible ? rec.max_model_len : prev.max_model_len,
          concurrency: rec.explanation.feasible ? rec.concurrency : prev.concurrency,
        }));
      } catch (err) {
        console.error("Overhead recalculation failed:", err);
      }
    }, 300);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      if (runMode === "suite") {
        // Create test suite run
        const res = await createSuiteRun({
          model_hf_id: form.model_hf_id,
          model_hf_revision: form.model_hf_revision,
          instance_type_name: form.instance_type_name,
          suite_id: selectedSuite,
          framework: form.framework,
          framework_version: form.framework_version,
          tensor_parallel_degree: form.tensor_parallel_degree,
          quantization: form.quantization || undefined,
          max_model_len: form.max_model_len || undefined,
          hf_token: form.hf_token || undefined,
        });
        navigate(`/suite-runs/${res.id}`);
      } else {
        // Create single scenario run
        const scenario = scenarios.find((s) => s.id === selectedScenario);
        const res = await createRun({
          ...form,
          quantization: form.quantization || undefined,
          max_model_len: form.max_model_len || undefined,
          min_duration_seconds: scenario?.duration_seconds || form.min_duration_seconds || undefined,
          hf_token: form.hf_token || undefined,
          scenario_id: selectedScenario,
          dataset_name: selectedDataset,
          run_type: "on_demand",
        });
        navigate(`/results/${res.id}`);
      }
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
                setMemoryBreakdown(null);
                setOOMHistory(null);
              }}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            >
              <option value="">Select...</option>
              <optgroup label="GPU">
                {instanceTypes
                  .filter((t) => t.accelerator_type === "gpu")
                  .map((t) => (
                    <option key={t.name} value={t.name}>
                      {t.name} ({t.accelerator_count}x {t.accelerator_name})
                    </option>
                  ))}
              </optgroup>
              <optgroup label="Neuron">
                {instanceTypes
                  .filter((t) => t.accelerator_type === "neuron")
                  .map((t) => (
                    <option key={t.name} value={t.name}>
                      {t.name} ({t.accelerator_count}x {t.accelerator_name})
                    </option>
                  ))}
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

        {/* PRD-12/13: Run Mode and Scenario/Suite Selection */}
        {scenarios.length > 0 && testSuites.length > 0 && (
          <div className="border rounded-lg p-4 bg-gray-50">
            <label className="block text-sm font-medium text-gray-700 mb-3">
              Benchmark Type
            </label>
            <div className="flex gap-4 mb-4">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="runMode"
                  value="single"
                  checked={runMode === "single"}
                  onChange={() => setRunMode("single")}
                  className="h-4 w-4 text-blue-600"
                />
                <span className="text-sm font-medium">Single Scenario</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="runMode"
                  value="suite"
                  checked={runMode === "suite"}
                  onChange={() => setRunMode("suite")}
                  className="h-4 w-4 text-blue-600"
                />
                <span className="text-sm font-medium">Test Suite</span>
              </label>
            </div>

            {runMode === "single" ? (
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Scenario
                  </label>
                  <select
                    value={selectedScenario}
                    onChange={(e) => setSelectedScenario(e.target.value)}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
                  >
                    {scenarios.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} ({Math.round(s.duration_seconds / 60)}m)
                      </option>
                    ))}
                  </select>
                  {scenarios.find((s) => s.id === selectedScenario) && (
                    <p className="mt-2 text-xs text-gray-500">
                      {scenarios.find((s) => s.id === selectedScenario)?.description}
                    </p>
                  )}
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Dataset
                  </label>
                  <select
                    value={selectedDataset}
                    onChange={(e) => setSelectedDataset(e.target.value)}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
                  >
                    {datasetOptions.map((d) => (
                      <option key={d.value} value={d.value}>
                        {d.label}
                      </option>
                    ))}
                  </select>
                  {datasetOptions.find((d) => d.value === selectedDataset) && (
                    <p className="mt-2 text-xs text-gray-500">
                      {datasetOptions.find((d) => d.value === selectedDataset)?.description}
                    </p>
                  )}
                </div>
              </div>
            ) : (
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Test Suite
                </label>
                <select
                  value={selectedSuite}
                  onChange={(e) => setSelectedSuite(e.target.value)}
                  className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
                >
                  {testSuites.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name} ({Math.round(s.total_duration_seconds / 60)}m total)
                    </option>
                  ))}
                </select>
                {testSuites.find((s) => s.id === selectedSuite) && (
                  <div className="mt-2">
                    <p className="text-xs text-gray-500 mb-1">
                      {testSuites.find((s) => s.id === selectedSuite)?.description}
                    </p>
                    <p className="text-xs text-gray-400">
                      Scenarios: {testSuites.find((s) => s.id === selectedSuite)?.scenarios.join(", ")}
                    </p>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {/* Auto-recommend status */}
        {suggesting && (
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
            </svg>
            Analyzing model configuration...
          </div>
        )}
        {suggestError && (
          <p className="text-sm text-red-600 bg-red-50 rounded-md px-3 py-2">{suggestError}</p>
        )}

        {/* PRD-15: OOM Warning */}
        {oomHistory && <OOMWarning history={oomHistory} />}

        {/* Recommendation Cards */}
        {recommendation?.explanation?.feasible && (
          <RecommendationCards recommendation={recommendation} />
        )}

        {/* Infeasibility warning with alternatives */}
        {recommendation?.explanation && !recommendation.explanation.feasible && (
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

        {/* PRD-15: Memory Breakdown (always visible when recommendation exists) */}
        {recommendation?.explanation?.feasible && (
          <MemoryBreakdown breakdown={memoryBreakdown} loading={memoryBreakdownLoading} />
        )}

        {/* Config */}
        <div className="grid grid-cols-4 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Tensor Parallel
            </label>
            {validTPOptions.length > 0 ? (
              <select
                value={form.tensor_parallel_degree}
                onChange={(e) => handleTPChange(Number(e.target.value))}
                className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
              >
                {validTPOptions.map((tp) => (
                  <option key={tp} value={tp}>
                    {tp}
                  </option>
                ))}
              </select>
            ) : (
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
            )}
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

        {/* Runtime Overhead Slider */}
        <div className="border-t border-gray-200 pt-4">
          <label className="block text-sm font-medium text-gray-700 mb-2">
            Runtime Overhead: {form.overhead_gib || 0} GiB
          </label>
          <input
            type="range"
            min={0}
            max={10}
            step={0.5}
            value={form.overhead_gib || 0}
            onChange={(e) => handleOverheadChange(Number(e.target.value))}
            className="w-full h-2 bg-gray-200 rounded-lg appearance-none cursor-pointer"
          />
          <div className="flex justify-between text-xs text-gray-400 mt-1">
            <span>0</span>
            <span>5</span>
            <span>10</span>
          </div>
          <p className="text-xs text-gray-500 mt-2">
            Increase if model fails with OOM. Qwen/Mistral often need 4-5 GiB.
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Min Duration (s)
          </label>
          <input
            type="number"
            min={0}
            value={form.min_duration_seconds}
            onChange={(e) => set("min_duration_seconds", Number(e.target.value))}
            placeholder="0 = no minimum"
            className="w-48 rounded-md border border-gray-300 px-3 py-2 text-sm"
          />
          <p className="mt-1 text-xs text-gray-500">
            Minimum benchmark duration to ensure enough GPU samples. 0 disables.
          </p>
        </div>

        <div className="grid grid-cols-2 gap-4">
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
          {submitting
            ? "Submitting..."
            : runMode === "suite"
            ? "Start Test Suite"
            : "Start Benchmark"}
        </button>
      </form>
    </div>
  );
}
