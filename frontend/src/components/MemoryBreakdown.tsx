import type { MemoryBreakdownResponse } from "../types";

interface Props {
  breakdown: MemoryBreakdownResponse | null;
  loading?: boolean;
}

const COLORS = {
  model_weights: "#3b82f6", // blue
  kv_cache: "#10b981", // green
  runtime_overhead: "#f59e0b", // amber
  quantization_metadata: "#8b5cf6", // purple
  block_table: "#ec4899", // pink
  headroom: "#e5e7eb", // gray-200
};

export default function MemoryBreakdown({ breakdown, loading }: Props) {
  if (!breakdown) {
    return null;
  }

  const total = breakdown.total_available_gib;
  const segments = [
    { label: "Model Weights", value: breakdown.model_weights_gib, color: COLORS.model_weights },
    { label: "KV Cache", value: breakdown.kv_cache_gib, color: COLORS.kv_cache },
    { label: "Runtime Overhead", value: breakdown.runtime_overhead_gib, color: COLORS.runtime_overhead },
    { label: "Quant Metadata", value: breakdown.quantization_metadata_gib, color: COLORS.quantization_metadata },
    { label: "Block Tables", value: breakdown.block_table_gib, color: COLORS.block_table },
    { label: "Headroom", value: breakdown.headroom_gib, color: COLORS.headroom },
  ].filter(s => s.value > 0.01); // Filter out negligible segments

  return (
    <div className="panel p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="eyebrow">MEMORY BREAKDOWN</h3>
        {loading && (
          <svg className="animate-spin h-4 w-4 text-ink-2" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        )}
      </div>

      {/* Stacked bar visualization */}
      <div className="relative h-8 border border-line overflow-hidden bg-surface-2 mb-3">
        {segments.map((seg, i) => {
          const pct = (seg.value / total) * 100;
          const offset = segments.slice(0, i).reduce((acc, s) => acc + (s.value / total) * 100, 0);
          return (
            <div
              key={seg.label}
              className="absolute top-0 h-full transition-all duration-300"
              style={{
                left: `${offset}%`,
                width: `${pct}%`,
                backgroundColor: seg.color,
              }}
              title={`${seg.label}: ${seg.value.toFixed(2)} GiB (${pct.toFixed(1)}%)`}
            />
          );
        })}
      </div>

      {/* Legend */}
      <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 font-mono text-[11.5px]">
        {segments.map((seg) => (
          <div key={seg.label} className="flex items-center gap-2">
            <span
              className="w-2.5 h-2.5 flex-shrink-0"
              style={{ backgroundColor: seg.color }}
            />
            <span className="text-ink-2">{seg.label}:</span>
            <span className="text-ink-0 tabular">{seg.value.toFixed(2)} GiB</span>
          </div>
        ))}
      </div>

      {/* Summary */}
      <div className="mt-3 pt-3 border-t border-line flex justify-between caption">
        <span>
          TOTAL USED: <span className="text-ink-0 tabular">{breakdown.total_used_gib.toFixed(2)} GiB</span>
        </span>
        <span>
          AVAILABLE: <span className="text-ink-0 tabular">{breakdown.total_available_gib.toFixed(2)} GiB</span>
        </span>
      </div>

      {/* PRD-51: scheduling-realistic banner. "clamps" = soft (runs at
          lower effective concurrency); "infeasible" = hard (vLLM
          crashes on load — still rendered as a warn banner so PRD-47's
          existing block path handles submission). "fits" = no banner. */}
      {breakdown.feasibility === "clamps" && breakdown.max_concurrency_at_pool !== undefined && (
        <div className="mt-3 p-2 bg-warn/5 border border-warn/40 font-mono text-[11.5px] text-warn">
          KV pool holds ~{breakdown.max_concurrency_at_pool} concurrent sequences; vLLM will schedule at most that many at a time. Requests beyond that queue.
        </div>
      )}
      {breakdown.feasibility === "infeasible" && (
        <div className="mt-3 p-2 bg-danger/5 border border-danger/40 font-mono text-[11.5px] text-danger">
          Weights + runtime overhead exceed available GPU memory. vLLM will crash on load.
        </div>
      )}

      {/* Legacy warning — PRD-51 superseded the "headroom &lt; 1 GiB"
          heuristic with the Feasibility field above. Kept for backwards
          compatibility with older API responses that lack `feasibility`. */}
      {!breakdown.feasibility && breakdown.warning_message && (
        <div className="mt-3 p-2 bg-warn/5 border border-warn/40 font-mono text-[11.5px] text-warn">
          {breakdown.warning_message}
        </div>
      )}
    </div>
  );
}
