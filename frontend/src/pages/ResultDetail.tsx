import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import {
  getRun,
  getMetrics,
  getExportManifestUrl,
  getExportReportUrl,
  listInstanceTypes,
  listPricing,
} from "../api";
import type {
  BenchmarkRun,
  BenchmarkMetrics,
  InstanceType,
  PricingRow,
  PricingTier,
} from "../types";
import MetricCard from "../components/MetricCard";
import LatencyDistribution from "../components/LatencyDistribution";
import HeroBlock from "../components/HeroBlock";
import ConfigPanel from "../components/ConfigPanel";
import PricingToggle from "../components/PricingToggle";
import {
  hourlyRateFromMap,
  costPerRequest,
  costPer1MTokens,
  totalSpent,
  toPricingMap,
} from "../lib/cost";

function SectionHeader({
  index,
  label,
}: {
  index: string;
  label: string;
}) {
  return (
    <div className="flex items-baseline gap-3 mb-3">
      <span className="font-mono text-[11px] tracking-widemech text-ink-2">[ {index} ]</span>
      <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">{label}</h2>
    </div>
  );
}

export default function ResultDetail() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<BenchmarkRun | null>(null);
  const [metrics, setMetrics] = useState<BenchmarkMetrics | null>(null);
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [pricing, setPricing] = useState<PricingRow[]>([]);
  const [pricingTier, setPricingTier] = useState<PricingTier>("on_demand");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    getRun(id).then(setRun).catch((e) => setError(e.message));
    getMetrics(id)
      .then(setMetrics)
      .catch(() => {
        /* metrics may not exist yet */
      });
    listInstanceTypes().then(setInstanceTypes).catch(() => {});
    listPricing().then(setPricing).catch(() => {});
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
        if (updated.status === "failed") clearInterval(interval);
      });
    }, 5000);
    return () => clearInterval(interval);
  }, [run]);

  const pricingMap = useMemo(() => toPricingMap(pricing), [pricing]);
  const instanceType = useMemo(
    () => instanceTypes.find((it) => it.id === run?.instance_type_id),
    [instanceTypes, run?.instance_type_id]
  );

  if (error) {
    return (
      <div className="p-6">
        <p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">
          {error}
        </p>
      </div>
    );
  }
  if (!run) return <div className="p-6 caption">LOADING…</div>;

  const isNeuron = (instanceType?.accelerator_type ?? "").toLowerCase() === "neuron";
  const acceleratorNoun = isNeuron ? "chip" : "GPU";
  const acceleratorCount = instanceType?.accelerator_count ?? 0;

  const succeeded = metrics?.successful_requests ?? 0;
  const failed = metrics?.failed_requests ?? 0;
  const totalReqs = succeeded + failed;
  const successRate = totalReqs > 0 ? (succeeded / totalReqs) * 100 : undefined;

  const aggregateTps =
    metrics?.throughput_aggregate_tps ?? metrics?.generation_throughput_tps;
  const perAccelTps =
    acceleratorCount > 0 && aggregateTps !== undefined
      ? aggregateTps / acceleratorCount
      : undefined;

  const hourly = hourlyRateFromMap(
    pricingMap,
    instanceType?.name ?? "",
    pricingTier
  );
  const perRequestCost = costPerRequest(hourly, metrics?.requests_per_second);
  const per1MCost = costPer1MTokens(hourly, aggregateTps);
  const spent = totalSpent(hourly, metrics?.total_duration_seconds);

  const instanceSummary = instanceType
    ? `${instanceType.name} · ${instanceType.accelerator_count}×${instanceType.accelerator_name} · ${instanceType.accelerator_memory_gib} GiB`
    : "—";

  const statusBadge = (
    <span className="flex items-center gap-2 font-mono text-[11px] tracking-widemech uppercase">
      <span className={`status-dot status-${run.status === "pending" ? "pending" : run.status}`} />
      {run.status}
    </span>
  );

  const runningCaption =
    run.status === "running" || run.status === "pending"
      ? "RESULTS WILL APPEAR WHEN COMPLETE"
      : null;

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
        <PricingToggle value={pricingTier} onChange={setPricingTier} />
      </div>

      <div className="p-6 max-w-6xl mx-auto animate-enter">
        <HeroBlock
          eyebrow="[ RUN ]"
          heading={run.model_hf_id || "(model)"}
          subheading={instanceSummary}
          meta={`${run.id.slice(0, 8)} · ${run.id}`}
          statusBadge={statusBadge}
          metrics={
            metrics
              ? [
                  { label: "TTFT p99", value: metrics.ttft_p99_ms, unit: "ms", precision: 0 },
                  { label: "Throughput", value: aggregateTps, unit: "tok/s", precision: 0 },
                  {
                    label: "Success Rate",
                    value: successRate,
                    unit: "%",
                    precision: 1,
                    accent: successRate !== undefined && successRate < 99 ? "warn" : "signal",
                  },
                  { label: "Cost / 1M tok", value: per1MCost, unit: "$", precision: 2 },
                ]
              : undefined
          }
        />

        {runningCaption && (
          <p className="mb-6 meta text-info">{runningCaption}</p>
        )}

        {run.status === "failed" && run.error_message && (
          <div className="border border-danger/40 bg-danger/5 p-4 mb-6">
            <p className="eyebrow text-danger mb-1.5">[ RUN FAILED ]</p>
            <p className="font-mono text-[12.5px] text-danger">{run.error_message}</p>
          </div>
        )}

        <ConfigPanel
          headline={[
            { label: "TP Degree", value: run.tensor_parallel_degree },
            { label: "Quantization", value: run.quantization ?? "default" },
            { label: "Concurrency", value: run.concurrency },
            { label: "Dataset", value: run.dataset_name },
          ]}
          details={[
            { label: "Framework", value: `${run.framework} ${run.framework_version}` },
            { label: "Run Type", value: run.run_type },
            { label: "Input Seq", value: run.input_sequence_length },
            { label: "Output Seq", value: run.output_sequence_length },
          ]}
        />

        {metrics && (
          <>
            {/* A. LATENCY */}
            <section className="mb-8">
              <SectionHeader index="A" label="Latency distribution" />
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
                <LatencyDistribution
                  label="TTFT"
                  p50={metrics.ttft_p50_ms}
                  p90={metrics.ttft_p90_ms}
                  p95={metrics.ttft_p95_ms}
                  p99={metrics.ttft_p99_ms}
                />
                <LatencyDistribution
                  label="E2E"
                  p50={metrics.e2e_latency_p50_ms}
                  p90={metrics.e2e_latency_p90_ms}
                  p95={metrics.e2e_latency_p95_ms}
                  p99={metrics.e2e_latency_p99_ms}
                />
                <LatencyDistribution
                  label="ITL"
                  p50={metrics.itl_p50_ms}
                  p90={metrics.itl_p90_ms}
                  p95={metrics.itl_p95_ms}
                  p99={metrics.itl_p99_ms}
                />
                <LatencyDistribution
                  label="TPOT"
                  p50={metrics.tpot_p50_ms}
                  p90={metrics.tpot_p90_ms}
                  p99={metrics.tpot_p99_ms}
                />
              </div>
            </section>

            {/* B. THROUGHPUT */}
            <section className="mb-8">
              <SectionHeader index="B" label="Throughput" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Aggregate" value={aggregateTps} unit="tok/s" precision={0} />
                <MetricCard
                  label={`Per ${acceleratorNoun}`}
                  value={perAccelTps}
                  unit={`tok/s/${acceleratorNoun}`}
                  precision={0}
                />
                <MetricCard
                  label="Requests/sec"
                  value={metrics.requests_per_second}
                  unit="rps"
                  precision={2}
                />
                <MetricCard
                  label="Success Rate"
                  value={successRate}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="Prompt"
                  value={metrics.prompt_throughput_tps}
                  unit="tok/s"
                  precision={0}
                />
                <MetricCard
                  label="Generation"
                  value={metrics.generation_throughput_tps}
                  unit="tok/s"
                  precision={0}
                />
                <MetricCard
                  label="Avg Output"
                  value={metrics.output_length_mean}
                  unit="tokens"
                  precision={0}
                />
                <MetricCard
                  label="Duration"
                  value={metrics.total_duration_seconds}
                  unit="s"
                  precision={0}
                />
              </div>
            </section>

            {/* C. HARDWARE UTILIZATION */}
            <section className="mb-8">
              <SectionHeader index="C" label="Hardware utilization" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard
                  label={`${acceleratorNoun} Busy (avg)`}
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
              </div>
            </section>

            {/* D. MEMORY */}
            <section className="mb-8">
              <SectionHeader index="D" label="Memory" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
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
                <MetricCard
                  label="KV Cache (avg)"
                  value={metrics.kv_cache_utilization_avg_pct}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="KV Cache (peak)"
                  value={metrics.kv_cache_utilization_peak_pct}
                  unit="%"
                  precision={1}
                />
                <MetricCard
                  label="Prefix Hit"
                  value={metrics.prefix_cache_hit_rate}
                  unit="%"
                  precision={1}
                />
              </div>
            </section>

            {/* E. REQUEST FLOW */}
            <section className="mb-8">
              <SectionHeader index="E" label="Request flow" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Successful" value={succeeded} unit="" precision={0} />
                <MetricCard label="Failed" value={failed} unit="" precision={0} />
                <MetricCard
                  label="Queue Max"
                  value={metrics.waiting_requests_max}
                  unit="req"
                  precision={0}
                />
                <MetricCard
                  label="Running (avg)"
                  value={metrics.running_requests_avg}
                  unit="req"
                  precision={1}
                />
                <MetricCard
                  label="Running (max)"
                  value={metrics.running_requests_max}
                  unit="req"
                  precision={0}
                />
                <MetricCard
                  label="Preemptions"
                  value={metrics.preemption_count}
                  unit=""
                  precision={0}
                />
              </div>
            </section>

            {/* F. COST */}
            <section className="mb-8">
              <SectionHeader index="F" label="Cost" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Hourly" value={hourly ?? undefined} unit="$" precision={2} />
                <MetricCard
                  label="Per Request"
                  value={perRequestCost ?? undefined}
                  unit="$"
                  precision={6}
                />
                <MetricCard
                  label="Per 1M Tokens"
                  value={per1MCost ?? undefined}
                  unit="$"
                  precision={2}
                />
                <MetricCard
                  label="Total Spent"
                  value={spent ?? undefined}
                  unit="$"
                  precision={2}
                />
              </div>
            </section>

            {/* Export buttons */}
            <div className="mt-8 pt-6 hairline">
              <div className="flex gap-4">
                <a href={getExportReportUrl(run.id)} download className="btn btn-primary">
                  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
                    />
                  </svg>
                  Export Report
                </a>
                <a href={getExportManifestUrl(run.id)} download className="btn">
                  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"
                    />
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
