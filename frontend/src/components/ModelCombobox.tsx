import { useCallback, useEffect, useRef, useState } from "react";
import { searchModels, getModelDetail } from "../hfApi";
import type { HfModelSummary, HfModelDetail } from "../hfApi";
import { listModelCache } from "../api";
import type { ModelCache } from "../types";

interface ModelComboboxProps {
  value: string;
  onChange: (modelId: string) => void;
  onModelSelect?: (detail: HfModelDetail) => void;
  onCachedModelSelect?: (cached: ModelCache) => void;
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
  onCachedModelSelect,
  hfToken,
}: ModelComboboxProps) {
  const [results, setResults] = useState<HfModelSummary[]>([]);
  const [cachedModels, setCachedModels] = useState<ModelCache[]>([]);
  const [filteredCached, setFilteredCached] = useState<ModelCache[]>([]);
  const [isOpen, setIsOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [gatedWarning, setGatedWarning] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    listModelCache()
      .then((items) => setCachedModels(items.filter((m) => m.status === "cached")))
      .catch(() => {});
  }, []);

  const doSearch = useCallback(
    async (query: string) => {
      if (query.length < 2) {
        setResults([]);
        setFilteredCached([]);
        setIsOpen(false);
        return;
      }
      setLoading(true);
      try {
        const q = query.toLowerCase();
        const cached = cachedModels.filter(
          (m) =>
            m.display_name.toLowerCase().includes(q) ||
            (m.hf_id && m.hf_id.toLowerCase().includes(q))
        );
        setFilteredCached(cached);

        const items = await searchModels(query, hfToken || undefined);
        setResults(items);
        setIsOpen(items.length > 0 || cached.length > 0);
        setActiveIndex(-1);
      } catch {
        setResults([]);
        setIsOpen(filteredCached.length > 0);
      } finally {
        setLoading(false);
      }
    },
    [hfToken, cachedModels]
  );

  function handleInputChange(text: string) {
    onChange(text);
    setGatedWarning("");
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => doSearch(text), 300);
  }

  function handleSelectCached(model: ModelCache) {
    onChange(model.hf_id || model.display_name);
    setIsOpen(false);
    setGatedWarning("");
    onCachedModelSelect?.(model);
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
          if (results.length > 0 || filteredCached.length > 0) setIsOpen(true);
        }}
        onKeyDown={handleKeyDown}
        placeholder="Search models or type ID (e.g. meta-llama/Llama-3.1-70B-Instruct)"
        className="input w-full"
        autoComplete="off"
      />

      {loading && (
        <div className="absolute right-3 top-2.5">
          <svg
            className="animate-spin h-4 w-4 text-ink-2"
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

      {isOpen && (filteredCached.length > 0 || results.length > 0) && (
        <ul className="absolute z-50 mt-1 w-full max-h-80 overflow-y-auto bg-surface-1 border border-line shadow-card-strong">
          {filteredCached.length > 0 && (
            <>
              <li className="px-3 py-1.5 eyebrow bg-surface-2 border-b border-line">
                Cached Models
              </li>
              {filteredCached.map((model) => (
                <li
                  key={`cached-${model.id}`}
                  onMouseDown={() => handleSelectCached(model)}
                  className="px-3 py-2 cursor-pointer hover:bg-surface-2"
                >
                  <div className="font-mono text-[12.5px] text-ink-0 flex items-center gap-2">
                    {model.display_name}
                    <span className="inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-widemech uppercase border border-signal/50 text-signal bg-signal/5">
                      S3
                    </span>
                  </div>
                  <div className="caption font-mono truncate">
                    {model.s3_uri}
                  </div>
                </li>
              ))}
            </>
          )}
          {results.length > 0 && (
            <>
              {filteredCached.length > 0 && (
                <li className="px-3 py-1.5 eyebrow bg-surface-2 border-b border-line">
                  HuggingFace
                </li>
              )}
              {results.map((model, i) => (
                <li
                  key={model.modelId}
                  onMouseDown={() => handleSelect(model)}
                  onMouseEnter={() => setActiveIndex(i)}
                  className={`px-3 py-2 cursor-pointer ${
                    i === activeIndex ? "bg-signal/10" : "hover:bg-surface-2"
                  }`}
                >
                  <div className="font-mono text-[12.5px] text-ink-0">
                    {model.modelId}
                  </div>
                  <div className="caption flex items-center gap-2">
                    <span>{formatCount(model.downloads)} downloads</span>
                    <span>&middot;</span>
                    <span>{formatCount(model.likes)} likes</span>
                  </div>
                </li>
              ))}
            </>
          )}
        </ul>
      )}

      {gatedWarning && (
        <p className="mt-1 font-mono text-[11px] text-warn">{gatedWarning}</p>
      )}
    </div>
  );
}
