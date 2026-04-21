import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
  RadarChart,
  PolarGrid,
  PolarAngleAxis,
  PolarRadiusAxis,
  Radar,
} from "recharts";
import { listCatalog, listPricing } from "../api";
import type { CatalogEntry, PricingTier, PricingRow } from "../types";
import PricingToggle from "../components/PricingToggle";
import {
  useChartTheme,
  seriesPalette,
  percentileRamp,
  axisStyle,
  gridProps,
  ChartTooltip,
  ChartLegend,
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

function getPrice(
  pricingMap: Map<string, PricingRow>,
  instance: string,
  tier: PricingTier
): number | null {
  const row = pricingMap.get(instance);
  if (!row) return null;
  switch (tier) {
    case "on_demand":
      return row.on_demand_hourly_usd;
    case "reserved_1yr":
      return row.reserved_1yr_hourly_usd ?? null;
    case "reserved_3yr":
      return row.reserved_3yr_hourly_usd ?? null;
  }
}

export default function Compare() {
  const [searchParams] = useSearchParams();
  const [entries, setEntries] = useState<CatalogEntry[]>([]);
  const [pricingTier, setPricingTier] = useState<PricingTier>("on_demand");
  const [region, setRegion] = useState("us-east-2");
  const [pricingMap, setPricingMap] = useState<Map<string, PricingRow>>(
    new Map()
  );
  const theme = useChartTheme();
  const palette = seriesPalette();
  const ramp = percentileRamp();
  const axis = axisStyle(theme);

  useEffect(() => {
    const ids = searchParams.get("ids")?.split(",") ?? [];
    if (ids.length === 0) return;

    // Fetch full catalog then filter to selected IDs.
    listCatalog({ limit: 500 }).then((all) => {
      setEntries(all.filter((e) => ids.includes(e.run_id)));
    });
  }, [searchParams]);

  useEffect(() => {
    listPricing(region).then((rows) => {
      const m = new Map<string, PricingRow>();
      for (const r of rows) {
        m.set(r.instance_type_name, r);
      }
      setPricingMap(m);
    });
  }, [region]);

  if (entries.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-bold mb-4">Compare</h1>
        <p className="meta">
          Select up to 4 entries from the Catalog to compare.
        </p>
      </div>
    );
  }

  const labels = entries.map(
    (e) => `${e.model_hf_id.split("/").pop()} / ${e.instance_type_name}`
  );

  const latencyData = entries.map((e, i) => ({
    name: labels[i],
    "TTFT p50": e.ttft_p50_ms ?? 0,
    "TTFT p99": e.ttft_p99_ms ?? 0,
    "E2E p50": e.e2e_latency_p50_ms ?? 0,
    "E2E p99": e.e2e_latency_p99_ms ?? 0,
  }));

  const throughputData = entries.map((e, i) => ({
    name: labels[i],
    "Tokens/s": e.throughput_aggregate_tps ?? 0,
    RPS: e.requests_per_second ?? 0,
  }));

  // Normalize metrics to 0-100 for radar chart.
  const metricKeys = [
    "ttft_p50_ms",
    "e2e_latency_p50_ms",
    "itl_p50_ms",
    "throughput_aggregate_tps",
    "requests_per_second",
  ] as const;
  const metricLabels = ["TTFT p50", "E2E p50", "ITL p50", "Throughput", "RPS"];

  const maxVals = metricKeys.map((k) =>
    Math.max(...entries.map((e) => (e[k] as number) ?? 0), 1)
  );

  const radarData = metricLabels.map((label, mi) => {
    const point: Record<string, string | number> = { metric: label };
    entries.forEach((e, ei) => {
      const raw = (e[metricKeys[mi]] as number) ?? 0;
      // For latency metrics, lower is better — invert the scale.
      const isLatency = mi < 3;
      point[labels[ei]] = isLatency
        ? Math.round(((maxVals[mi] - raw) / maxVals[mi]) * 100)
        : Math.round((raw / maxVals[mi]) * 100);
    });
    return point;
  });

  // Cost table.
  const costRows = entries.map((e) => {
    const hourly = getPrice(pricingMap, e.instance_type_name, pricingTier);
    const rps = e.requests_per_second;
    const tps = e.throughput_aggregate_tps;
    return {
      label: `${e.model_hf_id.split("/").pop()} / ${e.instance_type_name}`,
      hourly,
      costPerRequest:
        hourly && rps && rps > 0 ? hourly / rps / 3600 : null,
      costPer1MTokens:
        hourly && tps && tps > 0
          ? (hourly / tps / 3600) * 1_000_000
          : null,
    };
  });

  return (
    <>
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <a href="/runs" className="text-ink-1 hover:text-ink-0">runs</a>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">compare ({entries.length})</span>
        </div>
      </div>
      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
      <div className="flex items-center justify-between mb-6">
        <div>
          <div className="eyebrow mb-2">SIDE-BY-SIDE</div>
          <h1 className="font-sans text-[22px] leading-tight tracking-[-0.01em]">Compare runs</h1>
        </div>
        <div className="flex items-center gap-4">
          <select
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            className="input"
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

      {/* Comparison table */}
      <div className="panel overflow-x-auto mb-8">
        <table className="min-w-full">
          <thead className="bg-surface-1">
            <tr>
              <th className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1">
                Metric
              </th>
              {labels.map((l) => (
                <th
                  key={l}
                  className="eyebrow text-right py-2 px-3 border-b border-line bg-surface-1"
                >
                  {l}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {[
              ["TTFT p50 (ms)", "ttft_p50_ms"],
              ["TTFT p99 (ms)", "ttft_p99_ms"],
              ["E2E Latency p50 (ms)", "e2e_latency_p50_ms"],
              ["E2E Latency p99 (ms)", "e2e_latency_p99_ms"],
              ["ITL p50 (ms)", "itl_p50_ms"],
              ["Throughput (tok/s)", "throughput_aggregate_tps"],
              ["RPS", "requests_per_second"],
              ["GPU Util %", "accelerator_utilization_pct"],
            ].map(([label, key]) => (
              <tr key={key}>
                <td className="py-2 px-3 border-b border-line/60 text-ink-1 font-mono text-[12.5px]">
                  {label}
                </td>
                {entries.map((e) => (
                  <td
                    key={e.run_id}
                    className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px] tabular"
                  >
                    {(
                      e[key as keyof CatalogEntry] as number | undefined
                    )?.toFixed(1) ?? "--"}
                  </td>
                ))}
              </tr>
            ))}
            {/* Cost rows */}
            <tr className="bg-info/5">
              <td className="py-2 px-3 border-b border-line/60 text-ink-0 font-mono text-[12.5px]">
                Hourly Cost (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px] tabular"
                >
                  {c.hourly != null ? `$${c.hourly.toFixed(2)}` : "--"}
                </td>
              ))}
            </tr>
            <tr className="bg-info/5">
              <td className="py-2 px-3 border-b border-line/60 text-ink-0 font-mono text-[12.5px]">
                Cost/Request (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px] tabular"
                >
                  {c.costPerRequest != null
                    ? `$${c.costPerRequest.toFixed(6)}`
                    : "--"}
                </td>
              ))}
            </tr>
            <tr className="bg-info/5">
              <td className="py-2 px-3 border-b border-line/60 text-ink-0 font-mono text-[12.5px]">
                Cost/1M Tokens (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="py-2 px-3 border-b border-line/60 text-right text-ink-0 font-mono text-[12.5px] tabular"
                >
                  {c.costPer1MTokens != null
                    ? `$${c.costPer1MTokens.toFixed(2)}`
                    : "--"}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>

      {/* Charts */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-0 border-l border-t border-line mb-8">
        <div className="p-4 border-r border-b border-line bg-surface-1">
          <div className="flex items-baseline justify-between mb-3">
            <span className="eyebrow">LATENCY</span>
            <span className="caption">ms</span>
          </div>
          <ResponsiveContainer width="100%" height={280}>
            <BarChart data={latencyData} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
              <CartesianGrid {...gridProps} stroke={theme.grid} />
              <XAxis dataKey="name" tickLine={false} axisLine={{ stroke: theme.grid }} tick={axis} />
              <YAxis tickLine={false} axisLine={false} tick={axis} width={44} />
              <Tooltip content={<ChartTooltip unit="ms" />} cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }} />
              <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 4 }} />
              <Bar dataKey="TTFT p50" fill={ramp[0]} />
              <Bar dataKey="TTFT p99" fill={ramp[2]} />
              <Bar dataKey="E2E p50" fill={palette[1]} />
              <Bar dataKey="E2E p99" fill="rgb(var(--info) / 0.45)" />
            </BarChart>
          </ResponsiveContainer>
        </div>

        <div className="p-4 border-r border-b border-line bg-surface-1">
          <div className="flex items-baseline justify-between mb-3">
            <span className="eyebrow">THROUGHPUT</span>
            <span className="caption">tok/s · rps</span>
          </div>
          <ResponsiveContainer width="100%" height={280}>
            <BarChart data={throughputData} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
              <CartesianGrid {...gridProps} stroke={theme.grid} />
              <XAxis dataKey="name" tickLine={false} axisLine={{ stroke: theme.grid }} tick={axis} />
              <YAxis tickLine={false} axisLine={false} tick={axis} width={44} />
              <Tooltip content={<ChartTooltip />} cursor={{ fill: "rgb(var(--ink-2) / 0.08)" }} />
              <Legend content={<ChartLegend />} wrapperStyle={{ paddingBottom: 4 }} />
              <Bar dataKey="Tokens/s" fill={palette[0]} />
              <Bar dataKey="RPS" fill={palette[2]} />
            </BarChart>
          </ResponsiveContainer>
        </div>

        <div className="p-4 border-r border-b border-line bg-surface-1 lg:col-span-2">
          <div className="flex items-baseline justify-between mb-3">
            <span className="eyebrow">PERFORMANCE PROFILE</span>
            <span className="caption">normalized · higher = better</span>
          </div>
          <ResponsiveContainer width="100%" height={340}>
            <RadarChart data={radarData}>
              <PolarGrid stroke={theme.grid} strokeDasharray="2 4" />
              <PolarAngleAxis dataKey="metric" tick={{ ...axis, fill: theme.axis }} stroke={theme.grid} />
              <PolarRadiusAxis domain={[0, 100]} tick={false} stroke={theme.grid} axisLine={false} />
              {entries.map((_, i) => (
                <Radar
                  key={labels[i]}
                  name={labels[i]}
                  dataKey={labels[i]}
                  stroke={palette[i % palette.length]}
                  fill={palette[i % palette.length]}
                  fillOpacity={0.18}
                  strokeWidth={1.5}
                />
              ))}
              <Tooltip content={<ChartTooltip />} />
              <Legend content={<ChartLegend />} wrapperStyle={{ paddingTop: 8 }} />
            </RadarChart>
          </ResponsiveContainer>
        </div>
      </div>

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
        <button
          onClick={() => {
            const header = Object.keys(entries[0]).join(",");
            const rows = entries.map((e) => Object.values(e).join(","));
            const csv = [header, ...rows].join("\n");
            const blob = new Blob([csv], { type: "text/csv" });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = "accelbench-comparison.csv";
            a.click();
          }}
          className="btn"
        >
          Export CSV
        </button>
      </div>
      </div>
    </>
  );
}
