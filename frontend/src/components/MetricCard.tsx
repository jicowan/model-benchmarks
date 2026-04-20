interface Props {
  label: string;
  value: number | undefined | null;
  unit: string;
  precision?: number;
}

export default function MetricCard({
  label,
  value,
  unit,
  precision = 1,
}: Props) {
  return (
    <div className="panel p-4">
      <dt className="eyebrow truncate">{label}</dt>
      <dd className="mt-2 font-mono text-[22px] tabular text-ink-0 leading-none">
        {value != null ? value.toFixed(precision) : "—"}
        <span className="ml-1.5 font-mono text-[11px] text-ink-2 tracking-widemech uppercase">
          {unit}
        </span>
      </dd>
    </div>
  );
}
