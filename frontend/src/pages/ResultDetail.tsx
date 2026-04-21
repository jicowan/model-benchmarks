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
import { getRun, getMetrics, getExportManifestUrl, getExportReportUrl, listInstanceTypes } from "../api";
import type { BenchmarkRun, BenchmarkMetrics, InstanceType } from "../types";
import MetricCard from "../components/MetricCard";
import {
  useChartTheme,
  percentileRamp,
  axisStyle,
  gridProps,
  ChartTooltip,
  ChartLegend,
} from "../components/ChartTheme";
import { Legend } from "recharts";

export default function ResultDetail() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<BenchmarkRun | null>(null);
  const [metrics, setMetrics] = useState<BenchmarkMetrics | null>(null);
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [error, setError] = useState("");
  const chartTheme = useChartTheme();
  const ramp = percentileRamp();

  useEffect(() => {
    if (!id) return;
    getRun(id).then(setRun).catch((e) => setError(e.message));
    getMetrics(id)
      .then(setMetrics)
      .catch(() => {
        /* metrics may not exist yet */
      });
    listInstanceTypes().then(setInstanceTypes).catch(() => {});
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
    return <div className="p-6"><p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">{error}</p></div>;
  }
  if (!run) {
    return <div className="p-6 caption">LOADING…</div>;
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
      <div className="panel p-6 mb-8">
        <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-4 pb-2 border-b border-line">Configuration</h2>
        <dl className="grid grid-cols-2 md:grid-cols-4 gap-4">
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
              <dt className="eyebrow">{label}</dt>
              <dd className="font-mono text-[12.5px] text-ink-0">{value}</dd>
            </div>
          ))}
        </dl>
      </div>

      {run.status === "running" && (
        <p className="meta text-info mb-6 flex items-center gap-2">
          <span className="w-1.5 h-1.5 bg-signal animate-pulse_signal" />
          BENCHMARK IS RUNNING · RESULTS WILL APPEAR WHEN COMPLETE
        </p>
      )}

      {run.status === "failed" && run.error_message && (
        <div className="border border-danger/40 bg-danger/5 p-4 mb-6">
          <p className="eyebrow text-danger mb-1.5">[ RUN FAILED ]</p>
          <p className="font-mono text-[12.5px] text-danger">{run.error_message}</p>
        </div>
      )}

      {metrics && (
        <>
          {(() => {
            const acceleratorCount =
              instanceTypes.find((it) => it.id === run.instance_type_id)
                ?.accelerator_count ?? 0;
            const succeeded = metrics.successful_requests ?? 0;
            const failed = metrics.failed_requests ?? 0;
            const totalReqs = succeeded + failed;
            const successRate =
              totalReqs > 0 ? (succeeded / totalReqs) * 100 : undefined;
            const throughput =
              metrics.throughput_aggregate_tps ??
              metrics.generation_throughput_tps;
            const perAccelTPS =
              acceleratorCount > 0 && throughput !== undefined
                ? throughput / acceleratorCount
                : undefined;
            const acceleratorUnit =
              (run.framework ?? "").toLowerCase().includes("neuron")
                ? "tok/s/chip"
                : "tok/s/GPU";
            return (
              <>
                {/* Headline metric cards */}
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
                    label="GPU Busy (avg)"
                    value={
                      metrics.accelerator_utilization_avg_pct ??
                      metrics.accelerator_utilization_pct
                    }
                    unit="%"
                    precision={0}
                  />
                  <MetricCard
                    label="SM Active (avg)"
                    value={metrics.sm_active_avg_pct}
                    unit="%"
                    precision={0}
                  />
                  <MetricCard
                    label="Tensor Active (avg)"
                    value={metrics.tensor_active_avg_pct}
                    unit="%"
                    precision={0}
                  />
                  <MetricCard
                    label="DRAM Active (avg)"
                    value={metrics.dram_active_avg_pct}
                    unit="%"
                    precision={0}
                  />
                  <MetricCard
                    label="Memory (avg)"
                    value={metrics.accelerator_memory_avg_gib}
                    unit="GiB"
                  />
                  <MetricCard
                    label="Memory (peak)"
                    value={metrics.accelerator_memory_peak_gib}
                    unit="GiB"
                  />
                </div>

                {/* Request stats */}
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
                  <MetricCard
                    label="Successful Requests"
                    value={succeeded}
                    unit=""
                    precision={0}
                  />
                  <MetricCard
                    label="Failed Requests"
                    value={failed}
                    unit=""
                    precision={0}
                  />
                  <MetricCard
                    label="Success Rate"
                    value={successRate}
                    unit="%"
                    precision={1}
                  />
                  <MetricCard
                    label={`Throughput (per ${acceleratorUnit.includes("chip") ? "chip" : "GPU"})`}
                    value={perAccelTPS}
                    unit={acceleratorUnit}
                    precision={0}
                  />
                  <MetricCard
                    label="Total Duration"
                    value={metrics.total_duration_seconds}
                    unit="s"
                  />
                  <MetricCard
                    label="Queue Max"
                    value={metrics.waiting_requests_max}
                    unit="req"
                    precision={0}
                  />
                </div>
              </>
            );
          })()}

          {/* Latency Breakdown section */}
          {(metrics.tpot_p50_ms || metrics.prefill_time_p50_ms || metrics.decode_time_p50_ms) && (
            <div className="panel p-6 mb-8">
              <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-4 pb-2 border-b border-line">Latency Breakdown</h2>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <MetricCard
                  label="TPOT p50"
                  value={metrics.tpot_p50_ms}
                  unit="ms"
                />
                <MetricCard
                  label="TPOT p99"
                  value={metrics.tpot_p99_ms}
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
            <div className="panel p-6 mb-8">
              <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-4 pb-2 border-b border-line">Cache & Memory</h2>
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
            <div className="panel p-6 mb-8">
              <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-4 pb-2 border-b border-line">Throughput Breakdown</h2>
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
          <div className="panel p-4">
            <div className="flex items-baseline justify-between mb-4">
              <h3 className="eyebrow">REQUEST LATENCY — PERCENTILES</h3>
              <span className="caption">ms</span>
            </div>
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={highLatencyBars} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
                <CartesianGrid {...gridProps} stroke={chartTheme.grid} />
                <XAxis dataKey="name" tickLine={false} axisLine={{ stroke: chartTheme.grid }} tick={axisStyle(chartTheme)} />
                <YAxis tickLine={false} axisLine={false} tick={axisStyle(chartTheme)} width={44} />
                <Tooltip content={<ChartTooltip unit="ms" />} cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }} />
                <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 8 }} />
                <Bar dataKey="p50" fill={ramp[0]} name="p50" />
                <Bar dataKey="p90" fill={ramp[1]} name="p90" />
                <Bar dataKey="p95" fill={ramp[2]} name="p95" />
                <Bar dataKey="p99" fill={ramp[3]} name="p99" />
              </BarChart>
            </ResponsiveContainer>
          </div>

          {/* Token-level latency chart (ITL, TPOT) */}
          <div className="panel p-4 mt-4">
            <div className="flex items-baseline justify-between mb-4">
              <h3 className="eyebrow">TOKEN LATENCY — PERCENTILES</h3>
              <span className="caption">ms</span>
            </div>
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={lowLatencyBars} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
                <CartesianGrid {...gridProps} stroke={chartTheme.grid} />
                <XAxis dataKey="name" tickLine={false} axisLine={{ stroke: chartTheme.grid }} tick={axisStyle(chartTheme)} />
                <YAxis tickLine={false} axisLine={false} tick={axisStyle(chartTheme)} width={44} />
                <Tooltip content={<ChartTooltip unit="ms" />} cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }} />
                <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 8 }} />
                <Bar dataKey="p50" fill={ramp[0]} name="p50" />
                <Bar dataKey="p90" fill={ramp[1]} name="p90" />
                <Bar dataKey="p95" fill={ramp[2]} name="p95" />
                <Bar dataKey="p99" fill={ramp[3]} name="p99" />
              </BarChart>
            </ResponsiveContainer>
          </div>

          {/* Export buttons */}
          <div className="mt-8 pt-6 hairline">
            <div className="flex gap-4">
              <a
                href={getExportReportUrl(run.id)}
                download
                className="btn btn-primary"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                </svg>
                Export Report
              </a>
              <a
                href={getExportManifestUrl(run.id)}
                download
                className="btn"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                </svg>
                Export K8s Manifest
              </a>
            </div>
            <p className="mt-2 caption">
              Download HTML report for sharing or K8s manifest to deploy this configuration
            </p>
          </div>
        </>
      )}
      </div>
    </>
  );
}
