import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { getSuiteRun } from "../api";
import type { TestSuiteRun, ScenarioResult } from "../types";
import SuiteCharts from "../components/SuiteCharts";

function formatMetric(value: number | undefined, precision = 1): string {
  if (value === undefined || value === null) return "-";
  return value.toFixed(precision);
}

function ScenarioMetrics({ result }: { result: ScenarioResult }) {
  if (result.status !== "completed") return null;

  return (
    <div className="mt-3 grid grid-cols-2 md:grid-cols-4 lg:grid-cols-8 gap-3 text-sm">
      <div className="panel p-2">
        <div className="eyebrow">TTFT p50</div>
        <div className="font-medium">{formatMetric(result.ttft_p50_ms)} ms</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">TTFT p99</div>
        <div className="font-medium">{formatMetric(result.ttft_p99_ms)} ms</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">E2E p50</div>
        <div className="font-medium">{formatMetric(result.e2e_latency_p50_ms)} ms</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">ITL p50</div>
        <div className="font-medium">{formatMetric(result.itl_p50_ms)} ms</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">Throughput</div>
        <div className="font-medium">{formatMetric(result.throughput_tps, 0)} tok/s</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">Requests</div>
        <div className="font-medium">
          {result.successful_requests ?? 0} / {(result.successful_requests ?? 0) + (result.failed_requests ?? 0)}
        </div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">GPU Util</div>
        <div className="font-medium">{formatMetric(result.accelerator_utilization_pct, 0)}%</div>
      </div>
      <div className="panel p-2">
        <div className="eyebrow">Peak Memory</div>
        <div className="font-medium">{formatMetric(result.accelerator_memory_peak_gib)} GiB</div>
      </div>
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
    return <div className="p-6"><p className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">{error}</p></div>;
  }
  if (!suiteRun) {
    return <div className="p-6 caption">LOADING…</div>;
  }

  const progress = suiteRun.progress;
  const progressPct = progress ? Math.round((progress.completed / progress.total) * 100) : 0;

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
        <span className="flex items-center font-mono text-[11px] tracking-mech uppercase">
          <span className={`status-dot status-${suiteRun.status === "pending" ? "pending" : suiteRun.status}`} />
          {suiteRun.status}
        </span>
      </div>
      <div className="p-6 max-w-6xl mx-auto animate-enter">
      <div className="mb-6 flex items-baseline gap-4">
        <div className="eyebrow">[ SUITE ]</div>
        <div className="font-mono text-[18px] text-ink-0">{suiteRun.id.slice(0, 8)}</div>
        <div className="caption">{suiteRun.suite_id}</div>
      </div>

      {/* Suite Info */}
      <div className="panel p-4 mb-6">
        <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-3 pb-2 border-b border-line">Suite Information</h2>
        <dl className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <dt className="eyebrow">SUITE ID</dt>
            <dd className="font-medium">{suiteRun.suite_id}</dd>
          </div>
          <div>
            <dt className="eyebrow">CREATED</dt>
            <dd className="font-medium">
              {new Date(suiteRun.created_at).toLocaleString()}
            </dd>
          </div>
          {suiteRun.started_at && (
            <div>
              <dt className="eyebrow">STARTED</dt>
              <dd className="font-medium">
                {new Date(suiteRun.started_at).toLocaleString()}
              </dd>
            </div>
          )}
          {suiteRun.completed_at && (
            <div>
              <dt className="eyebrow">COMPLETED</dt>
              <dd className="font-medium">
                {new Date(suiteRun.completed_at).toLocaleString()}
              </dd>
            </div>
          )}
        </dl>
      </div>

      {/* Progress */}
      {progress && (
        <div className="panel p-4 mb-6">
          <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-3 pb-2 border-b border-line">Progress</h2>
          <div className="mb-2">
            <div className="flex justify-between text-sm mb-1">
              <span>{progress.completed} of {progress.total} scenarios complete</span>
              <span>{progressPct}%</span>
            </div>
            <div className="w-full bg-surface-2 h-1.5 border border-line">
              <div
                className="bg-signal h-1.5 transition-all"
                style={{ width: `${progressPct}%` }}
              />
            </div>
          </div>
        </div>
      )}

      {/* Scenario Results */}
      <div className="panel p-4">
        <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0 mb-3 pb-2 border-b border-line">Scenario Results</h2>
        <div className="space-y-4">
          {suiteRun.results?.map((result) => (
            <div
              key={result.id}
              className="p-3 panel-inset"
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <span className="flex items-center font-mono text-[11px] tracking-mech uppercase">
                    <span className={`status-dot status-${result.status === "pending" ? "pending" : result.status === "skipped" ? "deleting" : result.status}`} />
                    {result.status}
                  </span>
                  <span className="font-mono text-[12.5px] text-ink-0">{result.scenario_id}</span>
                </div>
                <div className="flex items-center gap-4 meta">
                  {result.started_at && (
                    <span>Started: {new Date(result.started_at).toLocaleTimeString()}</span>
                  )}
                  {result.completed_at && (
                    <span>Completed: {new Date(result.completed_at).toLocaleTimeString()}</span>
                  )}
                </div>
              </div>
              {result.error_message && (
                <div className="mt-2 font-mono text-[12px] text-danger">{result.error_message}</div>
              )}
              <ScenarioMetrics result={result} />
            </div>
          ))}
        </div>
      </div>

      {/* Performance Charts */}
      {suiteRun.results && suiteRun.scenario_definitions && (
        <SuiteCharts
          results={suiteRun.results}
          definitions={suiteRun.scenario_definitions}
        />
      )}
      </div>
    </>
  );
}
