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
import {
  useChartTheme,
  seriesPalette,
  axisStyle,
  gridProps,
  ChartTooltip,
  ChartLegend,
} from "./ChartTheme";

interface SuiteChartsProps {
  results: ScenarioResult[];
  definitions: ScenarioDefinition[];
}

export default function SuiteCharts({ results, definitions }: SuiteChartsProps) {
  const [expanded, setExpanded] = useState(true);
  const theme = useChartTheme();
  const palette = seriesPalette();
  const axis = axisStyle(theme);

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

  if (chartData.length < 2) return null;

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

  // Per-series colors derived from the palette
  const seriesColors = {
    ttft_p50: palette[0],
    ttft_p99: "rgb(var(--signal) / 0.4)",
    e2e_p50: palette[1],
    throughput: palette[2],
  };

  return (
    <div className="panel mt-6">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center justify-between w-full text-left px-4 h-11 border-b border-line hover:bg-surface-2 transition-colors"
      >
        <div className="flex items-baseline gap-3">
          <span className="eyebrow">[ CHARTS ]</span>
          <h2 className="font-sans text-[14px] font-medium tracking-mech text-ink-0">
            Performance visualizations
          </h2>
        </div>
        <span className="text-ink-2 font-mono text-[11px]">{expanded ? "▼ HIDE" : "▶ SHOW"}</span>
      </button>

      {expanded && (
        <div className="p-4 grid grid-cols-1 lg:grid-cols-2 gap-0 border-l border-t border-line">
          {/* QPS vs Latency */}
          <ChartPanel title="QPS → LATENCY" unit="ms">
            <ResponsiveContainer width="100%" height={260}>
              <LineChart data={chartData} margin={{ top: 8, right: 16, left: 0, bottom: 16 }}>
                <CartesianGrid {...gridProps} stroke={theme.grid} />
                <XAxis
                  dataKey="qps"
                  tickLine={false}
                  axisLine={{ stroke: theme.grid }}
                  tick={axis}
                  label={{
                    value: "TARGET QPS",
                    position: "insideBottom",
                    offset: -6,
                    style: { ...axis, fill: theme.axis, fontSize: 9, letterSpacing: "0.08em" },
                  }}
                />
                <YAxis tickLine={false} axisLine={false} tick={axis} width={44} />
                <Tooltip content={<ChartTooltip unit="ms" />} cursor={{ stroke: theme.grid, strokeWidth: 1 }} />
                <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 4 }} />
                <Line
                  type="monotone"
                  dataKey="ttft_p50"
                  name="TTFT p50"
                  stroke={seriesColors.ttft_p50}
                  strokeWidth={1.5}
                  dot={{ r: 2.5, strokeWidth: 0, fill: seriesColors.ttft_p50 }}
                  activeDot={{ r: 4, strokeWidth: 0 }}
                />
                <Line
                  type="monotone"
                  dataKey="ttft_p99"
                  name="TTFT p99"
                  stroke={seriesColors.ttft_p99}
                  strokeWidth={1.5}
                  strokeDasharray="4 2"
                  dot={{ r: 2.5, strokeWidth: 0, fill: seriesColors.ttft_p99 }}
                  activeDot={{ r: 4, strokeWidth: 0 }}
                />
                <Line
                  type="monotone"
                  dataKey="e2e_p50"
                  name="E2E p50"
                  stroke={seriesColors.e2e_p50}
                  strokeWidth={1.5}
                  dot={{ r: 2.5, strokeWidth: 0, fill: seriesColors.e2e_p50 }}
                  activeDot={{ r: 4, strokeWidth: 0 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </ChartPanel>

          {/* QPS vs Throughput */}
          <ChartPanel title="QPS → THROUGHPUT" unit="tok/s">
            <ResponsiveContainer width="100%" height={260}>
              <LineChart data={chartData} margin={{ top: 8, right: 16, left: 0, bottom: 16 }}>
                <CartesianGrid {...gridProps} stroke={theme.grid} />
                <XAxis
                  dataKey="qps"
                  tickLine={false}
                  axisLine={{ stroke: theme.grid }}
                  tick={axis}
                  label={{
                    value: "TARGET QPS",
                    position: "insideBottom",
                    offset: -6,
                    style: { ...axis, fill: theme.axis, fontSize: 9, letterSpacing: "0.08em" },
                  }}
                />
                <YAxis tickLine={false} axisLine={false} tick={axis} width={44} />
                <Tooltip content={<ChartTooltip unit="tok/s" />} cursor={{ stroke: theme.grid, strokeWidth: 1 }} />
                <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 4 }} />
                <Line
                  type="monotone"
                  dataKey="throughput"
                  name="Output tok/s"
                  stroke={seriesColors.throughput}
                  strokeWidth={1.5}
                  dot={{ r: 2.5, strokeWidth: 0, fill: seriesColors.throughput }}
                  activeDot={{ r: 4, strokeWidth: 0 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </ChartPanel>

          {/* Latency vs Throughput Scatter */}
          <ChartPanel title="LATENCY ↔ THROUGHPUT" unit="">
            <ResponsiveContainer width="100%" height={260}>
              <ScatterChart margin={{ top: 8, right: 16, left: 0, bottom: 16 }}>
                <CartesianGrid {...gridProps} stroke={theme.grid} />
                <XAxis
                  dataKey="throughput"
                  name="Throughput"
                  type="number"
                  tickLine={false}
                  axisLine={{ stroke: theme.grid }}
                  tick={axis}
                  label={{
                    value: "THROUGHPUT (TOK/S)",
                    position: "insideBottom",
                    offset: -6,
                    style: { ...axis, fill: theme.axis, fontSize: 9, letterSpacing: "0.08em" },
                  }}
                />
                <YAxis
                  dataKey="ttft_p50"
                  name="TTFT p50"
                  type="number"
                  tickLine={false}
                  axisLine={false}
                  tick={axis}
                  width={44}
                />
                <ZAxis dataKey="name" name="Scenario" />
                <Tooltip
                  cursor={{ strokeDasharray: "2 2", stroke: theme.grid }}
                  content={({ active, payload }) => {
                    if (!active || !payload || payload.length === 0) return null;
                    const d = payload[0]?.payload as typeof chartData[number];
                    return (
                      <div className="font-mono text-[11px] bg-surface-1 border border-line-strong shadow-card-strong px-2.5 py-2 min-w-[140px]">
                        <div className="eyebrow mb-1.5">{d.name}</div>
                        <div className="flex flex-col gap-1">
                          <div className="flex justify-between gap-3">
                            <span className="text-ink-1 text-[10px] uppercase tracking-mech">TTFT p50</span>
                            <span className="text-ink-0 tabular">{d.ttft_p50.toFixed(1)}<span className="text-ink-2 text-[10px] ml-0.5">ms</span></span>
                          </div>
                          <div className="flex justify-between gap-3">
                            <span className="text-ink-1 text-[10px] uppercase tracking-mech">Throughput</span>
                            <span className="text-ink-0 tabular">{d.throughput.toFixed(0)}<span className="text-ink-2 text-[10px] ml-0.5">tok/s</span></span>
                          </div>
                          <div className="flex justify-between gap-3">
                            <span className="text-ink-1 text-[10px] uppercase tracking-mech">Target QPS</span>
                            <span className="text-ink-0 tabular">{d.qps}</span>
                          </div>
                        </div>
                      </div>
                    );
                  }}
                />
                <Scatter data={chartData} fill={theme.signal} shape="square" />
              </ScatterChart>
            </ResponsiveContainer>
            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 font-mono text-[10px] tracking-widemech uppercase text-ink-1">
              {chartData.map((d) => (
                <span key={d.name} className="flex items-center gap-1">
                  <span className="w-2 h-2" style={{ backgroundColor: theme.signal }} />
                  {d.name}
                </span>
              ))}
            </div>
          </ChartPanel>

          {/* Scenario Comparison Bar Chart */}
          <ChartPanel title="SCENARIO COMPARISON" unit="ms">
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={comparisonData} margin={{ top: 8, right: 16, left: 0, bottom: 0 }}>
                <CartesianGrid {...gridProps} stroke={theme.grid} />
                <XAxis dataKey="metric" tickLine={false} axisLine={{ stroke: theme.grid }} tick={axis} />
                <YAxis tickLine={false} axisLine={false} tick={axis} width={44} />
                <Tooltip content={<ChartTooltip unit="ms" />} cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }} />
                <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 4 }} />
                {scenarioNames.map((name, i) => (
                  <Bar key={name} dataKey={name} fill={palette[i % palette.length]} />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </ChartPanel>
        </div>
      )}
    </div>
  );
}

function ChartPanel({
  title,
  unit,
  children,
}: {
  title: string;
  unit: string;
  children: React.ReactNode;
}) {
  return (
    <div className="p-4 border-r border-b border-line">
      <div className="flex items-baseline justify-between mb-3">
        <span className="eyebrow">{title}</span>
        {unit && <span className="caption">{unit}</span>}
      </div>
      {children}
    </div>
  );
}
