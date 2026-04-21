import { useEffect, useState } from "react";
import type { CatalogFilter } from "../types";

interface Props {
  onFilter: (filter: CatalogFilter) => void;
}

const ACCELERATORS = ["all", "gpu", "neuron"] as const;

export default function FilterBar({ onFilter }: Props) {
  const [model, setModel] = useState("");
  const [acceleratorType, setAcceleratorType] = useState<string>("all");

  // Debounce changes so typing in the search box doesn't hammer the backend.
  useEffect(() => {
    const t = setTimeout(() => {
      onFilter({
        model: model || undefined,
        accelerator_type:
          acceleratorType === "all" ? undefined : acceleratorType,
      });
    }, 200);
    return () => clearTimeout(t);
  }, [model, acceleratorType, onFilter]);

  return (
    <div className="mb-6 panel">
      <div className="flex items-center">
        <div className="flex border-r border-line">
          {ACCELERATORS.map((opt, i) => (
            <button
              key={opt}
              type="button"
              onClick={() => setAcceleratorType(opt)}
              className={`h-11 px-4 font-mono text-[11px] tracking-mech uppercase ${
                i < ACCELERATORS.length - 1 ? "border-r border-line" : ""
              } transition-colors ${
                acceleratorType === opt
                  ? "text-ink-0 bg-surface-2"
                  : "text-ink-1 hover:text-ink-0 hover:bg-surface-2/60"
              }`}
            >
              {opt}
            </button>
          ))}
        </div>

        <div className="flex-1 relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-2 pointer-events-none">
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="square"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="M21 21l-4.35-4.35" />
            </svg>
          </span>
          <input
            type="text"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="filter by model id…"
            className="w-full h-11 pl-9 pr-3 bg-transparent font-mono text-[12px] tracking-mech text-ink-0 placeholder:text-ink-2 focus:outline-none focus:bg-surface-2"
          />
        </div>
      </div>
    </div>
  );
}
