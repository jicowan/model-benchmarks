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
import { getRun, getMetrics } from "../api";
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

  const statusColor: Record<string, string> = {
    pending: "bg-yellow-100 text-yellow-800",
    running: "bg-blue-100 text-blue-800",
    completed: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
  };

  const latencyBars = metrics
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
        {
          name: "ITL",
          p50: metrics.itl_p50_ms ?? 0,
          p90: metrics.itl_p90_ms ?? 0,
          p95: metrics.itl_p95_ms ?? 0,
          p99: metrics.itl_p99_ms ?? 0,
        },
      ]
    : [];

  return (
    <div>
      <div className="flex items-center gap-4 mb-6">
        <h1 className="text-2xl font-bold">Run {run.id.slice(0, 8)}</h1>
        <span
          className={`px-3 py-1 rounded-full text-sm font-medium ${statusColor[run.status] ?? "bg-gray-100"}`}
        >
          {run.status}
        </span>
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
              label="Throughput"
              value={metrics.throughput_aggregate_tps}
              unit="tok/s"
              precision={0}
            />
            <MetricCard
              label="Per-Request TPS"
              value={metrics.throughput_per_request_tps}
              unit="tok/s"
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

          {/* Latency percentile chart */}
          <div className="bg-white border border-gray-200 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-4">
              Latency Percentiles (ms)
            </h3>
            <ResponsiveContainer width="100%" height={300}>
              <BarChart data={latencyBars}>
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
        </>
      )}
    </div>
  );
}
