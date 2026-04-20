// Industrial/grafana-style chart theme for Recharts, theme-aware via CSS vars.

import type { TooltipProps } from "recharts";

/* ------------------------------------------------------------------
   Read CSS variables at runtime so we get the active theme's colors.
   Recharts can't use Tailwind classes directly — it needs literal
   color strings. We evaluate these lazily so theme changes pick up
   on re-render.
   ------------------------------------------------------------------ */

function cssVar(name: string): string {
  if (typeof window === "undefined") return "#888";
  const root = getComputedStyle(document.documentElement);
  const v = root.getPropertyValue(name).trim();
  if (!v) return "#888";
  // CSS vars are stored as "R G B" triplets to support alpha compositing
  return `rgb(${v})`;
}

export function useChartTheme() {
  return {
    grid: cssVar("--line"),
    axis: cssVar("--ink-2"),
    text: cssVar("--ink-1"),
    textStrong: cssVar("--ink-0"),
    bg: cssVar("--surface-1"),
    bgInset: cssVar("--surface-2"),
    signal: cssVar("--signal"),
    warn: cssVar("--warn"),
    danger: cssVar("--danger"),
    info: cssVar("--info"),
  };
}

/* ------------------------------------------------------------------
   Palette — sequential ramps for percentile / metric series
   ------------------------------------------------------------------ */

// 4-stop percentile ramp (p50 → p99) — same hue, deepening intensity
export function percentileRamp(): string[] {
  // Derive from signal but steady fade — brightest for p50 (typical), subtler for tails
  return [
    "rgb(var(--signal) / 1)",
    "rgb(var(--signal) / 0.7)",
    "rgb(var(--signal) / 0.45)",
    "rgb(var(--signal) / 0.25)",
  ];
}

// Categorical series (when showing different metrics, not percentiles)
export function seriesPalette(): string[] {
  return [
    "rgb(var(--signal) / 0.95)",
    "rgb(var(--info) / 0.95)",
    "rgb(var(--warn) / 0.9)",
    "rgb(var(--danger) / 0.85)",
    "rgb(var(--ink-1) / 0.7)",
  ];
}

/* ------------------------------------------------------------------
   Common axis / grid props for consistency
   ------------------------------------------------------------------ */

export function axisStyle(theme: ReturnType<typeof useChartTheme>) {
  return {
    stroke: theme.axis,
    fontFamily: "Geist Mono, ui-monospace, monospace",
    fontSize: 10,
    letterSpacing: "0.02em",
  };
}

export const gridProps = {
  strokeDasharray: "2 4",
  vertical: false,
  strokeWidth: 0.5,
};

/* ------------------------------------------------------------------
   Custom tooltip — matches panel / eyebrow styling
   ------------------------------------------------------------------ */

interface TooltipPayload {
  name?: string | number;
  value?: string | number;
  color?: string;
  dataKey?: string | number;
}

export function ChartTooltip({
  active,
  payload,
  label,
  unit = "",
}: TooltipProps<number, string> & { unit?: string }) {
  if (!active || !payload || payload.length === 0) return null;
  return (
    <div className="font-mono text-[11px] bg-surface-1 border border-line-strong shadow-card-strong px-2.5 py-2 min-w-[140px]">
      {label !== undefined && label !== null && (
        <div className="eyebrow mb-1.5">{String(label)}</div>
      )}
      <div className="flex flex-col gap-1">
        {(payload as TooltipPayload[]).map((p, i) => (
          <div key={i} className="flex items-center justify-between gap-3">
            <span className="flex items-center gap-1.5 text-ink-1">
              <span
                className="w-2 h-2"
                style={{ backgroundColor: String(p.color) }}
              />
              <span className="uppercase tracking-mech text-[10px]">
                {String(p.name ?? p.dataKey ?? "")}
              </span>
            </span>
            <span className="text-ink-0 tabular">
              {typeof p.value === "number" ? p.value.toFixed(2) : String(p.value ?? "")}
              {unit && (
                <span className="text-ink-2 ml-0.5 text-[10px]">{unit}</span>
              )}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------
   Legend — monospace, small, industrial
   ------------------------------------------------------------------ */

interface LegendPayload {
  value?: string;
  color?: string;
}

export function ChartLegend({ payload }: { payload?: readonly LegendPayload[] }) {
  if (!payload) return null;
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[10.5px] tracking-widemech uppercase text-ink-1 pb-1">
      {payload.map((p, i) => (
        <div key={i} className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5" style={{ backgroundColor: String(p.color) }} />
          <span>{String(p.value)}</span>
        </div>
      ))}
    </div>
  );
}
