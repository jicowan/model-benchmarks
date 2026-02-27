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
    <div className="bg-white rounded-lg border border-gray-200 p-4">
      <dt className="text-sm font-medium text-gray-500 truncate">{label}</dt>
      <dd className="mt-1 text-2xl font-semibold text-gray-900">
        {value != null ? value.toFixed(precision) : "--"}
        <span className="ml-1 text-sm font-normal text-gray-500">{unit}</span>
      </dd>
    </div>
  );
}
