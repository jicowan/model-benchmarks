import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { createRun, createSuiteRun, getRecommendation, listInstanceTypes, listScenarios, listTestSuites, getMemoryBreakdown, getOOMHistory, listModelCache, getToolVersions } from "../api";
import type { InstanceType, RecommendResponse, MemoryBreakdownResponse, OOMHistory, Scenario, TestSuite, ModelCache } from "../types";
import { datasetOptions } from "../constants/datasets";
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
  const maxModelLenDebounceRef = useRef<number | null>(null);
  const autoRecommendRef = useRef<number | null>(null);
  const memoryBreakdownRef = useRef<number | null>(null);
  // PRD-15: Memory breakdown and OOM state
  const [memoryBreakdown, setMemoryBreakdown] = useState<MemoryBreakdownResponse | null>(null);
  const [memoryBreakdownLoading, setMemoryBreakdownLoading] = useState(false);
  const [oomHistory, setOOMHistory] = useState<OOMHistory | null>(null);

  const [cachedModel, setCachedModel] = useState<ModelCache | null>(null);
  const [useS3Cache, setUseS3Cache] = useState(true);

  // PRD-12/13: Scenarios and test suites
  const [runMode, setRunMode] = useState<RunMode>("single");
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [testSuites, setTestSuites] = useState<TestSuite[]>([]);
  const [selectedScenario, setSelectedScenario] = useState<string>("chatbot");
  const [selectedSuite, setSelectedSuite] = useState<string>("quick");
  const [selectedDataset, setSelectedDataset] = useState<string>("synthetic");

  // Initialize form with URL params (from Estimate page) or defaults.
  // framework_version starts blank and is filled from getToolVersions() on mount.
  const [form, setForm] = useState(() => {
    const instance = searchParams.get("instance") || "";
    const isNeuron = /^(inf|trn)/.test(instance);
    return {
      model_hf_id: searchParams.get("model") || "",
      model_hf_revision: "main",
      instance_type_name: instance,
      framework: isNeuron ? "vllm-neuron" : "vllm",
      framework_version: "",
      tensor_parallel_degree: Number(searchParams.get("tp")) || 1,
      quantization: searchParams.get("quantization") || "",
      concurrency: Number(searchParams.get("concurrency")) || 16,
      input_sequence_length: Number(searchParams.get("input_seq")) || 512,
      output_sequence_length: Number(searchParams.get("output_seq")) || 256,
      max_model_len: Number(searchParams.get("max_model_len")) || 0,
      min_duration_seconds: 180,
      hf_token: searchParams.get("hf_token") || "",
      overhead_gib: 0, // 0 = auto-calculated
      api_type: "",
      model_s3_uri: searchParams.get("model_s3_uri") || "",
    };
  });

  function set(key: string, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  // Load instance types, scenarios, test suites, and the platform vLLM
  // version (PRD-34) on mount. Tool versions seed the form default; users
  // can still override per-run.
  useEffect(() => {
    listInstanceTypes().then(setInstanceTypes).catch(() => {});
    listScenarios().then(setScenarios).catch(() => {});
    listTestSuites().then(setTestSuites).catch(() => {});
    getToolVersions()
      .then((tv) => {
        setForm((prev) => (prev.framework_version ? prev : { ...prev, framework_version: tv.framework_version }));
      })
      .catch(() => {
        // Fall back to a sensible placeholder if the config endpoint fails.
        setForm((prev) => (prev.framework_version ? prev : { ...prev, framework_version: "v0.19.0" }));
      });
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

    const modelId = detail.modelId;
    listModelCache()
      .then((resp) => {
        const match = resp.rows.find(
          (c) => c.hf_id === modelId && c.status === "cached"
        );
        setCachedModel(match || null);
        if (match) {
          set("model_s3_uri", match.s3_uri);
          setUseS3Cache(true);
        } else {
          set("model_s3_uri", "");
        }
      })
      .catch(() => setCachedModel(null));
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

  // Recalculate concurrency when max_model_len is changed by user (debounced)
  function handleMaxModelLenChange(newMaxModelLen: number) {
    set("max_model_len", newMaxModelLen);

    if (!recommendation || !recommendation.explanation.feasible) {
      return;
    }

    if (maxModelLenDebounceRef.current) {
      clearTimeout(maxModelLenDebounceRef.current);
    }

    maxModelLenDebounceRef.current = window.setTimeout(async () => {
      try {
        const rec = await getRecommendation(
          form.model_hf_id,
          form.instance_type_name,
          form.hf_token || undefined,
          form.tensor_parallel_degree,
          form.overhead_gib || undefined,
          newMaxModelLen > 0 ? newMaxModelLen : undefined
        );
        setRecommendation(rec);
        setForm((prev) => ({
          ...prev,
          concurrency: rec.explanation.feasible ? rec.concurrency : prev.concurrency,
        }));
      } catch (err) {
        console.error("Max model len recalculation failed:", err);
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
          model_s3_uri: form.model_s3_uri || undefined,
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
    <>
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">new benchmark</span>
        </div>
      </div>

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
        <div className="mb-8">
          <div className="eyebrow mb-3">CONFIGURE RUN</div>
          <h1 className="font-sans text-[28px] leading-tight tracking-[-0.01em] text-balance">
            Benchmark a model on a target instance.
          </h1>
          <p className="meta mt-3">
            Select a model (HuggingFace or cached/registered S3), pick an instance type,
            and launch. Recommendations auto-populate based on model size and available memory.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-5">
        {/* HF Token — above model so search can use it */}
        <div>
          <label className="eyebrow block mb-1.5">
            HF Token (optional, overrides platform default)
          </label>
          <div className="flex items-center gap-2">
            <input
              type="password"
              value={form.hf_token}
              onChange={(e) => set("hf_token", e.target.value)}
              onBlur={handleTokenBlur}
              placeholder="Uses platform token — leave blank for default"
              className="input flex-1"
            />
            {tokenStatus === "validating" && (
              <svg
                className="animate-spin h-4 w-4 text-ink-2"
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
              <span className="text-signal font-mono text-[11.5px] tracking-mech uppercase flex items-center gap-1">
                <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                </svg>
                Valid
              </span>
            )}
            {tokenStatus === "invalid" && (
              <span className="text-danger font-mono text-[11.5px] tracking-mech uppercase flex items-center gap-1">
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
          <label className="eyebrow block mb-1.5">
            Model (HuggingFace ID)
          </label>
          <ModelCombobox
            value={form.model_hf_id}
            onChange={(v) => {
              set("model_hf_id", v);
              setCachedModel(null);
            }}
            onModelSelect={handleModelSelect}
            onCachedModelSelect={(cached) => {
              setCachedModel(cached);
              set("model_s3_uri", cached.s3_uri);
              setUseS3Cache(true);
            }}
            hfToken={form.hf_token}
          />
          {cachedModel && (
            <div className="mt-2 flex items-center gap-2">
              <span className="inline-flex items-center gap-1.5 font-mono text-[10.5px] tracking-widemech uppercase px-2 py-1 border border-signal/50 text-signal bg-signal/5">
                <span className="w-1.5 h-1.5 bg-signal" />
                S3 CACHED
              </span>
              <label className="flex items-center gap-1.5 font-mono text-[11.5px] tracking-mech text-ink-1">
                <input
                  type="checkbox"
                  checked={useS3Cache}
                  onChange={(e) => {
                    setUseS3Cache(e.target.checked);
                    set("model_s3_uri", e.target.checked ? cachedModel.s3_uri : "");
                  }}
                  className="accent-signal"
                />
                LOAD FROM S3 (FASTER)
              </label>
            </div>
          )}
        </div>

        {/* Revision */}
        <div>
          <label className="eyebrow block mb-1.5">
            Revision
          </label>
          <input
            type="text"
            value={form.model_hf_revision}
            onChange={(e) => set("model_hf_revision", e.target.value)}
            className="input w-full"
          />
        </div>

        {/* S3 Model URI (read-only, populated from cached/registered models) */}
        {form.model_s3_uri && (
          <div>
            <label className="eyebrow block mb-1.5">
              S3 Model URI
            </label>
            <input
              type="text"
              value={form.model_s3_uri}
              readOnly
              className="input w-full bg-surface-2 text-ink-1 cursor-not-allowed"
            />
            <p className="mt-1.5 caption text-signal">
              Model will be loaded from S3 using Run:ai Streamer instead of HuggingFace.
            </p>
          </div>
        )}

        {/* Instance */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="eyebrow block mb-1.5">
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
              className="input w-full"
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
            <label className="eyebrow block mb-1.5">
              vLLM Version
            </label>
            <input
              type="text"
              value={form.framework_version}
              onChange={(e) => set("framework_version", e.target.value)}
              placeholder="v0.19.0"
              className="input w-full"
            />
            <p className="mt-1 caption">
              Default from Configuration → Tool Versions. Override to test a specific release.
            </p>
          </div>
        </div>

        {/* PRD-12/13: Run Mode and Scenario/Suite Selection */}
        {scenarios.length > 0 && testSuites.length > 0 && (
          <div className="panel-inset p-4">
            <label className="eyebrow block mb-2">
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
                  className="h-4 w-4 accent-signal"
                />
                <span className="font-mono text-[12.5px] tracking-mech uppercase">Single Scenario</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="runMode"
                  value="suite"
                  checked={runMode === "suite"}
                  onChange={() => setRunMode("suite")}
                  className="h-4 w-4 accent-signal"
                />
                <span className="font-mono text-[12.5px] tracking-mech uppercase">Test Suite</span>
              </label>
            </div>

            {runMode === "single" ? (
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="eyebrow block mb-1.5">
                    Scenario
                  </label>
                  <select
                    value={selectedScenario}
                    onChange={(e) => setSelectedScenario(e.target.value)}
                    className="input w-full"
                  >
                    {scenarios.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} ({Math.round(s.duration_seconds / 60)}m)
                      </option>
                    ))}
                  </select>
                  {scenarios.find((s) => s.id === selectedScenario) && (
                    <p className="mt-2 caption">
                      {scenarios.find((s) => s.id === selectedScenario)?.description}
                    </p>
                  )}
                </div>
                <div>
                  <label className="eyebrow block mb-1.5">
                    Dataset
                  </label>
                  <select
                    value={selectedDataset}
                    onChange={(e) => setSelectedDataset(e.target.value)}
                    className="input w-full"
                  >
                    {datasetOptions.map((d) => (
                      <option key={d.value} value={d.value}>
                        {d.label}
                      </option>
                    ))}
                  </select>
                  {datasetOptions.find((d) => d.value === selectedDataset) && (
                    <p className="mt-2 caption">
                      {datasetOptions.find((d) => d.value === selectedDataset)?.description}
                    </p>
                  )}
                </div>
              </div>
            ) : (
              <div>
                <label className="eyebrow block mb-1.5">
                  Test Suite
                </label>
                <select
                  value={selectedSuite}
                  onChange={(e) => setSelectedSuite(e.target.value)}
                  className="input w-full"
                >
                  {testSuites.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name} ({Math.round(s.total_duration_seconds / 60)}m total)
                    </option>
                  ))}
                </select>
                {testSuites.find((s) => s.id === selectedSuite) && (
                  <div className="mt-2">
                    <p className="caption mb-1">
                      {testSuites.find((s) => s.id === selectedSuite)?.description}
                    </p>
                    <p className="caption text-ink-2">
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
          <div className="flex items-center gap-2 meta">
            <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
            </svg>
            Analyzing model configuration...
          </div>
        )}
        {suggestError && (
          <p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">{suggestError}</p>
        )}

        {/* PRD-15: OOM Warning */}
        {oomHistory && <OOMWarning history={oomHistory} />}

        {/* Recommendation Cards */}
        {recommendation?.explanation?.feasible && (
          <RecommendationCards recommendation={recommendation} />
        )}

        {/* Production note when max_model_len was reduced for benchmarking */}
        {recommendation?.explanation?.feasible && recommendation.explanation.production_note && (
          <div className="border border-info/40 bg-info/5 p-4">
            <p className="eyebrow mb-1.5 text-info">PRODUCTION DEPLOYMENT NOTE</p>
            <p className="font-mono text-[12.5px] text-ink-0">{recommendation.explanation.production_note}</p>
          </div>
        )}

        {/* Infeasibility warning with alternatives */}
        {recommendation?.explanation && !recommendation.explanation.feasible && (
          <div className="border border-warn/40 bg-warn/5 p-4">
            <div className="flex items-center gap-2 mb-3">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square" className="text-warn shrink-0">
                <path d="M12 9v4M12 17h.01M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              </svg>
              <span className="eyebrow text-warn">[ MODEL DOES NOT FIT ON THIS INSTANCE ]</span>
            </div>
            <p className="font-mono text-[12.5px] text-ink-0 mb-4">
              {recommendation.explanation.reason}
            </p>
            {(() => {
              const hasQuant = !!recommendation.alternatives?.quantization_option;
              const hasLarger = !!recommendation.alternatives?.larger_instance;
              if (!hasQuant && !hasLarger) return null;
              const gridCols = hasQuant && hasLarger ? "md:grid-cols-2" : "grid-cols-1";
              return (
                <div className="mt-4 pt-3 border-t border-warn/30">
                  <div className="eyebrow text-warn mb-2">ALTERNATIVES</div>
                  <div className={`grid grid-cols-1 ${gridCols} gap-3`}>
                    {hasQuant && recommendation.alternatives?.quantization_option && (
                      <div className="p-3 border border-warn/30 bg-warn/5">
                        <div className="caption text-warn mb-1">OPTION A</div>
                        <div className="font-mono text-[12.5px] text-ink-0 mb-1">
                          Use{" "}
                          <span className="text-warn">
                            {recommendation.alternatives.quantization_option.quantization.toUpperCase()}
                          </span>{" "}
                          quantization on {form.instance_type_name}
                        </div>
                        <div className="caption">
                          est. memory{" "}
                          <span className="tabular text-ink-0">
                            {recommendation.alternatives.quantization_option.estimated_mem_gib.toFixed(1)} GiB
                          </span>
                        </div>
                      </div>
                    )}
                    {hasLarger && recommendation.alternatives?.larger_instance && (
                      <div className="p-3 border border-warn/30 bg-warn/5">
                        <div className="caption text-warn mb-1">
                          OPTION {hasQuant ? "B" : "A"}
                        </div>
                        <div className="font-mono text-[12.5px] text-ink-0 mb-1">
                          Switch to{" "}
                          <span className="text-warn">
                            {recommendation.alternatives.larger_instance}
                          </span>
                        </div>
                        <div className="caption">
                          full {recommendation.model_info.native_dtype} precision · no quality trade-off
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              );
            })()}
          </div>
        )}

        {/* PRD-15: Memory Breakdown (always visible when recommendation exists) */}
        {recommendation?.explanation?.feasible && (
          <MemoryBreakdown breakdown={memoryBreakdown} loading={memoryBreakdownLoading} />
        )}

        {/* Config */}
        <div className="grid grid-cols-4 gap-4">
          <div>
            <label className="eyebrow block mb-1.5">
              Tensor Parallel
            </label>
            {validTPOptions.length > 0 ? (
              <select
                value={form.tensor_parallel_degree}
                onChange={(e) => handleTPChange(Number(e.target.value))}
                className="input w-full"
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
                className="input w-full"
              />
            )}
          </div>
          <div>
            <label className="eyebrow block mb-1.5">
              Concurrency
            </label>
            <input
              type="number"
              min={1}
              max={256}
              value={form.concurrency}
              onChange={(e) => set("concurrency", Number(e.target.value))}
              className="input w-full"
            />
          </div>
          <div>
            <label className="eyebrow block mb-1.5">
              Quantization
            </label>
            <select
              value={form.quantization}
              onChange={(e) => set("quantization", e.target.value)}
              className="input w-full"
            >
              <option value="">None (default)</option>
              <option value="fp16">FP16</option>
              <option value="int8">INT8</option>
              <option value="int4">INT4</option>
            </select>
          </div>
          <div>
            <label className="eyebrow block mb-1.5">
              Max Model Len
            </label>
            <input
              type="number"
              min={0}
              value={form.max_model_len}
              onChange={(e) => handleMaxModelLenChange(Number(e.target.value))}
              placeholder="0 = auto"
              className="input w-full"
            />
          </div>
        </div>

        {/* Runtime Overhead Slider */}
        <div className="hairline pt-4">
          <label className="eyebrow block mb-2">
            Runtime Overhead: {form.overhead_gib || 0} GiB
          </label>
          <input
            type="range"
            min={0}
            max={10}
            step={0.5}
            value={form.overhead_gib || 0}
            onChange={(e) => handleOverheadChange(Number(e.target.value))}
            className="w-full h-1 bg-surface-2 appearance-none cursor-pointer accent-signal"
          />
          <div className="flex justify-between caption text-ink-2 mt-1">
            <span>0</span>
            <span>5</span>
            <span>10</span>
          </div>
          <p className="caption mt-2">
            Increase if model fails with OOM.
          </p>
        </div>

        <div>
          <label className="eyebrow block mb-1.5">
            Min Duration (s)
          </label>
          <input
            type="number"
            min={0}
            value={form.min_duration_seconds}
            onChange={(e) => set("min_duration_seconds", Number(e.target.value))}
            placeholder="0 = no minimum"
            className="input w-48"
          />
          <p className="mt-1 caption">
            Minimum benchmark duration to ensure enough GPU samples. 0 disables.
          </p>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="eyebrow block mb-1.5">
              Input Seq Length
            </label>
            <input
              type="number"
              min={1}
              value={form.input_sequence_length}
              onChange={(e) =>
                set("input_sequence_length", Number(e.target.value))
              }
              className="input w-full"
            />
          </div>
          <div>
            <label className="eyebrow block mb-1.5">
              Output Seq Length
            </label>
            <input
              type="number"
              min={1}
              value={form.output_sequence_length}
              onChange={(e) =>
                set("output_sequence_length", Number(e.target.value))
              }
              className="input w-full"
            />
          </div>
        </div>

        {error && (
          <p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={submitting}
          className="btn btn-primary h-10 px-6 text-[13px]"
        >
          {submitting
            ? "Submitting..."
            : runMode === "suite"
            ? "Start Test Suite"
            : "Start Benchmark"}
        </button>
      </form>
      </div>
    </>
  );
}
