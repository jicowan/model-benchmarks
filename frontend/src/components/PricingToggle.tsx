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
    <div className="inline-flex rounded-md shadow-sm" role="group">
      {tiers.map((tier) => (
        <button
          key={tier.value}
          type="button"
          onClick={() => onChange(tier.value)}
          className={`px-4 py-2 text-sm font-medium border ${
            value === tier.value
              ? "bg-blue-600 text-white border-blue-600"
              : "bg-white text-gray-700 border-gray-300 hover:bg-gray-50"
          } ${tier.value === "on_demand" ? "rounded-l-md" : ""} ${
            tier.value === "reserved_3yr" ? "rounded-r-md" : ""
          }`}
        >
          {tier.label}
        </button>
      ))}
    </div>
  );
}
