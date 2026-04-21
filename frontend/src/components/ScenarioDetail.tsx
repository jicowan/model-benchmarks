import type { ScenarioResult } from "../types";
import LatencyDistribution from "./LatencyDistribution";
import MetricCard from "./MetricCard";

interface Props {
  result: ScenarioResult;
  acceleratorNoun: string; // "GPU" or "chip"
  acceleratorCount?: number;
}

// Per-scenario detail block for the Suite Results page.
// Mirrors the Run Report's section grouping but compressed — no hero,
// no cost section (cost is the same across all scenarios in one suite
// since they share an instance and a duration budget).
export default function ScenarioDetail({ result, acceleratorNoun, acceleratorCount = 0 }: Props) {
  if (result.status === "failed" || result.status === "skipped") {
    return (
      <div className="mt-3">
        {result.error_message && (
          <div className="font-mono text-[12px] text-danger bg-danger/5 border border-danger/30 px-3 py-2">
            {result.error_message}
          </div>
        )}
      </div>
    );
  }
  if (result.status !== "completed") return null;

  const succeeded = result.successful_requests ?? 0;
  const failed = result.failed_requests ?? 0;
  const total = succeeded + failed;
  const successRate = total > 0 ? (succeeded / total) * 100 : undefined;

  const throughput = result.throughput_tps;
  const perAccel =
    acceleratorCount > 0 && throughput !== undefined ? throughput / acceleratorCount : undefined;

  return (
    <div className="mt-4 flex flex-col gap-4">
      {/* Latency distribution */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
        <LatencyDistribution
          label="TTFT"
          p50={result.ttft_p50_ms}
          p90={result.ttft_p90_ms}
          p95={result.ttft_p95_ms}
          p99={result.ttft_p99_ms}
        />
        <LatencyDistribution
          label="E2E"
          p50={result.e2e_latency_p50_ms}
          p90={result.e2e_latency_p90_ms}
          p95={result.e2e_latency_p95_ms}
          p99={result.e2e_latency_p99_ms}
        />
        <LatencyDistribution
          label="ITL"
          p50={result.itl_p50_ms}
          p90={result.itl_p90_ms}
          p95={result.itl_p95_ms}
          p99={result.itl_p99_ms}
        />
        <LatencyDistribution
          label="TPOT"
          p50={result.tpot_p50_ms}
          p90={result.tpot_p90_ms}
          p99={result.tpot_p99_ms}
        />
      </div>

      {/* Throughput + success */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MetricCard label="Throughput" value={throughput} unit="tok/s" precision={0} />
        <MetricCard
          label={`Per ${acceleratorNoun}`}
          value={perAccel}
          unit={`tok/s/${acceleratorNoun}`}
          precision={0}
        />
        <MetricCard
          label="Success Rate"
          value={successRate}
          unit="%"
          precision={1}
        />
        <MetricCard
          label="Requests"
          value={total}
          unit={`${succeeded} ok`}
          precision={0}
        />
      </div>

      {/* Hardware utilization */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MetricCard
          label={`${acceleratorNoun} Busy (avg)`}
          value={result.accelerator_utilization_avg_pct ?? result.accelerator_utilization_pct}
          unit="%"
          precision={0}
        />
        <MetricCard
          label="SM Active (avg)"
          value={result.sm_active_avg_pct}
          unit="%"
          precision={0}
        />
        <MetricCard
          label="Tensor Active (avg)"
          value={result.tensor_active_avg_pct}
          unit="%"
          precision={0}
        />
        <MetricCard
          label="DRAM Active (avg)"
          value={result.dram_active_avg_pct}
          unit="%"
          precision={0}
        />
      </div>

      {/* Memory + flow */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MetricCard
          label="Memory (avg)"
          value={result.accelerator_memory_avg_gib}
          unit="GiB"
        />
        <MetricCard
          label="Memory (peak)"
          value={result.accelerator_memory_peak_gib}
          unit="GiB"
        />
        <MetricCard
          label="Queue Max"
          value={result.waiting_requests_max}
          unit="req"
          precision={0}
        />
        <MetricCard label="Failed" value={failed} unit="" precision={0} />
      </div>

    </div>
  );
}
