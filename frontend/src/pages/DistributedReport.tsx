import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import {
  getRunDetail,
  getRunCSVUrl,
  getExportManifestUrl,
} from "../api";
import type {
  BenchmarkRun,
  BenchmarkMetrics,
  InstanceType,
  PricingRow,
  ShardMetric,
} from "../types";
import { hourlyRate, totalSpent, costPer1MTokens } from "../lib/cost";
import PrintButton from "../components/PrintButton";

// PRD-59: dedicated report for a distributed / disaggregated run — topology,
// the honest N-node cost breakdown, per-node/per-role GPU telemetry, and the
// loadgen result, with print/CSV/manifest export parity with ResultDetail
// (PRD-41). Single-instance runs keep using ResultDetail; this page is reached
// only from a distributed run.
export default function DistributedReport() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<BenchmarkRun | null>(null);
  const [metrics, setMetrics] = useState<BenchmarkMetrics | null>(null);
  const [instance, setInstance] = useState<InstanceType | null>(null);
  const [pricing, setPricing] = useState<PricingRow | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    getRunDetail(id, ["metrics", "instance", "pricing"])
      .then((d) => {
        setRun(d);
        setMetrics(d.metrics ?? null);
        setInstance(d.instance ?? null);
        setPricing(d.pricing ?? null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Failed to load run."));
  }, [id]);

  if (error) {
    return <div className="p-6 font-mono text-[12px] text-danger">{error}</div>;
  }
  if (!run) {
    return <div className="p-6 font-mono text-[12px] text-ink-2">Loading…</div>;
  }

  const disaggregated = run.deployment_mode === "disaggregated";
  const nodeCount = run.node_count ?? 1;

  // N-node cost: per-node hourly × node_count. computeRunCost already scaled the
  // persisted total; here we show the derivation from the per-node rate.
  const perNodeHourly = hourlyRate(pricing ?? undefined, "on_demand");
  const groupHourly = perNodeHourly != null ? perNodeHourly * nodeCount : null;
  const totalCost =
    run.total_cost_usd ??
    totalSpent(groupHourly, metrics?.total_duration_seconds) ??
    undefined;
  const per1M = costPer1MTokens(groupHourly, metrics?.throughput_aggregate_tps);

  const shards: ShardMetric[] = metrics?.shards ?? [];

  const topology = disaggregated
    ? `${run.prefill_replicas ?? "?"}P${run.decode_replicas ?? "?"}D · prefill TP=${run.prefill_tp ?? "?"} · decode TP=${run.decode_tp ?? "?"}`
    : `${nodeCount} nodes · TP=${run.tensor_parallel_degree} · PP=${run.pipeline_parallel_degree ?? "?"}`;

  const fmt = (n?: number | null, d = 1) =>
    n == null ? "—" : n.toFixed(d);
  const usd = (n?: number | null) => (n == null ? "—" : `$${n.toFixed(4)}`);

  return (
    <div className="flex flex-col">
      <div className="h-14 border-b border-line flex items-center justify-between px-6 bg-surface-0 sticky top-0 z-20 no-print">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-0">DISTRIBUTED REPORT</span>
          <span className="text-ink-2">— {disaggregated ? "prefill/decode" : "co-located"} · {run.model_hf_id ?? run.model_id}</span>
        </div>
        <Link to={`/results/${run.id}`} className="font-mono text-[11px] text-ink-2 hover:text-ink-0">
          ← standard view
        </Link>
      </div>

      <div className="p-6 max-w-4xl flex flex-col gap-6">
        {/* Topology header */}
        <section className="border border-line bg-surface-1 p-4">
          <div className="eyebrow text-ink-2 mb-2">[ TOPOLOGY ]</div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 font-mono text-[12px]">
            <Field label="Deployment" value={run.deployment_mode ?? "distributed"} />
            <Field label="Topology" value={topology} />
            <Field label="Nodes" value={String(nodeCount)} />
            <Field label="Fabric" value={run.network_mode ?? "—"} />
            {disaggregated && (
              <>
                <Field label="KV connector" value={run.kv_connector ?? "—"} />
                <Field label="KV transfer" value={run.kv_transfer_backend ?? "—"} />
              </>
            )}
            <Field label="Instance" value={instance?.name ?? "—"} />
            <Field label="Framework" value={`${run.framework} ${run.framework_version ?? ""}`.trim()} />
          </div>
        </section>

        {/* Cost breakdown */}
        <section className="border border-line bg-surface-1 p-4">
          <div className="eyebrow text-ink-2 mb-2">[ COST (N-NODE) ]</div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 font-mono text-[12px]">
            <Field label="Per-node / hr" value={usd(perNodeHourly)} />
            <Field label={`Group / hr (×${nodeCount})`} value={usd(groupHourly)} />
            <Field label="Total (run)" value={usd(totalCost)} />
            <Field label="$ / 1M tokens" value={per1M == null ? "—" : `$${per1M.toFixed(4)}`} />
          </div>
          <div className="mt-2 font-mono text-[10.5px] text-ink-2">
            Total = per-node hourly × {nodeCount} nodes × run lifetime. $/1M tokens uses the group hourly over aggregate throughput.
          </div>
        </section>

        {/* Loadgen result */}
        <section className="border border-line bg-surface-1 p-4">
          <div className="eyebrow text-ink-2 mb-2">[ LOADGEN RESULT ]</div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 font-mono text-[12px]">
            <Field label="Requests OK" value={`${metrics?.successful_requests ?? "—"} / ${(metrics?.successful_requests ?? 0) + (metrics?.failed_requests ?? 0)}`} />
            <Field label="Req/s" value={fmt(metrics?.requests_per_second, 2)} />
            <Field label="Output tok/s" value={fmt(metrics?.generation_throughput_tps, 0)} />
            <Field label="Aggregate tok/s" value={fmt(metrics?.throughput_aggregate_tps, 0)} />
            <Field label="TTFT p50 (ms)" value={fmt(metrics?.ttft_p50_ms, 0)} />
            <Field label="TTFT p90 (ms)" value={fmt(metrics?.ttft_p90_ms, 0)} />
            <Field label="E2E p50 (ms)" value={fmt(metrics?.e2e_latency_p50_ms, 0)} />
            <Field label="E2E p90 (ms)" value={fmt(metrics?.e2e_latency_p90_ms, 0)} />
          </div>
        </section>

        {/* Per-node / per-role GPU telemetry — the marquee view */}
        <section className="border border-line bg-surface-1 p-4">
          <div className="eyebrow text-ink-2 mb-2">[ GPU TELEMETRY — PER NODE / ROLE ]</div>
          {shards.length === 0 ? (
            <div className="font-mono text-[11.5px] text-ink-2">
              No per-node breakdown recorded (GPU metrics may not have been collected for this run).
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full font-mono text-[11.5px]">
                <thead>
                  <tr className="text-ink-2 text-left border-b border-line">
                    <th className="py-1 pr-3">Node</th>
                    <th className="py-1 pr-3">Role</th>
                    <th className="py-1 pr-3">Util avg/peak %</th>
                    <th className="py-1 pr-3">Mem avg/peak GiB</th>
                    <th className="py-1 pr-3">SM %</th>
                    <th className="py-1 pr-3">Tensor %</th>
                    <th className="py-1 pr-3">DRAM %</th>
                  </tr>
                </thead>
                <tbody>
                  {shards.map((s, i) => (
                    <tr key={i} className="border-b border-line/50 text-ink-0">
                      <td className="py-1 pr-3">{s.node}</td>
                      <td className="py-1 pr-3">{s.role || "—"}</td>
                      <td className="py-1 pr-3">{fmt(s.utilization_avg_pct)}/{fmt(s.utilization_peak_pct)}</td>
                      <td className="py-1 pr-3">{fmt(s.memory_avg_gib, 2)}/{fmt(s.memory_peak_gib, 2)}</td>
                      <td className="py-1 pr-3">{fmt(s.sm_active_avg_pct)}</td>
                      <td className="py-1 pr-3">{fmt(s.tensor_active_avg_pct)}</td>
                      <td className="py-1 pr-3">{fmt(s.dram_active_avg_pct)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="mt-3 font-mono text-[11px] text-ink-1">
                Group total GPU memory:{" "}
                <span className="text-ink-0">{fmt(metrics?.accelerator_memory_total_gib, 2)} GiB</span>
                <span className="text-ink-2"> (sum of per-node peaks) · </span>
                peak single node:{" "}
                <span className="text-ink-0">{fmt(metrics?.accelerator_memory_peak_gib, 2)} GiB</span>
              </div>
            </div>
          )}
        </section>

        {/* Export controls — parity with the standard report (PRD-41). */}
        <div className="pt-2 hairline no-print">
          <div className="flex items-center gap-3 pt-4">
            <PrintButton />
            <a href={getRunCSVUrl(run.id)} download className="btn">
              Export CSV
            </a>
            <a href={getExportManifestUrl(run.id)} download className="btn">
              Export K8s Manifest
            </a>
          </div>
          <div className="mt-2 font-mono text-[11px] text-ink-2">
            Print for sharing (PDF), CSV includes the per-node/per-role breakdown, or K8s manifest to redeploy this topology.
          </div>
        </div>
      </div>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-ink-2 text-[10.5px] uppercase tracking-mech">{label}</span>
      <span className="text-ink-0">{value}</span>
    </div>
  );
}
