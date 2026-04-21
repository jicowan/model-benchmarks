import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
import { listCatalog, listPricing } from "../api";
import type { CatalogEntry, PricingTier, PricingRow } from "../types";
import PricingToggle from "../components/PricingToggle";
import {
  hourlyRateFromMap,
  costPerRequest as deriveCostPerRequest,
  costPer1MTokens as deriveCostPer1MTokens,
  toPricingMap,
} from "../lib/cost";
import { winnerIndex, configDiff, compareSummary } from "../lib/compare";
import {
  useChartTheme,
  seriesPalette,
  axisStyle,
  gridProps,
  chartFontFamily,
} from "../components/ChartTheme";

const AWS_REGIONS = [
  "us-east-1",
  "us-east-2",
  "us-west-1",
  "us-west-2",
  "eu-west-1",
  "eu-west-2",
  "eu-central-1",
  "ap-southeast-1",
  "ap-northeast-1",
];

type Direction = "min" | "max";

interface MetricRow {
  label: string;
  values: Array<number | undefined>;
  direction: Direction | null; // null = no winner highlighting
  format: (v: number | undefined) => string;
}

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

function MetricTable({
  entries,
  rows,
}: {
  entries: CatalogEntry[];
  rows: MetricRow[];
}) {
  return (
    <div className="panel overflow-x-auto">
      <table className="min-w-full">
        <thead>
          <tr>
            <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
              Metric
            </th>
            {entries.map((e) => (
              <th
                key={e.run_id}
                className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1"
              >
                {e.model_hf_id.split("/").pop()} / {e.instance_type_name}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const win = row.direction ? winnerIndex(row.values, row.direction) : null;
            return (
              <tr key={row.label}>
                <td className="py-2 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                  {row.label}
                </td>
                {row.values.map((v, i) => {
                  const isWinner = win === i;
                  return (
                    <td
                      key={entries[i].run_id}
                      className={`py-2 px-3 border-b border-line/60 text-right font-mono text-[12.5px] tabular ${
                        isWinner
                          ? "bg-signal/10 text-signal"
                          : "text-ink-0"
                      }`}
                    >
                      {isWinner && <span className="mr-1">▸</span>}
                      {row.format(v)}
                    </td>
                  );
                })}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function num(v: number | undefined, precision = 1): string {
  return v === undefined || v === null || Number.isNaN(v) ? "—" : v.toFixed(precision);
}

function usd(v: number | null | undefined, precision = 2): string {
  return v == null ? "—" : `$${v.toFixed(precision)}`;
}

export default function Compare() {
  const [searchParams] = useSearchParams();
  const [entries, setEntries] = useState<CatalogEntry[]>([]);
  const [pricingTier, setPricingTier] = useState<PricingTier>("on_demand");
  const [region, setRegion] = useState("us-east-2");
  const [pricingMap, setPricingMap] = useState<Map<string, PricingRow>>(new Map());
  const theme = useChartTheme();
  const palette = seriesPalette();
  const axis = axisStyle(theme);

  useEffect(() => {
    const ids = searchParams.get("ids")?.split(",") ?? [];
    if (ids.length === 0) return;
    listCatalog({ limit: 500 }).then((all) => {
      setEntries(all.filter((e) => ids.includes(e.run_id)));
    });
  }, [searchParams]);

  useEffect(() => {
    listPricing(region).then((rows) => setPricingMap(toPricingMap(rows)));
  }, [region]);

  if (entries.length === 0) {
    return (
      <div className="p-6">
        <h1 className="text-2xl font-bold mb-4">Compare</h1>
        <p className="meta">Select entries from the Catalog to compare.</p>
      </div>
    );
  }

  return (
    <CompareInner
      entries={entries}
      pricingMap={pricingMap}
      pricingTier={pricingTier}
      setPricingTier={setPricingTier}
      region={region}
      setRegion={setRegion}
      theme={theme}
      axis={axis}
      palette={palette}
    />
  );
}

function CompareInner({
  entries,
  pricingMap,
  pricingTier,
  setPricingTier,
  region,
  setRegion,
  theme,
  axis,
  palette,
}: {
  entries: CatalogEntry[];
  pricingMap: Map<string, PricingRow>;
  pricingTier: PricingTier;
  setPricingTier: (t: PricingTier) => void;
  region: string;
  setRegion: (r: string) => void;
  theme: ReturnType<typeof useChartTheme>;
  axis: ReturnType<typeof axisStyle>;
  palette: string[];
}) {
  // Cost derivation per entry
  const costs = useMemo(
    () =>
      entries.map((e) => {
        const hourly = hourlyRateFromMap(pricingMap, e.instance_type_name, pricingTier);
        return {
          hourly,
          perRequest: deriveCostPerRequest(hourly, e.requests_per_second),
          per1MTokens: deriveCostPer1MTokens(hourly, e.throughput_aggregate_tps),
        };
      }),
    [entries, pricingMap, pricingTier]
  );

  const summary = useMemo(() => compareSummary(entries), [entries]);
  const diff = useMemo(() => configDiff(entries), [entries]);

  // Tradeoff charts: horizontal bars for TTFT p99 (lower=better) and
  // Throughput (higher=better). Same Y-axis order in both so readers can
  // compare a given run across axes by scanning the same row position.
  const chartData = entries.map((e, i) => ({
    name:
      (e.model_hf_id.split("/").pop() ?? e.run_id.slice(0, 8)) +
      ` · ${e.instance_type_name}`,
    ttft: e.ttft_p99_ms ?? 0,
    throughput: e.throughput_aggregate_tps ?? 0,
    color: palette[i % palette.length],
  }));

  // Identify winners so we can tint the winning bar.
  const ttftWin = winnerIndex(
    chartData.map((d) => d.ttft),
    "min"
  );
  const tpWin = winnerIndex(
    chartData.map((d) => d.throughput),
    "max"
  );

  const latencyRows: MetricRow[] = [
    row("TTFT p50 (ms)", entries, (e) => e.ttft_p50_ms, "min"),
    row("TTFT p95 (ms)", entries, (e) => e.ttft_p95_ms, "min"),
    row("TTFT p99 (ms)", entries, (e) => e.ttft_p99_ms, "min"),
    row("E2E p50 (ms)", entries, (e) => e.e2e_latency_p50_ms, "min"),
    row("E2E p95 (ms)", entries, (e) => e.e2e_latency_p95_ms, "min"),
    row("E2E p99 (ms)", entries, (e) => e.e2e_latency_p99_ms, "min"),
    row("ITL p50 (ms)", entries, (e) => e.itl_p50_ms, "min"),
    row("ITL p95 (ms)", entries, (e) => e.itl_p95_ms, "min"),
  ];

  const throughputRows: MetricRow[] = [
    row("Throughput (tok/s)", entries, (e) => e.throughput_aggregate_tps, "max", 0),
    {
      label: "Throughput / GPU (tok/s)",
      values: entries.map((e) => {
        const t = e.throughput_aggregate_tps;
        const n = e.accelerator_count || 0;
        return t !== undefined && n > 0 ? t / n : undefined;
      }),
      direction: "max",
      format: (v) => num(v, 1),
    },
    row("Requests / sec", entries, (e) => e.requests_per_second, "max", 2),
    {
      label: "Success Rate (%)",
      values: entries.map((e) => {
        const ok = e.successful_requests ?? 0;
        const total = ok + (e.failed_requests ?? 0);
        return total > 0 ? (ok / total) * 100 : undefined;
      }),
      // All-at-100% is a common tied case we don't want to decorate.
      direction: "max",
      format: (v) => num(v, 1),
    },
  ];

  const hardwareRows: MetricRow[] = [
    row("GPU Busy % (avg)", entries, (e) => e.accelerator_utilization_avg_pct, null, 0),
    row("SM Active % (avg)", entries, (e) => e.sm_active_avg_pct, null, 0),
    row("Tensor Active % (avg)", entries, (e) => e.tensor_active_avg_pct, null, 0),
    row("DRAM Active % (avg)", entries, (e) => e.dram_active_avg_pct, null, 0),
  ];

  const memoryRows: MetricRow[] = [
    row("Memory GiB (avg)", entries, (e) => e.accelerator_memory_avg_gib, null, 1),
    row("Memory GiB (peak)", entries, (e) => e.accelerator_memory_peak_gib, null, 1),
  ];

  const costRows: MetricRow[] = [
    {
      label: "Hourly Cost (USD)",
      values: costs.map((c) => c.hourly ?? undefined),
      direction: "min",
      format: (v) => usd(v, 2),
    },
    {
      label: "Cost / Request (USD)",
      values: costs.map((c) => c.perRequest ?? undefined),
      direction: "min",
      format: (v) => usd(v, 6),
    },
    {
      label: "Cost / 1M Tokens (USD)",
      values: costs.map((c) => c.per1MTokens ?? undefined),
      direction: "min",
      format: (v) => usd(v, 2),
    },
  ];

  return (
    <>
      <div className="h-14 border-b border-line flex items-center justify-between px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <a href="/runs" className="text-ink-1 hover:text-ink-0">runs</a>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">compare ({entries.length})</span>
        </div>
        <div className="flex items-center gap-3">
          <select
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            className="input input-sm"
          >
            {AWS_REGIONS.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <PricingToggle value={pricingTier} onChange={setPricingTier} />
        </div>
      </div>

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
        <div className="mb-8">
          <div className="eyebrow mb-2">SIDE-BY-SIDE</div>
          <h1 className="font-sans text-[22px] leading-tight tracking-[-0.01em]">
            Compare {entries.length} runs
          </h1>
        </div>

        {/* Summary */}
        {summary.length > 0 && (
          <div className="panel p-5 mb-8">
            <div className="eyebrow mb-3">SUMMARY</div>
            <ul className="flex flex-col gap-1.5 font-sans text-[13.5px] text-ink-0">
              {summary.map((s, i) => (
                <li key={i} className="flex items-baseline gap-2">
                  <span className="text-ink-2 font-mono text-[11px]">›</span>
                  <span>{s}</span>
                </li>
              ))}
            </ul>
          </div>
        )}

        {/* A. Configuration diff */}
        <section className="mb-8">
          <SectionHeader index="A" label="Configuration" />
          {diff.length === 0 ? (
            <div className="panel p-4 caption">
              ALL RUNS USE IDENTICAL CONFIGURATION
            </div>
          ) : (
            <div className="panel overflow-x-auto">
              <table className="min-w-full">
                <thead>
                  <tr>
                    <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                      Field
                    </th>
                    {entries.map((e) => (
                      <th
                        key={e.run_id}
                        className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1"
                      >
                        {e.run_id.slice(0, 8)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {diff.map(({ label, values }) => (
                    <tr key={label}>
                      <td className="py-2 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                        {label}
                      </td>
                      {values.map((v, i) => (
                        <td
                          key={i}
                          className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px]"
                        >
                          {v}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>

        {/* B. Tradeoff bars */}
        {entries.length >= 2 && (
          <section className="mb-8">
            <SectionHeader index="B" label="Latency ↔ throughput" />
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-0 border-l border-t border-line">
              <TradeoffBarChart
                title="TTFT P99 — SHORTER IS BETTER"
                unit="ms"
                data={chartData}
                dataKey="ttft"
                winnerIdx={ttftWin}
                theme={theme}
                axis={axis}
              />
              <TradeoffBarChart
                title="THROUGHPUT — LONGER IS BETTER"
                unit="tok/s"
                data={chartData}
                dataKey="throughput"
                winnerIdx={tpWin}
                theme={theme}
                axis={axis}
              />
            </div>
          </section>
        )}

        {/* C. Latency */}
        <section className="mb-8">
          <SectionHeader index="C" label="Latency" />
          <MetricTable entries={entries} rows={latencyRows} />
        </section>

        {/* D. Throughput */}
        <section className="mb-8">
          <SectionHeader index="D" label="Throughput" />
          <MetricTable entries={entries} rows={throughputRows} />
        </section>

        {/* E. Hardware utilization */}
        <section className="mb-8">
          <SectionHeader index="E" label="Hardware utilization" />
          <MetricTable entries={entries} rows={hardwareRows} />
          <p className="mt-1.5 caption text-ink-2">
            Higher isn't always better — high utilization may indicate saturation.
          </p>
        </section>

        {/* F. Memory */}
        <section className="mb-8">
          <SectionHeader index="F" label="Memory" />
          <MetricTable entries={entries} rows={memoryRows} />
        </section>

        {/* G. Cost */}
        <section className="mb-8">
          <SectionHeader index="G" label="Cost" />
          <MetricTable entries={entries} rows={costRows} />
        </section>

        {/* Export */}
        <div className="flex gap-3">
          <button
            onClick={() => {
              const blob = new Blob([JSON.stringify(entries, null, 2)], {
                type: "application/json",
              });
              const url = URL.createObjectURL(blob);
              const a = document.createElement("a");
              a.href = url;
              a.download = "accelbench-comparison.json";
              a.click();
            }}
            className="btn"
          >
            Export JSON
          </button>
        </div>
      </div>
    </>
  );
}

// Helper to build a MetricRow with a scalar extractor
function row(
  label: string,
  entries: CatalogEntry[],
  get: (e: CatalogEntry) => number | undefined,
  direction: Direction | null,
  precision = 1
): MetricRow {
  return {
    label,
    values: entries.map(get),
    direction,
    format: (v) => num(v, precision),
  };
}

interface BarRow {
  name: string;
  color: string;
  [key: string]: string | number;
}

function TradeoffBarChart({
  title,
  unit,
  data,
  dataKey,
  winnerIdx,
  theme,
  axis,
}: {
  title: string;
  unit: string;
  data: BarRow[];
  dataKey: "ttft" | "throughput";
  winnerIdx: number | null;
  theme: ReturnType<typeof useChartTheme>;
  axis: ReturnType<typeof axisStyle>;
}) {
  // Height scales with number of rows so bars stay readable.
  const height = Math.max(140, 48 * data.length + 48);
  return (
    <div className="p-4 border-r border-b border-line bg-surface-1">
      <div className="flex items-baseline justify-between mb-3">
        <span className="eyebrow">{title}</span>
        <span className="caption">{unit}</span>
      </div>
      <ResponsiveContainer width="100%" height={height}>
        <BarChart
          data={data}
          layout="vertical"
          margin={{ top: 4, right: 60, left: 8, bottom: 4 }}
        >
          <CartesianGrid {...gridProps} stroke={theme.grid} horizontal={false} />
          <XAxis
            type="number"
            tickLine={false}
            axisLine={{ stroke: theme.grid }}
            tick={axis}
          />
          <YAxis
            type="category"
            dataKey="name"
            tickLine={false}
            axisLine={false}
            tick={{ ...axis, fill: theme.text }}
            width={200}
            interval={0}
          />
          <Tooltip
            cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }}
            content={({ active, payload }) => {
              if (!active || !payload || payload.length === 0) return null;
              const p = payload[0];
              const d = p.payload as BarRow;
              return (
                <div className="font-mono text-[11px] bg-surface-1 border border-line-strong shadow-card-strong px-2.5 py-2 min-w-[200px]">
                  <div className="eyebrow mb-1.5 truncate">{d.name}</div>
                  <div className="flex justify-between gap-3">
                    <span className="text-ink-1 text-[10px] uppercase tracking-mech">{title.split(" — ")[0]}</span>
                    <span className="text-ink-0 tabular">
                      {(d[dataKey] as number).toFixed(0)}
                      <span className="text-ink-2 text-[10px] ml-0.5">{unit}</span>
                    </span>
                  </div>
                </div>
              );
            }}
          />
          <Bar
            dataKey={dataKey}
            isAnimationActive={false}
            label={{
              position: "right",
              fill: theme.text,
              stroke: "none",
              fontSize: 11,
              fontFamily: chartFontFamily,
              formatter: (value: number) => value.toFixed(0),
            }}
          >
            {data.map((d, i) => {
              const isWinner = winnerIdx === i;
              return (
                <Cell
                  key={i}
                  fill={d.color}
                  fillOpacity={isWinner ? 1 : 0.55}
                  stroke={isWinner ? "rgb(var(--signal))" : "transparent"}
                  strokeWidth={isWinner ? 1.5 : 0}
                />
              );
            })}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
