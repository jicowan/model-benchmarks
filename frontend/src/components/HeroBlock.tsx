import type { ReactNode } from "react";

export interface HeroMetric {
  label: string;
  value: number | string | null | undefined;
  unit?: string;
  precision?: number;
  accent?: "signal" | "warn" | "danger" | "info";
}

interface Props {
  eyebrow?: string;
  heading: string;
  subheading?: ReactNode;
  meta?: ReactNode;
  metrics?: HeroMetric[];
  statusBadge?: ReactNode;
}

function formatValue(m: HeroMetric): string {
  if (m.value == null || m.value === "") return "—";
  if (typeof m.value === "number") return m.value.toFixed(m.precision ?? 1);
  return m.value;
}

function accentClass(a: HeroMetric["accent"]): string {
  switch (a) {
    case "signal":
      return "text-signal";
    case "warn":
      return "text-warn";
    case "danger":
      return "text-danger";
    case "info":
      return "text-info";
    default:
      return "text-ink-0";
  }
}

export default function HeroBlock({
  eyebrow,
  heading,
  subheading,
  meta,
  metrics,
  statusBadge,
}: Props) {
  // Grid layout scales with the number of metrics. Up to 4 uses the original
  // `md:grid-cols-4`; 5 metrics (PRD-35 added Total Cost) goes to
  // `md:grid-cols-5` so the row stays single-line on wide screens.
  const colClass =
    metrics && metrics.length >= 5 ? "md:grid-cols-5" : "md:grid-cols-4";

  return (
    <div className="mb-10 flex flex-col gap-6 md:flex-row md:items-end md:justify-between border-b border-line pb-8">
      <div className="min-w-0">
        {eyebrow && <div className="eyebrow mb-3">{eyebrow}</div>}
        <h1 className="font-sans text-[28px] leading-tight tracking-[-0.01em] text-balance break-words">
          {heading}
        </h1>
        {subheading && (
          <div className="mt-1.5 font-mono text-[13px] text-ink-1 truncate">
            {subheading}
          </div>
        )}
        {meta && <div className="mt-3 caption">{meta}</div>}
        {statusBadge && <div className="mt-4">{statusBadge}</div>}
      </div>

      {metrics && metrics.length > 0 && (
        <div className={`grid grid-cols-2 ${colClass} gap-6 md:gap-8`}>
          {metrics.map((m, i) => (
            <div key={i} className="min-w-0">
              <div className="eyebrow mb-1 truncate">{m.label}</div>
              <div
                className={`font-mono text-[28px] leading-none tabular ${accentClass(
                  m.accent
                )}`}
              >
                {formatValue(m)}
                {m.unit && (
                  <span className="ml-1.5 font-mono text-[11px] text-ink-2 tracking-widemech uppercase">
                    {m.unit}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
