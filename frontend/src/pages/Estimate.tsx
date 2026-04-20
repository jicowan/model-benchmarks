import { useState, useCallback, useEffect } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { getEstimate } from "../api";
import type { EstimateResponse, EstimateRow, EstimateFilter } from "../types";
import { validateToken } from "../hfApi";
import ModelCombobox from "../components/ModelCombobox";
import type { HfModelDetail } from "../hfApi";

type TokenStatus = "idle" | "validating" | "valid" | "invalid";

export default function Estimate() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [model, setModel] = useState(searchParams.get("model") || "");
  const [hfToken, setHfToken] = useState("");
  const [tokenStatus, setTokenStatus] = useState<TokenStatus>("idle");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<EstimateResponse | null>(null);
  const [expandedRow, setExpandedRow] = useState<string | null>(null);

  // Filters
  const [filters, setFilters] = useState<EstimateFilter>({
    accelerator_type: "",
  });

  const handleTokenBlur = useCallback(async () => {
    const token = hfToken.trim();
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
  }, [hfToken]);

  useEffect(() => {
    if (!hfToken.trim()) setTokenStatus("idle");
  }, [hfToken]);

  function handleModelSelect(detail: HfModelDetail) {
    setModel(detail.modelId);
    setResult(null);
  }

  async function handleEstimate() {
    if (!model.trim()) return;
    setError("");
    setLoading(true);
    setResult(null);
    try {
      const res = await getEstimate(model, filters, hfToken || undefined);
      setResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to get estimate");
    } finally {
      setLoading(false);
    }
  }

  function handleRunBenchmark(row: EstimateRow) {
    if (!row.config || !result) return;
    const params = new URLSearchParams({
      model: result.model_info.hf_id,
      instance: row.instance_type,
      tp: String(row.config.tensor_parallel_degree),
      concurrency: String(row.config.concurrency),
      max_model_len: String(row.config.max_model_len),
      input_seq: String(row.config.input_sequence_length),
      output_seq: String(row.config.output_sequence_length),
    });
    if (row.config.quantization) {
      params.set("quantization", row.config.quantization);
    }
    if (hfToken) {
      params.set("hf_token", hfToken);
    }
    navigate(`/run?${params}`);
  }

  function getFeasibilityBadge(row: EstimateRow) {
    if (!row.feasible) {
      return <span className="font-mono text-[11px] tracking-widemech uppercase text-danger">Infeasible</span>;
    }
    if (row.requires_quantization) {
      return <span className="font-mono text-[11px] tracking-widemech uppercase text-warn">Quantized</span>;
    }
    return <span className="font-mono text-[11px] tracking-widemech uppercase text-signal">Native</span>;
  }

  return (
    <>
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">estimate</span>
        </div>
      </div>
      <div className="p-6 max-w-6xl mx-auto animate-enter">
      <div className="mb-8">
        <div className="eyebrow mb-3">INSTANCE SIZING</div>
        <h1 className="font-sans text-[28px] leading-tight tracking-[-0.01em] text-balance">
          Which instances can run this model?
        </h1>
        <p className="meta mt-3 max-w-xl">
          Memory fit, recommended configuration, and on-demand cost — computed from
          model architecture and GPU specs. No benchmark required.
        </p>
      </div>

      {/* Input Section */}
      <div className="panel p-4 mb-6">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
          <div>
            <label className="eyebrow block mb-1.5">
              HF Token (optional, for gated models)
            </label>
            <div className="flex items-center gap-2">
              <input
                type="password"
                value={hfToken}
                onChange={(e) => setHfToken(e.target.value)}
                onBlur={handleTokenBlur}
                placeholder="hf_..."
                className="input flex-1"
              />
              {tokenStatus === "valid" && (
                <span className="font-mono text-[11px] tracking-mech uppercase text-signal">Valid</span>
              )}
              {tokenStatus === "invalid" && (
                <span className="font-mono text-[11px] tracking-mech uppercase text-danger">Invalid</span>
              )}
            </div>
          </div>
          <div>
            <label className="eyebrow block mb-1.5">
              Accelerator Type
            </label>
            <select
              value={filters.accelerator_type}
              onChange={(e) => setFilters({ ...filters, accelerator_type: e.target.value })}
              className="input w-full"
            >
              <option value="">All</option>
              <option value="gpu">GPU only</option>
              <option value="neuron">Neuron only</option>
            </select>
          </div>
        </div>

        <div className="mb-4">
          <label className="eyebrow block mb-1.5">
            Model (HuggingFace ID)
          </label>
          <ModelCombobox
            value={model}
            onChange={setModel}
            onModelSelect={handleModelSelect}
            hfToken={hfToken}
          />
        </div>

        <button
          onClick={handleEstimate}
          disabled={!model.trim() || loading}
          className="btn btn-primary h-10 px-6 text-[13px]"
        >
          {loading ? "Analyzing..." : "Get Estimates"}
        </button>

        {error && (
          <p className="mt-3 font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">
            {error}
          </p>
        )}
      </div>

      {/* Results */}
      {result && (
        <>
          {/* Model Info */}
          <div className="panel-inset p-4 mb-4">
            <div className="flex flex-wrap gap-x-6 gap-y-2 font-mono text-[12.5px]">
              <span className="flex items-baseline gap-2">
                <span className="eyebrow">MODEL</span>
                <span className="text-ink-0">{result.model_info.hf_id}</span>
              </span>
              <span className="flex items-baseline gap-2">
                <span className="eyebrow">PARAMS</span>
                <span className="text-ink-0 tabular">
                  {(result.model_info.parameter_count / 1e9).toFixed(1)}B
                </span>
              </span>
              <span className="flex items-baseline gap-2">
                <span className="eyebrow">DTYPE</span>
                <span className="text-ink-0">{result.model_info.native_dtype || "bfloat16"}</span>
              </span>
              <span className="flex items-baseline gap-2">
                <span className="eyebrow">MAX CTX</span>
                <span className="text-ink-0 tabular">
                  {result.model_info.max_position_embeddings.toLocaleString()}
                </span>
              </span>
            </div>
          </div>

          {/* Summary */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <div className="border border-signal/40 bg-signal/5 p-3">
              <div className="font-mono text-[22px] tabular text-signal">
                {result.summary.feasible_native}
              </div>
              <div className="caption text-signal mt-1">NATIVE PRECISION</div>
            </div>
            <div className="border border-warn/40 bg-warn/5 p-3">
              <div className="font-mono text-[22px] tabular text-warn">
                {result.summary.feasible_quantized}
              </div>
              <div className="caption text-warn mt-1">WITH QUANTIZATION</div>
            </div>
            <div className="border border-danger/40 bg-danger/5 p-3">
              <div className="font-mono text-[22px] tabular text-danger">
                {result.summary.infeasible}
              </div>
              <div className="caption text-danger mt-1">INFEASIBLE</div>
            </div>
            <div className="border border-info/40 bg-info/5 p-3">
              <div className="font-mono text-[13px] text-info truncate">
                {result.summary.cheapest_feasible || "-"}
              </div>
              <div className="caption text-info mt-1">CHEAPEST OPTION</div>
            </div>
          </div>

          {/* Results Table */}
          <div className="panel overflow-x-auto">
            <table className="data-table min-w-full">
              <thead className="bg-surface-1">
                <tr>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Instance
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Accelerator
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Status
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Quant
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Context
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    $/hr
                  </th>
                  <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                    Action
                  </th>
                </tr>
              </thead>
              <tbody>
                {result.estimates.map((row) => (
                  <>
                    <tr
                      key={row.instance_type}
                      className={`hover:bg-surface-1 cursor-pointer ${
                        !row.feasible ? "opacity-60" : ""
                      }`}
                      onClick={() =>
                        setExpandedRow(
                          expandedRow === row.instance_type ? null : row.instance_type
                        )
                      }
                    >
                      <td className="py-2.5 px-3 border-b border-line/60 text-ink-0 font-mono text-[12.5px]">
                        {row.instance_type}
                      </td>
                      <td className="py-2.5 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                        {row.accelerator_count}x {row.accelerator_name}
                      </td>
                      <td className="py-2.5 px-3 border-b border-line/60">{getFeasibilityBadge(row)}</td>
                      <td className="py-2.5 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                        {row.config?.quantization || "None"}
                      </td>
                      <td className="py-2.5 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                        {row.config?.max_model_len
                          ? `${(row.config.max_model_len / 1000).toFixed(0)}K`
                          : "-"}
                      </td>
                      <td className="py-2.5 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                        {row.cost ? `$${row.cost.hourly_usd.toFixed(2)}` : "-"}
                      </td>
                      <td className="py-2.5 px-3 border-b border-line/60">
                        {row.feasible && (
                          <button
                            onClick={(e) => {
                              e.stopPropagation();
                              handleRunBenchmark(row);
                            }}
                            className="btn btn-primary h-7 px-3 text-[11px]"
                          >
                            Run
                          </button>
                        )}
                      </td>
                    </tr>
                    {expandedRow === row.instance_type && (
                      <tr key={`${row.instance_type}-expanded`}>
                        <td colSpan={7} className="p-4 bg-surface-2 border-b border-line">
                          {row.feasible && row.config ? (
                            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                              <div>
                                <span className="eyebrow mr-1.5">TP</span>{" "}
                                <span className="font-medium">{row.config.tensor_parallel_degree}</span>
                              </div>
                              <div>
                                <span className="eyebrow mr-1.5">CONCURRENCY</span>{" "}
                                <span className="font-medium">{row.config.concurrency}</span>
                              </div>
                              <div>
                                <span className="eyebrow mr-1.5">MEMORY</span>{" "}
                                <span className="font-medium">
                                  {row.memory
                                    ? `${row.memory.model_weights_gib.toFixed(0)} / ${row.memory.available_gib.toFixed(0)} GiB (${row.memory.utilization_pct.toFixed(0)}%)`
                                    : "-"}
                                </span>
                              </div>
                              <div>
                                <span className="eyebrow mr-1.5">BENCHMARK</span>{" "}
                                <span className="font-medium">
                                  {row.has_benchmark_data ? "Available" : "Not yet run"}
                                </span>
                              </div>
                            </div>
                          ) : (
                            <p className="font-mono text-[12px] text-danger">{row.explanation}</p>
                          )}
                        </td>
                      </tr>
                    )}
                  </>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      </div>
    </>
  );
}
