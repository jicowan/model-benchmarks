import { useMemo, useState } from "react";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
  BarChart,
  Bar,
  ScatterChart,
  Scatter,
  ZAxis,
} from "recharts";
import type { ScenarioResult, ScenarioDefinition } from "../types";

interface SuiteChartsProps {
  results: ScenarioResult[];
  definitions: ScenarioDefinition[];
}

const COLORS = {
  ttft_p50: "#2563eb",
  ttft_p99: "#93c5fd",
  e2e_p50: "#dc2626",
  e2e_p99: "#fca5a5",
  itl_p50: "#059669",
  throughput: "#d97706",
};

export default function SuiteCharts({ results, definitions }: SuiteChartsProps) {
  const [expanded, setExpanded] = useState(true);

  // Merge scenario definitions with results and sort by QPS
  const chartData = useMemo(() => {
    const completedResults = results.filter((r) => r.status === "completed");

    return completedResults
      .map((r) => {
        const def = definitions.find((d) => d.id === r.scenario_id);
        return {
          name: def?.name ?? r.scenario_id,
          qps: def?.target_qps ?? 0,
          ttft_p50: r.ttft_p50_ms ?? 0,
          ttft_p99: r.ttft_p99_ms ?? 0,
          e2e_p50: r.e2e_latency_p50_ms ?? 0,
          e2e_p99: r.e2e_latency_p99_ms ?? 0,
          itl_p50: r.itl_p50_ms ?? 0,
          throughput: r.throughput_tps ?? 0,
          rps: r.requests_per_second ?? 0,
        };
      })
      .sort((a, b) => a.qps - b.qps);
  }, [results, definitions]);

  // Need at least 2 completed scenarios to show meaningful charts
  if (chartData.length < 2) {
    return null;
  }

  // Data for bar chart comparison
  const comparisonData = [
    {
      metric: "TTFT p50",
      ...Object.fromEntries(chartData.map((d) => [d.name, d.ttft_p50])),
    },
    {
      metric: "E2E p50",
      ...Object.fromEntries(chartData.map((d) => [d.name, d.e2e_p50])),
    },
    {
      metric: "ITL p50",
      ...Object.fromEntries(chartData.map((d) => [d.name, d.itl_p50])),
    },
  ];

  const scenarioNames = chartData.map((d) => d.name);
  const barColors = ["#2563eb", "#dc2626", "#059669", "#d97706", "#7c3aed"];

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 mt-6">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center justify-between w-full text-left"
      >
        <h2 className="text-lg font-semibold">Performance Charts</h2>
        <span className="text-gray-500">{expanded ? "▼" : "▶"}</span>
      </button>

      {expanded && (
        <div className="mt-4 grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* QPS vs Latency */}
          <div className="border border-gray-100 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-3">
              QPS vs Latency (ms)
            </h3>
            <ResponsiveContainer width="100%" height={280}>
              <LineChart data={chartData} margin={{ top: 5, right: 20, left: 10, bottom: 25 }}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis
                  dataKey="qps"
                  label={{ value: "Target QPS", position: "insideBottom", offset: -15 }}
                />
                <YAxis width={50} />
                <Tooltip
                  formatter={(value: number) => `${value.toFixed(1)} ms`}
                  labelFormatter={(qps) => `QPS: ${qps}`}
                />
                <Legend verticalAlign="top" height={36} />
                <Line
                  type="monotone"
                  dataKey="ttft_p50"
                  name="TTFT p50"
                  stroke={COLORS.ttft_p50}
                  strokeWidth={2}
                  dot={{ r: 4 }}
                />
                <Line
                  type="monotone"
                  dataKey="ttft_p99"
                  name="TTFT p99"
                  stroke={COLORS.ttft_p99}
                  strokeWidth={2}
                  dot={{ r: 4 }}
                />
                <Line
                  type="monotone"
                  dataKey="e2e_p50"
                  name="E2E p50"
                  stroke={COLORS.e2e_p50}
                  strokeWidth={2}
                  dot={{ r: 4 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </div>

          {/* QPS vs Throughput */}
          <div className="border border-gray-100 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-3">
              QPS vs Throughput (tok/s)
            </h3>
            <ResponsiveContainer width="100%" height={280}>
              <LineChart data={chartData} margin={{ top: 5, right: 20, left: 10, bottom: 25 }}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis
                  dataKey="qps"
                  label={{ value: "Target QPS", position: "insideBottom", offset: -15 }}
                />
                <YAxis width={50} />
                <Tooltip
                  formatter={(value: number) => `${value.toFixed(0)} tok/s`}
                  labelFormatter={(qps) => `QPS: ${qps}`}
                />
                <Legend verticalAlign="top" height={36} />
                <Line
                  type="monotone"
                  dataKey="throughput"
                  name="Output Throughput"
                  stroke={COLORS.throughput}
                  strokeWidth={2}
                  dot={{ r: 4 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </div>

          {/* Latency vs Throughput Scatter */}
          <div className="border border-gray-100 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-3">
              TTFT p50 (ms) vs Throughput (tok/s)
            </h3>
            <ResponsiveContainer width="100%" height={280}>
              <ScatterChart margin={{ top: 5, right: 20, left: 10, bottom: 25 }}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis
                  dataKey="throughput"
                  name="Throughput"
                  type="number"
                  label={{
                    value: "Throughput",
                    position: "insideBottom",
                    offset: -15,
                  }}
                />
                <YAxis
                  dataKey="ttft_p50"
                  name="TTFT p50"
                  type="number"
                  width={50}
                />
                <ZAxis dataKey="name" name="Scenario" />
                <Tooltip
                  cursor={{ strokeDasharray: "3 3" }}
                  formatter={(value: number, name: string) => {
                    if (name === "Throughput") return `${value.toFixed(0)} tok/s`;
                    if (name === "TTFT p50") return `${value.toFixed(1)} ms`;
                    return value;
                  }}
                />
                <Scatter
                  data={chartData}
                  fill={COLORS.ttft_p50}
                  name="Scenarios"
                />
              </ScatterChart>
            </ResponsiveContainer>
            <div className="mt-2 flex flex-wrap gap-2 text-xs text-gray-600">
              {chartData.map((d) => (
                <span key={d.name} className="flex items-center gap-1">
                  <span
                    className="w-2 h-2 rounded-full"
                    style={{ backgroundColor: COLORS.ttft_p50 }}
                  />
                  {d.name}
                </span>
              ))}
            </div>
          </div>

          {/* Scenario Comparison Bar Chart */}
          <div className="border border-gray-100 rounded-lg p-4">
            <h3 className="text-sm font-medium text-gray-700 mb-3">
              Scenario Comparison (ms)
            </h3>
            <ResponsiveContainer width="100%" height={280}>
              <BarChart data={comparisonData} margin={{ top: 5, right: 20, left: 10, bottom: 5 }}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="metric" />
                <YAxis width={50} />
                <Tooltip formatter={(value: number) => `${value.toFixed(1)} ms`} />
                <Legend verticalAlign="top" height={36} />
                {scenarioNames.map((name, i) => (
                  <Bar
                    key={name}
                    dataKey={name}
                    fill={barColors[i % barColors.length]}
                  />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}
    </div>
  );
}
