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
      return <span className="text-red-600 font-medium">Infeasible</span>;
    }
    if (row.requires_quantization) {
      return <span className="text-amber-600 font-medium">Quantized</span>;
    }
    return <span className="text-green-600 font-medium">Native</span>;
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
      <div className="bg-white rounded-lg border border-gray-200 p-4 mb-6">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              HF Token (optional, for gated models)
            </label>
            <div className="flex items-center gap-2">
              <input
                type="password"
                value={hfToken}
                onChange={(e) => setHfToken(e.target.value)}
                onBlur={handleTokenBlur}
                placeholder="hf_..."
                className="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm"
              />
              {tokenStatus === "valid" && (
                <span className="text-green-600 text-sm">Valid</span>
              )}
              {tokenStatus === "invalid" && (
                <span className="text-red-600 text-sm">Invalid</span>
              )}
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Accelerator Type
            </label>
            <select
              value={filters.accelerator_type}
              onChange={(e) => setFilters({ ...filters, accelerator_type: e.target.value })}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
            >
              <option value="">All</option>
              <option value="gpu">GPU only</option>
              <option value="neuron">Neuron only</option>
            </select>
          </div>
        </div>

        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
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
          className="rounded-md bg-blue-600 px-6 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? "Analyzing..." : "Get Estimates"}
        </button>

        {error && (
          <p className="mt-3 text-sm text-red-600 bg-red-50 rounded-md px-3 py-2">
            {error}
          </p>
        )}
      </div>

      {/* Results */}
      {result && (
        <>
          {/* Model Info */}
          <div className="bg-gray-50 rounded-lg border border-gray-200 p-4 mb-4">
            <div className="flex flex-wrap gap-4 text-sm">
              <span>
                <span className="text-gray-500">Model:</span>{" "}
                <span className="font-medium">{result.model_info.hf_id}</span>
              </span>
              <span>
                <span className="text-gray-500">Params:</span>{" "}
                <span className="font-medium">
                  {(result.model_info.parameter_count / 1e9).toFixed(1)}B
                </span>
              </span>
              <span>
                <span className="text-gray-500">Dtype:</span>{" "}
                <span className="font-medium">{result.model_info.native_dtype || "bfloat16"}</span>
              </span>
              <span>
                <span className="text-gray-500">Max Context:</span>{" "}
                <span className="font-medium">
                  {result.model_info.max_position_embeddings.toLocaleString()}
                </span>
              </span>
            </div>
          </div>

          {/* Summary */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <div className="bg-green-50 rounded-lg p-3 border border-green-200">
              <div className="text-2xl font-bold text-green-700">
                {result.summary.feasible_native}
              </div>
              <div className="text-sm text-green-600">Native precision</div>
            </div>
            <div className="bg-amber-50 rounded-lg p-3 border border-amber-200">
              <div className="text-2xl font-bold text-amber-700">
                {result.summary.feasible_quantized}
              </div>
              <div className="text-sm text-amber-600">With quantization</div>
            </div>
            <div className="bg-red-50 rounded-lg p-3 border border-red-200">
              <div className="text-2xl font-bold text-red-700">
                {result.summary.infeasible}
              </div>
              <div className="text-sm text-red-600">Infeasible</div>
            </div>
            <div className="bg-blue-50 rounded-lg p-3 border border-blue-200">
              <div className="text-sm font-medium text-blue-700 truncate">
                {result.summary.cheapest_feasible || "-"}
              </div>
              <div className="text-sm text-blue-600">Cheapest option</div>
            </div>
          </div>

          {/* Results Table */}
          <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Instance
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Accelerator
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Status
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Quant
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Context
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    $/hr
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                    Action
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {result.estimates.map((row) => (
                  <>
                    <tr
                      key={row.instance_type}
                      className={`hover:bg-gray-50 cursor-pointer ${
                        !row.feasible ? "opacity-60" : ""
                      }`}
                      onClick={() =>
                        setExpandedRow(
                          expandedRow === row.instance_type ? null : row.instance_type
                        )
                      }
                    >
                      <td className="px-4 py-3 text-sm font-medium text-gray-900">
                        {row.instance_type}
                      </td>
                      <td className="px-4 py-3 text-sm text-gray-600">
                        {row.accelerator_count}x {row.accelerator_name}
                      </td>
                      <td className="px-4 py-3 text-sm">{getFeasibilityBadge(row)}</td>
                      <td className="px-4 py-3 text-sm text-gray-600">
                        {row.config?.quantization || "None"}
                      </td>
                      <td className="px-4 py-3 text-sm text-gray-600">
                        {row.config?.max_model_len
                          ? `${(row.config.max_model_len / 1000).toFixed(0)}K`
                          : "-"}
                      </td>
                      <td className="px-4 py-3 text-sm text-gray-600">
                        {row.cost ? `$${row.cost.hourly_usd.toFixed(2)}` : "-"}
                      </td>
                      <td className="px-4 py-3 text-sm">
                        {row.feasible && (
                          <button
                            onClick={(e) => {
                              e.stopPropagation();
                              handleRunBenchmark(row);
                            }}
                            className="rounded bg-blue-600 px-3 py-1 text-xs font-medium text-white hover:bg-blue-700"
                          >
                            Run
                          </button>
                        )}
                      </td>
                    </tr>
                    {expandedRow === row.instance_type && (
                      <tr key={`${row.instance_type}-expanded`}>
                        <td colSpan={7} className="px-4 py-3 bg-gray-50">
                          {row.feasible && row.config ? (
                            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                              <div>
                                <span className="text-gray-500">Tensor Parallel:</span>{" "}
                                <span className="font-medium">{row.config.tensor_parallel_degree}</span>
                              </div>
                              <div>
                                <span className="text-gray-500">Concurrency:</span>{" "}
                                <span className="font-medium">{row.config.concurrency}</span>
                              </div>
                              <div>
                                <span className="text-gray-500">Memory:</span>{" "}
                                <span className="font-medium">
                                  {row.memory
                                    ? `${row.memory.model_weights_gib.toFixed(0)} / ${row.memory.available_gib.toFixed(0)} GiB (${row.memory.utilization_pct.toFixed(0)}%)`
                                    : "-"}
                                </span>
                              </div>
                              <div>
                                <span className="text-gray-500">Benchmark:</span>{" "}
                                <span className="font-medium">
                                  {row.has_benchmark_data ? "Available" : "Not yet run"}
                                </span>
                              </div>
                            </div>
                          ) : (
                            <p className="text-sm text-red-600">{row.explanation}</p>
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
