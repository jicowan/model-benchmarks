import { useCallback, useEffect, useRef, useState } from "react";
import { searchModels, getModelDetail } from "../hfApi";
import type { HfModelSummary, HfModelDetail } from "../hfApi";

interface ModelComboboxProps {
  value: string;
  onChange: (modelId: string) => void;
  onModelSelect?: (detail: HfModelDetail) => void;
  hfToken?: string;
}

function formatCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

export default function ModelCombobox({
  value,
  onChange,
  onModelSelect,
  hfToken,
}: ModelComboboxProps) {
  const [results, setResults] = useState<HfModelSummary[]>([]);
  const [isOpen, setIsOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [gatedWarning, setGatedWarning] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  const doSearch = useCallback(
    async (query: string) => {
      if (query.length < 2) {
        setResults([]);
        setIsOpen(false);
        return;
      }
      setLoading(true);
      try {
        const items = await searchModels(query, hfToken || undefined);
        setResults(items);
        setIsOpen(items.length > 0);
        setActiveIndex(-1);
      } catch {
        setResults([]);
        setIsOpen(false);
      } finally {
        setLoading(false);
      }
    },
    [hfToken]
  );

  function handleInputChange(text: string) {
    onChange(text);
    setGatedWarning("");
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => doSearch(text), 300);
  }

  async function handleSelect(model: HfModelSummary) {
    onChange(model.modelId);
    setIsOpen(false);
    setGatedWarning("");

    try {
      const detail = await getModelDetail(model.modelId, hfToken || undefined);
      onModelSelect?.(detail);
    } catch (err: any) {
      if (err.message === "gated") {
        setGatedWarning(
          `This model requires access approval. Visit huggingface.co/${model.modelId} to request access.`
        );
      }
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!isOpen || results.length === 0) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveIndex((prev) => Math.min(prev + 1, results.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveIndex((prev) => Math.max(prev - 1, 0));
    } else if (e.key === "Enter" && activeIndex >= 0) {
      e.preventDefault();
      handleSelect(results[activeIndex]);
    } else if (e.key === "Escape") {
      setIsOpen(false);
    }
  }

  // Close dropdown on outside click.
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (
        wrapperRef.current &&
        !wrapperRef.current.contains(e.target as Node)
      ) {
        setIsOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  return (
    <div ref={wrapperRef} className="relative">
      <input
        type="text"
        required
        value={value}
        onChange={(e) => handleInputChange(e.target.value)}
        onFocus={() => {
          if (results.length > 0) setIsOpen(true);
        }}
        onKeyDown={handleKeyDown}
        placeholder="Search models or type ID (e.g. meta-llama/Llama-3.1-70B-Instruct)"
        className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        autoComplete="off"
      />

      {loading && (
        <div className="absolute right-3 top-2.5">
          <svg
            className="animate-spin h-4 w-4 text-gray-400"
            viewBox="0 0 24 24"
          >
            <circle
              className="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              strokeWidth="4"
              fill="none"
            />
            <path
              className="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
            />
          </svg>
        </div>
      )}

      {isOpen && results.length > 0 && (
        <ul className="absolute z-50 mt-1 w-full max-h-80 overflow-y-auto bg-white border border-gray-200 rounded-md shadow-lg">
          {results.map((model, i) => (
            <li
              key={model.modelId}
              onMouseDown={() => handleSelect(model)}
              onMouseEnter={() => setActiveIndex(i)}
              className={`px-3 py-2 cursor-pointer ${
                i === activeIndex ? "bg-blue-50" : "hover:bg-gray-50"
              }`}
            >
              <div className="text-sm font-medium text-gray-900">
                {model.modelId}
              </div>
              <div className="text-xs text-gray-500 flex items-center gap-2">
                <span>{formatCount(model.downloads)} downloads</span>
                <span>&middot;</span>
                <span>{formatCount(model.likes)} likes</span>
              </div>
            </li>
          ))}
        </ul>
      )}

      {gatedWarning && (
        <p className="mt-1 text-xs text-amber-600">{gatedWarning}</p>
      )}
    </div>
  );
}
