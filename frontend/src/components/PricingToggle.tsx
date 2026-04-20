import type { PricingTier } from "../types";

const tiers: { value: PricingTier; label: string }[] = [
  { value: "on_demand", label: "On-Demand" },
  { value: "reserved_1yr", label: "1yr RI" },
  { value: "reserved_3yr", label: "3yr RI" },
];

interface Props {
  value: PricingTier;
  onChange: (tier: PricingTier) => void;
}

export default function PricingToggle({ value, onChange }: Props) {
  return (
    <div className="inline-flex border border-line divide-x divide-line" role="group">
      {tiers.map((tier) => (
        <button
          key={tier.value}
          type="button"
          onClick={() => onChange(tier.value)}
          className={`h-8 px-3 font-mono text-[11px] tracking-mech uppercase transition-colors ${
            value === tier.value
              ? "bg-signal/10 text-signal"
              : "bg-surface-1 text-ink-1 hover:bg-surface-2 hover:text-ink-0"
          }`}
        >
          {tier.label}
        </button>
      ))}
    </div>
  );
}
