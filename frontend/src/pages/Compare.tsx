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
import { listCatalog } from "../api";
import type { CatalogEntry, PricingTier } from "../types";
import PricingToggle from "../components/PricingToggle";

const COLORS = ["#2563eb", "#dc2626", "#059669", "#d97706"];

// Placeholder hourly pricing (to be replaced with real pricing API data).
const PLACEHOLDER_PRICING: Record<string, Record<PricingTier, number>> = {
  "g5.xlarge": { on_demand: 1.006, reserved_1yr: 0.64, reserved_3yr: 0.43 },
  "g5.48xlarge": {
    on_demand: 16.288,
    reserved_1yr: 10.35,
    reserved_3yr: 6.97,
  },
  "p5.48xlarge": {
    on_demand: 98.32,
    reserved_1yr: 62.48,
    reserved_3yr: 42.08,
  },
  "p5e.48xlarge": {
    on_demand: 120.0,
    reserved_1yr: 76.27,
    reserved_3yr: 51.37,
  },
  "inf2.xlarge": {
    on_demand: 0.7582,
    reserved_1yr: 0.48,
    reserved_3yr: 0.33,
  },
};

function getPrice(instance: string, tier: PricingTier): number | null {
  return PLACEHOLDER_PRICING[instance]?.[tier] ?? null;
}

export default function Compare() {
  const [searchParams] = useSearchParams();
  const [entries, setEntries] = useState<CatalogEntry[]>([]);
  const [pricingTier, setPricingTier] = useState<PricingTier>("on_demand");

  useEffect(() => {
    const ids = searchParams.get("ids")?.split(",") ?? [];
    if (ids.length === 0) return;

    // Fetch full catalog then filter to selected IDs.
    listCatalog({ limit: 500 }).then((all) => {
      setEntries(all.filter((e) => ids.includes(e.run_id)));
    });
  }, [searchParams]);

  if (entries.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-bold mb-4">Compare</h1>
        <p className="text-gray-500">
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
    const hourly = getPrice(e.instance_type_name, pricingTier);
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
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Compare ({entries.length})</h1>
        <PricingToggle value={pricingTier} onChange={setPricingTier} />
      </div>

      {/* Comparison table */}
      <div className="overflow-x-auto border border-gray-200 rounded-lg mb-8">
        <table className="min-w-full divide-y divide-gray-200 text-sm">
          <thead className="bg-gray-50">
            <tr>
              <th className="px-4 py-3 text-left font-medium text-gray-500">
                Metric
              </th>
              {labels.map((l) => (
                <th
                  key={l}
                  className="px-4 py-3 text-right font-medium text-gray-500"
                >
                  {l}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
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
                <td className="px-4 py-2 font-medium text-gray-700">
                  {label}
                </td>
                {entries.map((e) => (
                  <td
                    key={e.run_id}
                    className="px-4 py-2 text-right text-gray-700"
                  >
                    {(
                      e[key as keyof CatalogEntry] as number | undefined
                    )?.toFixed(1) ?? "--"}
                  </td>
                ))}
              </tr>
            ))}
            {/* Cost rows */}
            <tr className="bg-blue-50">
              <td className="px-4 py-2 font-medium text-gray-700">
                Hourly Cost (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="px-4 py-2 text-right text-gray-700"
                >
                  {c.hourly != null ? `$${c.hourly.toFixed(2)}` : "--"}
                </td>
              ))}
            </tr>
            <tr className="bg-blue-50">
              <td className="px-4 py-2 font-medium text-gray-700">
                Cost/Request (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="px-4 py-2 text-right text-gray-700"
                >
                  {c.costPerRequest != null
                    ? `$${c.costPerRequest.toFixed(6)}`
                    : "--"}
                </td>
              ))}
            </tr>
            <tr className="bg-blue-50">
              <td className="px-4 py-2 font-medium text-gray-700">
                Cost/1M Tokens (USD)
              </td>
              {costRows.map((c) => (
                <td
                  key={c.label}
                  className="px-4 py-2 text-right text-gray-700"
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
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 mb-8">
        <div className="bg-white border border-gray-200 rounded-lg p-4">
          <h3 className="text-sm font-medium text-gray-700 mb-4">
            Latency (ms)
          </h3>
          <ResponsiveContainer width="100%" height={300}>
            <BarChart data={latencyData}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="name" tick={{ fontSize: 11 }} />
              <YAxis />
              <Tooltip />
              <Legend />
              <Bar dataKey="TTFT p50" fill="#2563eb" />
              <Bar dataKey="TTFT p99" fill="#93c5fd" />
              <Bar dataKey="E2E p50" fill="#dc2626" />
              <Bar dataKey="E2E p99" fill="#fca5a5" />
            </BarChart>
          </ResponsiveContainer>
        </div>

        <div className="bg-white border border-gray-200 rounded-lg p-4">
          <h3 className="text-sm font-medium text-gray-700 mb-4">
            Throughput
          </h3>
          <ResponsiveContainer width="100%" height={300}>
            <BarChart data={throughputData}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="name" tick={{ fontSize: 11 }} />
              <YAxis />
              <Tooltip />
              <Legend />
              <Bar dataKey="Tokens/s" fill="#059669" />
              <Bar dataKey="RPS" fill="#d97706" />
            </BarChart>
          </ResponsiveContainer>
        </div>

        <div className="bg-white border border-gray-200 rounded-lg p-4 lg:col-span-2">
          <h3 className="text-sm font-medium text-gray-700 mb-4">
            Performance Radar (higher = better)
          </h3>
          <ResponsiveContainer width="100%" height={350}>
            <RadarChart data={radarData}>
              <PolarGrid />
              <PolarAngleAxis dataKey="metric" tick={{ fontSize: 12 }} />
              <PolarRadiusAxis domain={[0, 100]} tick={false} />
              {entries.map((_, i) => (
                <Radar
                  key={labels[i]}
                  name={labels[i]}
                  dataKey={labels[i]}
                  stroke={COLORS[i]}
                  fill={COLORS[i]}
                  fillOpacity={0.15}
                />
              ))}
              <Legend />
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
          className="rounded-md bg-white px-4 py-2 text-sm font-medium text-gray-700 border border-gray-300 hover:bg-gray-50"
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
          className="rounded-md bg-white px-4 py-2 text-sm font-medium text-gray-700 border border-gray-300 hover:bg-gray-50"
        >
          Export CSV
        </button>
      </div>
    </div>
  );
}
