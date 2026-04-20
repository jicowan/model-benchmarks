import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { getRun, getMetrics, getExportManifestUrl, getExportReportUrl } from "../api";
import type { BenchmarkRun, BenchmarkMetrics } from "../types";
import MetricCard from "../components/MetricCard";

export default function ResultDetail() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<BenchmarkRun | null>(null);
  const [metrics, setMetrics] = useState<BenchmarkMetrics | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    getRun(id).then(setRun).catch((e) => setError(e.message));
    getMetrics(id)
      .then(setMetrics)
      .catch(() => {
        /* metrics may not exist yet */
      });
  }, [id]);

  useEffect(() => {
    if (!run || run.status === "completed" || run.status === "failed") return;

    const interval = setInterval(() => {
      getRun(run.id).then((updated) => {
        setRun(updated);
        if (updated.status === "completed") {
          getMetrics(updated.id).then(setMetrics);
          clearInterval(interval);
        }
        if (updated.status === "failed") {
          clearInterval(interval);
        }
      });
    }, 5000);

    return () => clearInterval(interval);
  }, [run]);

  if (error) {
    return <p className="text-red-600">{error}</p>;
  }
  if (!run) {
    return <p className="text-gray-500">Loading...</p>;
  }

  // High latency metrics (request-level): TTFT, E2E
  const highLatencyBars = metrics
    ? [
        {
          name: "TTFT",
          p50: metrics.ttft_p50_ms ?? 0,
          p90: metrics.ttft_p90_ms ?? 0,
          p95: metrics.ttft_p95_ms ?? 0,
          p99: metrics.ttft_p99_ms ?? 0,
        },
        {
          name: "E2E",
          p50: metrics.e2e_latency_p50_ms ?? 0,
          p90: metrics.e2e_latency_p90_ms ?? 0,
          p95: metrics.e2e_latency_p95_ms ?? 0,
          p99: metrics.e2e_latency_p99_ms ?? 0,
        },
      ]
    : [];

  // Low latency metrics (token-level): ITL, TPOT
  const lowLatencyBars = metrics
    ? [
        {
          name: "ITL",
          p50: metrics.itl_p50_ms ?? 0,
          p90: metrics.itl_p90_ms ?? 0,
          p95: metrics.itl_p95_ms ?? 0,
          p99: metrics.itl_p99_ms ?? 0,
        },
        {
          name: "TPOT",
          p50: metrics.tpot_p50_ms ?? 0,
          p90: metrics.tpot_p90_ms ?? 0,
          p95: 0, // Not collected
          p99: metrics.tpot_p99_ms ?? 0,
        },
      ]
    : [];

  return (
    <>
      <div className="h-14 border-b border-line flex items-center justify-between px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <a href="/runs" className="text-ink-1 hover:text-ink-0">runs</a>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">{run.id.slice(0, 8)}</span>
        </div>
        <span className="flex items-center font-mono text-[11px] tracking-mech uppercase">
          <span className={`status-dot status-${run.status === "pending" ? "pending" : run.status}`} />
          {run.status}
        </span>
      </div>
      <div className="p-6 max-w-6xl mx-auto animate-enter">
      <div className="mb-6 flex items-baseline gap-4">
        <div className="eyebrow">[ RUN ]</div>
        <div className="font-mono text-[18px] text-ink-0">{run.id.slice(0, 8)}</div>
        <div className="caption">{run.id}</div>
      </div>

      {/* Run configuration */}
      <div className="bg-white border border-gray-200 rounded-lg p-6 mb-8">
        <h2 className="text-lg font-semibold mb-4">Configuration</h2>
        <dl className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          {[
            ["Framework", `${run.framework} ${run.framework_version}`],
            ["TP Degree", String(run.tensor_parallel_degree)],
            ["Concurrency", String(run.concurrency)],
            ["Quantization", run.quantization ?? "default"],
            ["Input Seq", String(run.input_sequence_length)],
            ["Output Seq", String(run.output_sequence_length)],
            ["Dataset", run.dataset_name],
            ["Type", run.run_type],
          ].map(([label, value]) => (
            <div key={label}>
              <dt className="text-gray-500">{label}</dt>
              <dd className="font-medium">{value}</dd>
            </div>
          ))}
        </dl>
      </div>

      {run.status === "running" && (
        <p className="text-blue-600 mb-6">
          Benchmark is running. Results will appear when complete...
        </p>
      )}

      {run.status === "failed" && run.error_message && (
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 mb-6">
          <p className="text-red-800 font-medium">Run Failed</p>
          <p className="text-red-700 text-sm mt-1">{run.error_message}</p>
        </div>
      )}

      {metrics && (
        <>
          {/* Metric cards */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
            <MetricCard
              label="TTFT p50"
              value={metrics.ttft_p50_ms}
              unit="ms"
            />
            <MetricCard
              label="E2E Latency p50"
              value={metrics.e2e_latency_p50_ms}
              unit="ms"
            />
            <MetricCard
              label="ITL p50"
              value={metrics.itl_p50_ms}
              unit="ms"
            />
            <MetricCard
              label="Requests/sec"
              value={metrics.requests_per_second}
              unit="rps"
              precision={2}
            />
            <MetricCard
              label="GPU Utilization"
              value={metrics.accelerator_utilization_pct}
              unit="%"
              precision={0}
            />
            <MetricCard
              label="Peak Memory"
              value={metrics.accelerator_memory_peak_gib}
              unit="GiB"
            />
          </div>

          {/* Request stats */}
          <div className="grid grid-cols-3 gap-4 mb-8">
            <MetricCard
              label="Successful Requests"
              value={metrics.successful_requests}
              unit=""
              precision={0}
            />
            <MetricCard
              label="Failed Requests"
              value={metrics.failed_requests}
              unit=""
              precision={0}
            />
            <MetricCard
              label="Total Duration"
              value={metrics.total_duration_seconds}
              unit="s"
            />
          </div>

          {/* Latency Breakdown section */}
          {(metrics.tpot_p50_ms || metrics.prefill_time_p50_ms || metrics.decode_time_p50_ms) && (
            <div className="bg-white border border-gray-200 rounded-lg p-6 mb-8">
              <h2 className="text-lg font-semibold mb-4">Latency Breakdown</h2>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <MetricCard
                  label="TPOT p50"
                  value={metrics.tpot_p50_ms}
                  unit="ms"
                />
                <MetricCard
                  label="Prefill p50"
                  value={metrics.prefill_time_p50_ms}
                  unit="ms"
                />
                <MetricCard
                  label="Decode p50"
                  value={metrics.decode_time_p50_ms}
                  unit="ms"
                />
                <MetricCard
                  label="Queue p50"
                  value={metrics.queue_time_p50_ms}
                  unit="ms"
                />
              </div>
            </div>
          )}

          {/* Cache & Memory section */}
          {(metrics.kv_cache_utilization_avg_pct || metrics.prefix_cache_hit_rate !== undefined || metrics.preemption_count !== undefined) && (
            <div className="bg-white border border-gray-200 rounded-lg p-6 mb-8">
              <h2 className="text-lg font-semibold mb-4">Cache & Memory</h2>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <MetricCard
                  label="KV Cache Avg"
                  value={metrics.kv_cache_utilization_avg_pct}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="KV Cache Peak"
                  value={metrics.kv_cache_utilization_peak_pct}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="Prefix Cache Hit"
                  value={metrics.prefix_cache_hit_rate}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="Preemptions"
                  value={metrics.preemption_count}
                  unit=""
                  precision={0}
                />
              </div>
            </div>
          )}

          {/* Throughput Breakdown section */}
          {(metrics.prompt_throughput_tps || metrics.generation_throughput_tps || metrics.output_length_mean) && (
            <div className="bg-white border border-gray-200 rounded-lg p-6 mb-8">
              <h2 className="text-lg font-semibold mb-4">Throughput Breakdown</h2>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <MetricCard
                  label="Input Throughput"
                  value={metrics.prompt_throughput_tps}
                  unit="tok/s"
                  precision={1}
                />
                <MetricCard
                  label="Output Throughput"
                  value={metrics.generation_throughput_tps}
                  unit="tok/s"
                  precision={1}
                />
                <MetricCard
                  label="Avg Output Length"
                  value={metrics.output_length_mean}
                  unit="tokens"
                  precision={0}
                />
                <MetricCard
                  label="Running Requests Avg"
                  value={metrics.running_requests_avg}
                  unit=""
                  precision={1}
                />
              </div>
            </div>
          )}

          {/* Request-level latency chart (TTFT, E2E) */}
          <div className="bg-white border border-gray-200 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-4">
              Request Latency Percentiles (ms)
            </h3>
            <ResponsiveContainer width="100%" height={250}>
              <BarChart data={highLatencyBars}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" />
                <YAxis />
                <Tooltip />
                <Bar dataKey="p50" fill="#2563eb" name="p50" />
                <Bar dataKey="p90" fill="#60a5fa" name="p90" />
                <Bar dataKey="p95" fill="#93c5fd" name="p95" />
                <Bar dataKey="p99" fill="#bfdbfe" name="p99" />
              </BarChart>
            </ResponsiveContainer>
          </div>

          {/* Token-level latency chart (ITL, TPOT) */}
          <div className="bg-white border border-gray-200 rounded-lg p-4 mt-4">
            <h3 className="text-sm font-medium text-gray-700 mb-4">
              Token Latency Percentiles (ms)
            </h3>
            <ResponsiveContainer width="100%" height={250}>
              <BarChart data={lowLatencyBars}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" />
                <YAxis />
                <Tooltip />
                <Bar dataKey="p50" fill="#2563eb" name="p50" />
                <Bar dataKey="p90" fill="#60a5fa" name="p90" />
                <Bar dataKey="p95" fill="#93c5fd" name="p95" />
                <Bar dataKey="p99" fill="#bfdbfe" name="p99" />
              </BarChart>
            </ResponsiveContainer>
          </div>

          {/* Export buttons */}
          <div className="mt-8 pt-6 border-t border-gray-200">
            <div className="flex gap-4">
              <a
                href={getExportReportUrl(run.id)}
                download
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                </svg>
                Export Report
              </a>
              <a
                href={getExportManifestUrl(run.id)}
                download
                className="inline-flex items-center gap-2 px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 transition-colors"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                </svg>
                Export K8s Manifest
              </a>
            </div>
            <p className="mt-2 text-sm text-gray-500">
              Download HTML report for sharing or K8s manifest to deploy this configuration
            </p>
          </div>
        </>
      )}
      </div>
    </>
  );
}
