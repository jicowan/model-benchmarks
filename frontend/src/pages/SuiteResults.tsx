import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { getSuiteRun } from "../api";
import type { TestSuiteRun, ScenarioResult } from "../types";

function formatMetric(value: number | undefined, precision = 1): string {
  if (value === undefined || value === null) return "-";
  return value.toFixed(precision);
}

function ScenarioMetrics({ result }: { result: ScenarioResult }) {
  if (result.status !== "completed") return null;

  return (
    <div className="mt-3 grid grid-cols-2 md:grid-cols-4 lg:grid-cols-8 gap-3 text-sm">
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">TTFT p50</div>
        <div className="font-medium">{formatMetric(result.ttft_p50_ms)} ms</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">TTFT p99</div>
        <div className="font-medium">{formatMetric(result.ttft_p99_ms)} ms</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">E2E p50</div>
        <div className="font-medium">{formatMetric(result.e2e_latency_p50_ms)} ms</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">ITL p50</div>
        <div className="font-medium">{formatMetric(result.itl_p50_ms)} ms</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">Throughput</div>
        <div className="font-medium">{formatMetric(result.throughput_tps, 0)} tok/s</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">Requests</div>
        <div className="font-medium">
          {result.successful_requests ?? 0} / {(result.successful_requests ?? 0) + (result.failed_requests ?? 0)}
        </div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">GPU Util</div>
        <div className="font-medium">{formatMetric(result.accelerator_utilization_pct, 0)}%</div>
      </div>
      <div className="bg-white p-2 rounded border">
        <div className="text-gray-500 text-xs">Peak Memory</div>
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
    return <p className="text-red-600">{error}</p>;
  }
  if (!suiteRun) {
    return <p className="text-gray-500">Loading...</p>;
  }

  const statusColor: Record<string, string> = {
    pending: "bg-yellow-100 text-yellow-800",
    running: "bg-blue-100 text-blue-800",
    completed: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
    skipped: "bg-gray-100 text-gray-800",
  };

  const progress = suiteRun.progress;
  const progressPct = progress ? Math.round((progress.completed / progress.total) * 100) : 0;

  return (
    <div>
      <div className="flex items-center gap-4 mb-6">
        <h1 className="text-2xl font-bold">Test Suite Run</h1>
        <span
          className={`px-2 py-1 rounded-full text-xs font-medium ${
            statusColor[suiteRun.status] || "bg-gray-100 text-gray-800"
          }`}
        >
          {suiteRun.status}
        </span>
      </div>

      {/* Suite Info */}
      <div className="bg-white border border-gray-200 rounded-lg p-4 mb-6">
        <h2 className="text-lg font-semibold mb-3">Suite Information</h2>
        <dl className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <dt className="text-gray-500">Suite ID</dt>
            <dd className="font-medium">{suiteRun.suite_id}</dd>
          </div>
          <div>
            <dt className="text-gray-500">Created</dt>
            <dd className="font-medium">
              {new Date(suiteRun.created_at).toLocaleString()}
            </dd>
          </div>
          {suiteRun.started_at && (
            <div>
              <dt className="text-gray-500">Started</dt>
              <dd className="font-medium">
                {new Date(suiteRun.started_at).toLocaleString()}
              </dd>
            </div>
          )}
          {suiteRun.completed_at && (
            <div>
              <dt className="text-gray-500">Completed</dt>
              <dd className="font-medium">
                {new Date(suiteRun.completed_at).toLocaleString()}
              </dd>
            </div>
          )}
        </dl>
      </div>

      {/* Progress */}
      {progress && (
        <div className="bg-white border border-gray-200 rounded-lg p-4 mb-6">
          <h2 className="text-lg font-semibold mb-3">Progress</h2>
          <div className="mb-2">
            <div className="flex justify-between text-sm mb-1">
              <span>{progress.completed} of {progress.total} scenarios complete</span>
              <span>{progressPct}%</span>
            </div>
            <div className="w-full bg-gray-200 rounded-full h-2.5">
              <div
                className="bg-blue-600 h-2.5 rounded-full transition-all"
                style={{ width: `${progressPct}%` }}
              />
            </div>
          </div>
        </div>
      )}

      {/* Scenario Results */}
      <div className="bg-white border border-gray-200 rounded-lg p-4">
        <h2 className="text-lg font-semibold mb-3">Scenario Results</h2>
        <div className="space-y-4">
          {suiteRun.results?.map((result) => (
            <div
              key={result.id}
              className="p-3 bg-gray-50 rounded-lg"
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <span
                    className={`px-2 py-0.5 rounded text-xs font-medium ${
                      statusColor[result.status] || "bg-gray-100 text-gray-800"
                    }`}
                  >
                    {result.status}
                  </span>
                  <span className="font-medium">{result.scenario_id}</span>
                </div>
                <div className="flex items-center gap-4 text-sm text-gray-500">
                  {result.started_at && (
                    <span>Started: {new Date(result.started_at).toLocaleTimeString()}</span>
                  )}
                  {result.completed_at && (
                    <span>Completed: {new Date(result.completed_at).toLocaleTimeString()}</span>
                  )}
                </div>
              </div>
              {result.error_message && (
                <div className="mt-2 text-sm text-red-600">{result.error_message}</div>
              )}
              <ScenarioMetrics result={result} />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
