import { useState } from "react";
import type { ScenarioResult, ScenarioDefinition } from "../types";
import { winnerIndex } from "../lib/compare";
import ScenarioDetail from "./ScenarioDetail";

interface Props {
  results: ScenarioResult[];
  definitions: ScenarioDefinition[];
  acceleratorNoun: string;
  acceleratorCount?: number;
}

function num(v: number | undefined | null, precision = 1): string {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return v.toFixed(precision);
}

function successRate(r: ScenarioResult): number | undefined {
  const ok = r.successful_requests ?? 0;
  const total = ok + (r.failed_requests ?? 0);
  return total > 0 ? (ok / total) * 100 : undefined;
}

export default function ScenarioTable({
  results,
  definitions,
  acceleratorNoun,
  acceleratorCount,
}: Props) {
  const [expanded, setExpanded] = useState<string | null>(null);

  // Build winner maps across completed scenarios only
  const completed = results.filter((r) => r.status === "completed");
  const winners = {
    ttft50: winnerIndex(completed.map((r) => r.ttft_p50_ms), "min"),
    ttft99: winnerIndex(completed.map((r) => r.ttft_p99_ms), "min"),
    e2e50: winnerIndex(completed.map((r) => r.e2e_latency_p50_ms), "min"),
    itl50: winnerIndex(completed.map((r) => r.itl_p50_ms), "min"),
    tpot99: winnerIndex(completed.map((r) => r.tpot_p99_ms), "min"),
    throughput: winnerIndex(completed.map((r) => r.throughput_tps), "max"),
    success: winnerIndex(completed.map((r) => successRate(r)), "max"),
    gpu: null, // utilization — no winner (ambiguous)
  };

  // Map completed-index back to full-result-index for winner highlighting
  const completedIdxFor: Record<string, number> = {};
  completed.forEach((r, ci) => (completedIdxFor[r.id] = ci));

  const isWin = (winner: number | null, resultId: string) => {
    if (winner === null) return false;
    return completedIdxFor[resultId] === winner;
  };

  function definitionFor(scenarioId: string): ScenarioDefinition | undefined {
    return definitions.find((d) => d.id === scenarioId);
  }

  function cell(isWinner: boolean, content: React.ReactNode) {
    return (
      <td
        className={`py-2 px-3 border-b border-line/60 text-right font-mono text-[12.5px] tabular ${
          isWinner ? "bg-signal/10 text-signal" : "text-ink-0"
        }`}
      >
        {isWinner && <span className="mr-1">▸</span>}
        {content}
      </td>
    );
  }

  return (
    <div className="panel overflow-x-auto">
      <table className="min-w-full">
        <thead>
          <tr>
            <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1 w-8"></th>
            <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">Scenario</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">Target QPS</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">TTFT p50</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">TTFT p99</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">E2E p50</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">ITL p50</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">TPOT p99</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">Throughput</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">Success %</th>
            <th className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1">{acceleratorNoun} Busy</th>
          </tr>
        </thead>
        <tbody>
          {results.map((r) => {
            const def = definitionFor(r.scenario_id);
            const open = expanded === r.id;
            const canExpand = r.status === "completed" || r.status === "failed" || r.status === "skipped";
            return (
              <>
                <tr
                  key={r.id}
                  onClick={() => canExpand && setExpanded(open ? null : r.id)}
                  className={`${canExpand ? "cursor-pointer hover:bg-surface-2/50" : "opacity-60"} transition-colors`}
                >
                  <td className="py-2 px-3 border-b border-line/60 text-ink-2 font-mono text-[12px]">
                    {open ? "▾" : "▸"}
                  </td>
                  <td className="py-2 px-3 border-b border-line/60 font-mono text-[12.5px]">
                    <span className="flex items-center gap-2">
                      <span className={`status-dot status-${r.status === "pending" ? "pending" : r.status === "skipped" ? "deleting" : r.status}`} />
                      <span className="text-ink-0">{def?.name ?? r.scenario_id}</span>
                    </span>
                  </td>
                  <td className="py-2 px-3 border-b border-line/60 text-right text-ink-1 font-mono text-[12.5px] tabular">
                    {def?.target_qps ?? "—"}
                  </td>
                  {cell(isWin(winners.ttft50, r.id), `${num(r.ttft_p50_ms, 0)} ms`)}
                  {cell(isWin(winners.ttft99, r.id), `${num(r.ttft_p99_ms, 0)} ms`)}
                  {cell(isWin(winners.e2e50, r.id), `${num(r.e2e_latency_p50_ms, 0)} ms`)}
                  {cell(isWin(winners.itl50, r.id), `${num(r.itl_p50_ms, 1)} ms`)}
                  {cell(isWin(winners.tpot99, r.id), `${num(r.tpot_p99_ms, 1)} ms`)}
                  {cell(isWin(winners.throughput, r.id), `${num(r.throughput_tps, 0)} tok/s`)}
                  {cell(isWin(winners.success, r.id), `${num(successRate(r), 1)}%`)}
                  <td className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px] tabular">
                    {num(r.accelerator_utilization_avg_pct ?? r.accelerator_utilization_pct, 0)}%
                  </td>
                </tr>
                {open && canExpand && (
                  <tr key={`${r.id}-detail`}>
                    <td colSpan={11} className="border-b border-line bg-surface-0 p-4">
                      <ScenarioDetail
                        result={r}
                        acceleratorNoun={acceleratorNoun}
                        acceleratorCount={acceleratorCount}
                      />
                    </td>
                  </tr>
                )}
              </>
            );
          })}
        </tbody>
      </table>
      <p className="px-3 py-2 caption text-ink-2">
        Winners reflect raw values across scenarios with different target QPS.
      </p>
    </div>
  );
}
