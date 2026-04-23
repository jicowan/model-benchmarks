import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { getSuiteRun } from "../api";
import type { TestSuiteRun } from "../types";
import HeroBlock from "../components/HeroBlock";
import SuiteCharts from "../components/SuiteCharts";
import ScenarioTable from "../components/ScenarioTable";

function SectionHeader({ index, label }: { index: string; label: string }) {
  return (
    <div className="flex items-baseline gap-3 mb-3">
      <span className="font-mono text-[11px] tracking-widemech text-ink-2">
        [ {index} ]
      </span>
      <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">
        {label}
      </h2>
    </div>
  );
}

export default function SuiteResults() {
  const { id } = useParams<{ id: string }>();
  const [suiteRun, setSuiteRun] = useState<TestSuiteRun | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    getSuiteRun(id).then(setSuiteRun).catch((e) => setError(e.message));
  }, [id]);

  useEffect(() => {
    if (!suiteRun || suiteRun.status === "completed" || suiteRun.status === "failed") return;
    const interval = setInterval(() => {
      getSuiteRun(suiteRun.id).then((updated) => {
        setSuiteRun(updated);
        if (updated.status === "completed" || updated.status === "failed") {
          clearInterval(interval);
        }
      });
    }, 5000);
    return () => clearInterval(interval);
  }, [suiteRun]);

  if (error) {
    return (
      <div className="p-6">
        <p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">
          {error}
        </p>
      </div>
    );
  }
  if (!suiteRun) {
    return <div className="p-6 caption">LOADING…</div>;
  }

  const results = suiteRun.results ?? [];
  const definitions = suiteRun.scenario_definitions ?? [];
  const progress = suiteRun.progress;
  const progressPct = progress ? Math.round((progress.completed / progress.total) * 100) : 0;

  const isNeuron = (suiteRun.accelerator_type ?? "").toLowerCase() === "neuron";
  const acceleratorNoun = isNeuron ? "chip" : "GPU";

  const instanceSummary = suiteRun.instance_type_name
    ? `${suiteRun.instance_type_name} · ${suiteRun.accelerator_count ?? "?"}×${suiteRun.accelerator_name ?? "?"} · ${suiteRun.accelerator_memory_gib ?? "?"} GiB`
    : "—";

  // Hero metrics
  const completed = results.filter((r) => r.status === "completed");
  const scenariosDone = `${completed.length} / ${results.length}`;
  const peakThroughput = completed.length
    ? Math.max(...completed.map((r) => r.throughput_tps ?? 0))
    : undefined;
  const bestTTFT = completed.length
    ? Math.min(...completed.filter((r) => r.ttft_p99_ms !== undefined).map((r) => r.ttft_p99_ms!))
    : undefined;
  // Weighted success rate across all scenarios
  let totalOk = 0;
  let totalFail = 0;
  for (const r of completed) {
    totalOk += r.successful_requests ?? 0;
    totalFail += r.failed_requests ?? 0;
  }
  const suiteSuccessRate = totalOk + totalFail > 0 ? (totalOk / (totalOk + totalFail)) * 100 : undefined;

  const statusBadge = (
    <span className="flex items-center gap-2 font-mono text-[11px] tracking-widemech uppercase">
      <span className={`status-dot status-${suiteRun.status === "pending" ? "pending" : suiteRun.status}`} />
      {suiteRun.status}
    </span>
  );

  return (
    <>
      <div className="h-14 border-b border-line flex items-center justify-between px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <a href="/runs" className="text-ink-1 hover:text-ink-0">runs</a>
          <span className="text-ink-2">/</span>
          <span className="text-info">suite</span>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">{suiteRun.id.slice(0, 8)}</span>
        </div>
      </div>

      <div className="p-6 max-w-6xl mx-auto animate-enter">
        <HeroBlock
          eyebrow="[ SUITE ]"
          heading={suiteRun.model_hf_id || "(model)"}
          subheading={`${suiteRun.suite_id} · ${instanceSummary}`}
          meta={`${suiteRun.id.slice(0, 8)} · ${suiteRun.id}`}
          statusBadge={statusBadge}
          metrics={
            completed.length > 0
              ? [
                  { label: "Scenarios", value: scenariosDone },
                  { label: "Peak Throughput", value: peakThroughput, unit: "tok/s", precision: 0 },
                  { label: "Best TTFT p99", value: bestTTFT, unit: "ms", precision: 0 },
                  {
                    label: "Success Rate",
                    value: suiteSuccessRate,
                    unit: "%",
                    precision: 1,
                    accent: suiteSuccessRate !== undefined && suiteSuccessRate < 99 ? "warn" : "signal",
                  },
                  // PRD-35: persisted suite cost (one shared EC2 node lifetime).
                  {
                    label: "Total Cost",
                    value: suiteRun.total_cost_usd ?? undefined,
                    unit: "$",
                    precision: 2,
                  },
                ]
              : undefined
          }
        />

        {/* Progress — only while running */}
        {progress && (suiteRun.status === "pending" || suiteRun.status === "running") && (
          <div className="panel p-4 mb-8">
            <div className="flex justify-between font-mono text-[12px] text-ink-1 mb-2">
              <span>
                {progress.completed} of {progress.total} scenarios complete
                {suiteRun.status === "running" && progress.scenarios && (
                  <>
                    {" · "}
                    running: {progress.scenarios.find((s) => s.status === "running")?.id ?? "—"}
                  </>
                )}
              </span>
              <span className="tabular">{progressPct}%</span>
            </div>
            <div className="w-full bg-surface-2 h-1.5 border border-line">
              <div
                className="bg-signal h-1.5 transition-all"
                style={{ width: `${progressPct}%` }}
              />
            </div>
          </div>
        )}

        {/* A. Scaling curves — only when ≥ 2 scenarios completed */}
        {completed.length >= 2 && (
          <section className="mb-8">
            <SectionHeader index="A" label="Scaling curves" />
            <SuiteCharts results={results} definitions={definitions} />
          </section>
        )}

        {/* B. Scenarios table */}
        <section className="mb-8">
          <SectionHeader index="B" label="Scenarios" />
          <ScenarioTable
            results={results}
            definitions={definitions}
            acceleratorNoun={acceleratorNoun}
            acceleratorCount={suiteRun.accelerator_count}
          />
        </section>
      </div>
    </>
  );
}
