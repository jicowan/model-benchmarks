import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { getRun, getRunDetail, getRunCSVUrl, getExportManifestUrl } from "../api";
import type {
  BenchmarkRun,
  BenchmarkMetrics,
  InstanceType,
  PricingRow,
  PricingTier,
  ShardMetric,
} from "../types";
import MetricCard from "../components/MetricCard";
import LatencyDistribution from "../components/LatencyDistribution";
import HeroBlock from "../components/HeroBlock";
import ConfigPanel from "../components/ConfigPanel";
import PricingToggle from "../components/PricingToggle";
import PrintButton from "../components/PrintButton";
import { hourlyRate, costPerRequest, costPer1MTokens, totalSpent } from "../lib/cost";

function SectionHeader({ index, label }: { index: string; label: string }) {
  return (
    <div className="flex items-baseline gap-3 mb-3">
      <span className="font-mono text-[11px] tracking-widemech text-ink-2">[ {index} ]</span>
      <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">{label}</h2>
    </div>
  );
}

// PRD-59: the complete report for a distributed / disaggregated run. Mirrors the
// standard single-node report's layout (HeroBlock + lettered card sections +
// latency distribution + pricing toggle) and ADDS the distributed-specific
// pieces: topology, the N-node cost breakdown, and per-node/per-role DCGM
// telemetry. Distributed runs redirect here from /results/:id (ResultDetail).
export default function DistributedReport() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<BenchmarkRun | null>(null);
  const [metrics, setMetrics] = useState<BenchmarkMetrics | null>(null);
  const [instanceType, setInstanceType] = useState<InstanceType | null>(null);
  const [pricingRow, setPricingRow] = useState<PricingRow | null>(null);
  const [pricingTier, setPricingTier] = useState<PricingTier>("on_demand");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    getRunDetail(id, ["metrics", "instance", "pricing"])
      .then((d) => {
        setRun(d);
        setMetrics(d.metrics ?? null);
        setInstanceType(d.instance ?? null);
        setPricingRow(d.pricing ?? null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Failed to load run."));
  }, [id]);

  // Poll while the run is in flight, same as ResultDetail.
  useEffect(() => {
    if (!run || run.status === "completed" || run.status === "failed") return;
    const interval = setInterval(() => {
      getRun(run.id).then((updated) => {
        setRun(updated);
        if (updated.status === "completed") {
          getRunDetail(updated.id, ["metrics", "instance", "pricing"]).then((d) => {
            setRun(d);
            setMetrics(d.metrics ?? null);
            setInstanceType(d.instance ?? null);
            setPricingRow(d.pricing ?? null);
          });
          clearInterval(interval);
        }
        if (updated.status === "failed") clearInterval(interval);
      });
    }, 5000);
    return () => clearInterval(interval);
  }, [run]);

  if (error) {
    return (
      <div className="p-6">
        <p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">{error}</p>
      </div>
    );
  }
  if (!run) return <div className="p-6 caption">LOADING…</div>;

  const disaggregated = run.deployment_mode === "disaggregated";
  const nodeCount = run.node_count ?? 1;

  const succeeded = metrics?.successful_requests ?? 0;
  const failed = metrics?.failed_requests ?? 0;
  const totalReqs = succeeded + failed;
  const successRate = totalReqs > 0 ? (succeeded / totalReqs) * 100 : undefined;

  const aggregateTps = metrics?.throughput_aggregate_tps ?? metrics?.generation_throughput_tps;

  // Cost is the GROUP cost: per-node hourly × node_count (PRD-57 computeRunCost
  // scaled the persisted total the same way; here we show the derivation).
  const perNodeHourly = hourlyRate(pricingRow ?? undefined, pricingTier);
  const groupHourly = perNodeHourly != null ? perNodeHourly * nodeCount : null;
  const perRequestCost = costPerRequest(groupHourly, metrics?.requests_per_second);
  const per1MCost = costPer1MTokens(groupHourly, aggregateTps);
  const spent = run.total_cost_usd ?? totalSpent(groupHourly, metrics?.total_duration_seconds);

  const shards: ShardMetric[] = metrics?.shards ?? [];

  const instanceSummary = instanceType
    ? `${instanceType.name} · ${instanceType.accelerator_count}×${instanceType.accelerator_name} · ${nodeCount} nodes`
    : `${nodeCount} nodes`;

  // PRD-63: xPyDzB, dropping zero-count roles. A both-only run reads "2B".
  const disaggTopoStr = [
    run.prefill_replicas ? `${run.prefill_replicas}P` : "",
    run.decode_replicas ? `${run.decode_replicas}D` : "",
    run.both_replicas ? `${run.both_replicas}B` : "",
  ].filter(Boolean).join("") || "?";
  const disaggTopoDetail = [
    run.prefill_replicas ? `prefill TP=${run.prefill_tp ?? "?"}` : "",
    run.decode_replicas ? `decode TP=${run.decode_tp ?? "?"}` : "",
    run.both_replicas ? `both TP=${run.both_tp ?? "?"}` : "",
  ].filter(Boolean).join(" · ");
  const topologyValue = disaggregated
    ? `${disaggTopoStr} · ${disaggTopoDetail} · ${nodeCount} nodes`
    : `${nodeCount} nodes · TP=${run.tensor_parallel_degree} · PP=${run.pipeline_parallel_degree ?? "?"}`;

  const statusBadge = (
    <span className="flex items-center gap-2 font-mono text-[11px] tracking-widemech uppercase">
      <span className={`status-dot status-${run.status === "pending" ? "pending" : run.status}`} />
      {run.status}
    </span>
  );
  const runningCaption =
    run.status === "running" || run.status === "pending" ? "RESULTS WILL APPEAR WHEN COMPLETE" : null;

  const roleLabel = (r?: string) => (r ? r : "node");

  return (
    <>
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <a href="/runs" className="text-ink-1 hover:text-ink-0">runs</a>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">{run.id.slice(0, 8)}</span>
          <span className="text-ink-2">· {disaggregated ? "disaggregated" : "distributed"}</span>
        </div>
      </div>

      <div className="sticky top-14 z-10 bg-surface-0 border-b border-line no-print">
        <div className="px-6 py-3 flex items-center gap-3">
          <div className="flex-1" />
          <span className="eyebrow">PRICING</span>
          <PricingToggle value={pricingTier} onChange={setPricingTier} />
        </div>
      </div>

      <div className="p-6 max-w-6xl mx-auto animate-enter">
        <HeroBlock
          eyebrow={disaggregated ? "[ DISAGGREGATED RUN ]" : "[ DISTRIBUTED RUN ]"}
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
                  { label: "Cost / 1M tok", value: per1MCost ?? undefined, unit: "$", precision: 2 },
                  // PRD-62: for disaggregated runs, surface the marquee signal —
                  // how often PD actually engaged — in the hero; else total cost.
                  disaggregated && metrics.disagg_engaged_rate_pct != null
                    ? { label: "PD Engaged", value: metrics.disagg_engaged_rate_pct, unit: "%", precision: 0 }
                    : { label: "Total Cost", value: run.total_cost_usd ?? undefined, unit: "$", precision: 2 },
                ]
              : undefined
          }
        />

        {runningCaption && <p className="mb-6 meta text-info">{runningCaption}</p>}

        {run.status === "failed" && run.error_message && (
          <div className="border border-danger/40 bg-danger/5 p-4 mb-6">
            <p className="eyebrow text-danger mb-1.5">[ RUN FAILED ]</p>
            <p className="font-mono text-[12.5px] text-danger">{run.error_message}</p>
          </div>
        )}

        <ConfigPanel
          headline={[
            { label: "Topology", value: topologyValue },
            { label: "Network Fabric", value: run.network_mode ?? null },
            { label: "Framework", value: `${run.framework ?? ""} ${run.framework_version ?? ""}`.trim() || null },
            { label: "Max Model Len", value: run.max_model_len ?? null },
          ]}
          details={[
            {
              label: "Deployment",
              value: disaggregated ? "disaggregated (prefill/decode, llm-d)" : "distributed (multi-node llm-d)",
            },
            { label: "Node Count", value: nodeCount },
            ...(disaggregated
              ? [
                  // PRD-63: only show a role row when that pool has replicas.
                  ...(run.prefill_replicas
                    ? [{ label: "Prefill", value: `${run.prefill_replicas} × TP=${run.prefill_tp ?? "?"}` }]
                    : []),
                  ...(run.decode_replicas
                    ? [{ label: "Decode", value: `${run.decode_replicas} × TP=${run.decode_tp ?? "?"}` }]
                    : []),
                  ...(run.both_replicas
                    ? [{ label: "Both (co-located)", value: `${run.both_replicas} × TP=${run.both_tp ?? "?"}` }]
                    : []),
                  { label: "KV Connector", value: run.kv_connector ?? null },
                  { label: "KV Transfer", value: run.kv_transfer_backend ?? null },
                  // PRD-64/63: per-role scheduler override (only shown when set).
                  ...(run.prefill_max_num_batched_tokens || run.decode_max_num_batched_tokens || run.both_max_num_batched_tokens
                    ? [{
                        label: "Max Batched Tokens (P/D/B)",
                        value: `prefill=${run.prefill_max_num_batched_tokens ?? "shared"} · decode=${run.decode_max_num_batched_tokens ?? "shared"} · both=${run.both_max_num_batched_tokens ?? "shared"}`,
                      }]
                    : []),
                  // PRD-61: effective EPP routing config (NULL → the shipped
                  // default, labeled, so the run is self-describing for Compare).
                  {
                    label: "Routing / EPP",
                    value: `nonCachedTokens=${run.pd_noncached_tokens ?? "16 (default)"} · prefix/queue weight=${run.pd_prefix_cache_weight ?? 2}/${run.pd_queue_scorer_weight ?? 1} · prefixBlocks=${run.pd_max_prefix_blocks ?? 256} · lru=${run.pd_lru_capacity_per_server ?? 31250} · decider=${run.pd_decider_strategy ?? "threshold"}`,
                  },
                ]
              : [
                  { label: "Tensor Parallel", value: run.tensor_parallel_degree },
                  { label: "Pipeline Parallel", value: run.pipeline_parallel_degree ?? null },
                ]),
            { label: "Concurrency", value: run.concurrency },
            { label: "Dataset", value: run.dataset_name },
            { label: "Scenario", value: run.scenario_id ?? null },
            { label: "Input Seq", value: run.input_sequence_length },
            { label: "Output Seq", value: run.output_sequence_length },
            { label: "Quantization", value: run.quantization ?? "default" },
          ]}
        />

        {metrics && (
          <>
            {/* A. LATENCY */}
            <section className="mb-8">
              <SectionHeader index="A" label="Latency distribution" />
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
                <LatencyDistribution label="TTFT" p50={metrics.ttft_p50_ms} p90={metrics.ttft_p90_ms} p95={metrics.ttft_p95_ms} p99={metrics.ttft_p99_ms} />
                <LatencyDistribution label="E2E" p50={metrics.e2e_latency_p50_ms} p90={metrics.e2e_latency_p90_ms} p95={metrics.e2e_latency_p95_ms} p99={metrics.e2e_latency_p99_ms} />
                <LatencyDistribution label="ITL" p50={metrics.itl_p50_ms} p90={metrics.itl_p90_ms} p95={metrics.itl_p95_ms} p99={metrics.itl_p99_ms} />
                <LatencyDistribution label="TPOT" p50={metrics.tpot_p50_ms} p90={metrics.tpot_p90_ms} p99={metrics.tpot_p99_ms} />
              </div>
            </section>

            {/* B. THROUGHPUT */}
            <section className="mb-8">
              <SectionHeader index="B" label="Throughput" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Aggregate" value={aggregateTps} unit="tok/s" precision={0} />
                <MetricCard label="Requests/sec" value={metrics.requests_per_second} unit="rps" precision={2} />
                <MetricCard label="Success Rate" value={successRate} unit="%" precision={1} />
                <MetricCard label="Prompt" value={metrics.prompt_throughput_tps} unit="tok/s" precision={0} />
                <MetricCard label="Generation" value={metrics.generation_throughput_tps} unit="tok/s" precision={0} />
                <MetricCard label="Avg Output" value={metrics.output_length_mean} unit="tokens" precision={0} />
                <MetricCard label="Duration" value={metrics.total_duration_seconds} unit="s" precision={0} />
              </div>
            </section>

            {/* C. HARDWARE — group roll-up */}
            <section className="mb-8">
              <SectionHeader index="C" label="Hardware utilization (group)" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="GPU Busy (avg)" value={metrics.accelerator_utilization_avg_pct ?? metrics.accelerator_utilization_pct} unit="%" precision={0} />
                <MetricCard label="SM Active (avg)" value={metrics.sm_active_avg_pct} unit="%" precision={0} />
                <MetricCard label="Tensor Active (avg)" value={metrics.tensor_active_avg_pct} unit="%" precision={0} />
                <MetricCard label="DRAM Active (avg)" value={metrics.dram_active_avg_pct} unit="%" precision={0} />
              </div>
            </section>

            {/* D. MEMORY — group */}
            <section className="mb-8">
              <SectionHeader index="D" label="Memory (group)" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Memory Total" value={metrics.accelerator_memory_total_gib} unit="GiB" precision={1} />
                <MetricCard label="Memory Peak (node)" value={metrics.accelerator_memory_peak_gib} unit="GiB" precision={1} />
                <MetricCard label="KV Cache (avg)" value={metrics.kv_cache_utilization_avg_pct} unit="%" precision={1} />
                <MetricCard label="Prefix Hit" value={metrics.prefix_cache_hit_rate} unit="%" precision={1} />
              </div>
              <p className="mt-2 caption">
                Memory Total sums each node&apos;s peak (honest group footprint); Memory Peak is the hottest single node.
              </p>
            </section>

            {/* E. PER-NODE / PER-ROLE — the distributed-specific view.
                Collapsible so the report stays scannable with many nodes;
                open by default (and <details open> prints expanded). */}
            <section className="mb-8">
              {shards.length === 0 ? (
                <>
                  <SectionHeader index="E" label="Per-node / per-role GPU telemetry" />
                  <p className="caption">No per-node breakdown recorded (GPU metrics may not have been collected).</p>
                </>
              ) : (
                <details open className="panel">
                  <summary className="cursor-pointer list-none px-4 py-3 flex items-baseline gap-3 select-none">
                    <span className="font-mono text-[11px] tracking-widemech text-ink-2">[ E ]</span>
                    <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">
                      Per-node / per-role GPU telemetry
                    </h2>
                    <span className="ml-auto font-mono text-[11px] text-ink-2">
                      {shards.length} {shards.length === 1 ? "shard" : "shards"}
                    </span>
                  </summary>
                  <div className="overflow-x-auto border-t border-line">
                    <table className="w-full font-mono text-[12px]">
                      <thead>
                        <tr className="text-ink-2 text-left border-b border-line">
                          <th className="py-2 px-3">Node</th>
                          <th className="py-2 px-3">Role</th>
                          <th className="py-2 px-3">Util avg/peak</th>
                          <th className="py-2 px-3">Mem avg/peak (GiB)</th>
                          <th className="py-2 px-3">SM %</th>
                          <th className="py-2 px-3">Tensor %</th>
                          <th className="py-2 px-3">DRAM %</th>
                        </tr>
                      </thead>
                      <tbody>
                        {shards.map((s, i) => (
                          <tr key={i} className="border-b border-line/40 text-ink-0">
                            <td className="py-2 px-3">{s.node}</td>
                            <td className="py-2 px-3 uppercase tracking-mech">{roleLabel(s.role)}</td>
                            <td className="py-2 px-3">{fmt(s.utilization_avg_pct, 0)}/{fmt(s.utilization_peak_pct, 0)}%</td>
                            <td className="py-2 px-3">{fmt(s.memory_avg_gib, 1)}/{fmt(s.memory_peak_gib, 1)}</td>
                            <td className="py-2 px-3">{fmt(s.sm_active_avg_pct, 0)}</td>
                            <td className="py-2 px-3">{fmt(s.tensor_active_avg_pct, 0)}</td>
                            <td className="py-2 px-3">{fmt(s.dram_active_avg_pct, 0)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </details>
              )}
            </section>

            {/* E2. DISAGGREGATION / KV TRANSFER (PRD-62) — disaggregated runs
                only, and only when the series were collected. The headline is
                the disagg-engaged rate: did PD actually fire, and how often. */}
            {disaggregated && hasDisaggMetrics(metrics) && (
              <section className="mb-8">
                <SectionHeader index="E2" label="Disaggregation / KV transfer" />
                <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                  <MetricCard label="PD Engaged" value={metrics.disagg_engaged_rate_pct} unit="%" precision={0} />
                  <MetricCard label="Prefill→Decode reqs" value={metrics.disagg_prefill_decode_count} unit="" precision={0} />
                  <MetricCard label="Decode-only reqs" value={metrics.disagg_decode_only_count} unit="" precision={0} />
                  <MetricCard label="KV Transfer (avg)" value={metrics.kv_transfer_time_avg_ms} unit="ms" precision={2} />
                  <MetricCard label="KV Bytes Moved" value={mib(metrics.kv_transfer_bytes_total)} unit="MiB" precision={1} />
                  <MetricCard label="KV Transfer Fails" value={metrics.kv_transfer_failures} unit="" precision={0} />
                  <MetricCard label="Prefill Time (srv)" value={metrics.prefill_time_server_avg_ms} unit="ms" precision={0} />
                  <MetricCard label="Decode Time (srv)" value={metrics.decode_time_server_avg_ms} unit="ms" precision={0} />
                  <MetricCard label="Ext. Prefix Hit" value={metrics.external_prefix_cache_hit_rate} unit="%" precision={1} />
                  <MetricCard label="Pool KV Util (avg)" value={metrics.pool_kv_cache_util_pct} unit="%" precision={0} />
                  <MetricCard label="Pool Queue (avg)" value={metrics.pool_queue_size_avg} unit="req" precision={1} />
                </div>
                <p className="mt-2 caption">
                  PD Engaged = share of requests the EPP routed prefill→decode (vs. served decode-only locally).
                  KV Transfer is the NIXL prefill→decode hand-off cost. Pool gauges are averaged over
                  serving scrapes (idle warmup/teardown excluded). Empty cells = metric not emitted
                  (e.g. NIXL &lt; 0.7.1 or EPP metrics unavailable).
                </p>
              </section>
            )}

            {/* F. REQUEST FLOW */}
            <section className="mb-8">
              <SectionHeader index="F" label="Request flow" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Successful" value={succeeded} unit="" precision={0} />
                <MetricCard label="Failed" value={failed} unit="" precision={0} />
                <MetricCard label="Queue Max" value={metrics.waiting_requests_max} unit="req" precision={0} />
                <MetricCard label="Running (max)" value={metrics.running_requests_max} unit="req" precision={0} />
                <MetricCard label="Preemptions" value={metrics.preemption_count} unit="" precision={0} />
              </div>
            </section>

            {/* G. COST — N-node */}
            <section className="mb-8">
              <SectionHeader index="G" label="Cost (N-node)" />
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <MetricCard label="Per-node Hourly" value={perNodeHourly ?? undefined} unit="$" precision={2} />
                <MetricCard label={`Group Hourly (×${nodeCount})`} value={groupHourly ?? undefined} unit="$" precision={2} />
                <MetricCard label="Per Request" value={perRequestCost ?? undefined} unit="$" precision={6} />
                <MetricCard label="Per 1M Tokens" value={per1MCost ?? undefined} unit="$" precision={2} />
                <MetricCard label="Total Spent" value={spent ?? undefined} unit="$" precision={2} />
              </div>
            </section>

            {/* Exports — parity with the standard report (PRD-41). */}
            <div className="mt-8 pt-6 hairline no-print">
              <div className="flex gap-4 flex-wrap">
                <PrintButton />
                <a href={getRunCSVUrl(run.id)} download className="btn">Export CSV</a>
                <a href={getExportManifestUrl(run.id)} download className="btn">Export K8s Manifest</a>
              </div>
              <p className="mt-2 caption">
                Print for sharing (PDF); CSV includes the per-node/per-role breakdown; K8s manifest redeploys this topology.
              </p>
            </div>
          </>
        )}
      </div>
    </>
  );
}

function fmt(n?: number | null, d = 1): string {
  return n == null ? "—" : n.toFixed(d);
}

// hasDisaggMetrics reports whether ANY PRD-62 disaggregation/KV/EPP field was
// collected, so the section is shown only when there's something to show.
function hasDisaggMetrics(m: BenchmarkMetrics): boolean {
  return [
    m.disagg_engaged_rate_pct,
    m.disagg_prefill_decode_count,
    m.disagg_decode_only_count,
    m.kv_transfer_time_avg_ms,
    m.kv_transfer_bytes_total,
    m.kv_transfer_failures,
    m.prefill_time_server_avg_ms,
    m.decode_time_server_avg_ms,
    m.external_prefix_cache_hit_rate,
    m.pool_kv_cache_util_pct,
    m.pool_queue_size_avg,
  ].some((v) => v != null);
}

// mib converts a byte count to MiB for display; undefined passes through.
function mib(bytes?: number): number | undefined {
  return bytes == null ? undefined : bytes / (1024 * 1024);
}
