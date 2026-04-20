import { useState } from "react";
import type { RecommendResponse } from "../types";

interface Props {
  recommendation: RecommendResponse;
}

interface CardProps {
  label: string;
  value: string | number;
  hint: string;
  tooltip: string;
}

function Card({ label, value, hint, tooltip }: CardProps) {
  const [showTooltip, setShowTooltip] = useState(false);

  return (
    <div
      className="relative panel p-4 flex flex-col items-center text-center cursor-help hover:border-line-strong transition-colors"
      onMouseEnter={() => setShowTooltip(true)}
      onMouseLeave={() => setShowTooltip(false)}
    >
      <span className="eyebrow">{label}</span>
      <span className="font-mono text-[22px] tabular text-ink-0 mt-1.5 leading-none">
        {value}
      </span>
      <span className="caption mt-2 leading-tight">{hint}</span>

      {showTooltip && (
        <div className="absolute z-10 bottom-full left-1/2 -translate-x-1/2 mb-2 w-64 p-3 bg-surface-2 border border-line-strong text-ink-0 font-mono text-[11.5px] leading-relaxed shadow-card-strong">
          <div className="eyebrow mb-1.5">{label}</div>
          <div className="text-ink-1">{tooltip}</div>
          <div className="absolute top-full left-1/2 -translate-x-1/2 border-4 border-transparent border-t-line-strong" />
        </div>
      )}
    </div>
  );
}

export default function RecommendationCards({ recommendation }: Props) {
  const { model_info, instance_info } = recommendation;

  if (!model_info || !instance_info) {
    return (
      <div className="border border-danger/40 bg-danger/5 p-4 font-mono text-[12px] text-danger">
        ERROR: Missing model_info or instance_info in recommendation
      </div>
    );
  }

  const paramB = ((model_info.parameter_count ?? 0) / 1e9).toFixed(1);
  const arch = model_info.architecture || "unknown";
  const modelSummary = `${arch.toUpperCase()} ${paramB}B`;
  const instanceSummary = `${instance_info.accelerator_count ?? 0}× ${instance_info.accelerator_name ?? "unknown"}`;

  const formatContext = (len: number) => {
    if (len >= 1000) return `${(len / 1024).toFixed(0)}K`;
    return len.toLocaleString();
  };

  return (
    <div className="border border-signal/40 bg-signal/5 p-4">
      {/* Header */}
      <div className="flex items-center gap-2 mb-3">
        <span className="w-1.5 h-1.5 bg-signal" />
        <span className="eyebrow text-signal">CONFIGURATION READY</span>
      </div>

      {/* Model → Instance summary */}
      <div className="font-mono text-[12.5px] text-ink-0 mb-4 flex items-center gap-2 flex-wrap">
        <span>{modelSummary}</span>
        <span className="text-ink-2">({model_info.native_dtype})</span>
        {model_info.sliding_window && model_info.sliding_window > 0 && (
          <span
            className="text-[10px] tracking-widemech uppercase border border-info/40 text-info px-1.5 py-0.5"
            title="KV cache capped at window size for efficient memory usage"
          >
            {(model_info.sliding_window / 1024).toFixed(0)}K WINDOW
          </span>
        )}
        <span className="text-signal">→</span>
        <span>{instanceSummary}</span>
        <span className="text-ink-2">({instance_info.accelerator_memory_gib} GiB)</span>
      </div>

      {/* Cards Grid */}
      <div className="grid grid-cols-2 md:grid-cols-3 gap-0 border-l border-t border-line">
        <Card
          label="TENSOR PARALLEL"
          value={recommendation.tensor_parallel_degree}
          hint={
            recommendation.tensor_parallel_degree === instance_info.accelerator_count
              ? "All GPUs"
              : `${recommendation.tensor_parallel_degree} of ${instance_info.accelerator_count} GPUs`
          }
          tooltip={recommendation.explanation.tensor_parallel_degree}
        />
        <Card
          label="QUANTIZATION"
          value={recommendation.quantization ?? "NONE"}
          hint={
            recommendation.quantization
              ? `${recommendation.quantization.toUpperCase()} precision`
              : `Native ${model_info.native_dtype}`
          }
          tooltip={recommendation.explanation.quantization}
        />
        <Card
          label="MAX CONTEXT"
          value={formatContext(recommendation.max_model_len)}
          hint={`${recommendation.max_model_len.toLocaleString()} tokens`}
          tooltip={recommendation.explanation.max_model_len}
        />
        <Card
          label="CONCURRENCY"
          value={recommendation.concurrency}
          hint="Parallel requests"
          tooltip={recommendation.explanation.concurrency}
        />
        <Card
          label="OVERHEAD"
          value={`${recommendation.overhead_gib.toFixed(1)}G`}
          hint="CUDA + graphs"
          tooltip={`Runtime overhead of ${recommendation.overhead_gib.toFixed(
            2
          )} GiB reserved for CUDA context, CUDA graph captures, and PyTorch allocator fragmentation.`}
        />
        <Card
          label="SEQUENCE"
          value={`${recommendation.input_sequence_length}→${recommendation.output_sequence_length}`}
          hint="Input → Output"
          tooltip={`Input: ${recommendation.input_sequence_length} tokens. Output: ${recommendation.output_sequence_length} tokens.`}
        />
      </div>
    </div>
  );
}
