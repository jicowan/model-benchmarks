interface Props {
  offset: number;
  pageSize: number;
  total: number;
  onOffsetChange: (next: number) => void;
  loading?: boolean;
}

// Footer for paginated tables. Matches the PREV/NEXT pattern the Runs page
// used before PRD-36; extracted so Catalog, Runs, and ModelCache share one
// style and behaviour.
export default function Pagination({
  offset,
  pageSize,
  total,
  onOffsetChange,
  loading,
}: Props) {
  const firstShown = total === 0 ? 0 : offset + 1;
  const lastShown = Math.min(offset + pageSize, total);
  const page = Math.floor(offset / pageSize) + 1;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="mt-4 flex items-center justify-between caption">
      <span>
        {loading
          ? "LOADING…"
          : total === 0
            ? "NO RESULTS"
            : `SHOWING ${firstShown}–${lastShown} OF ${total}`}
      </span>
      <div className="flex items-center gap-2">
        <button
          onClick={() => onOffsetChange(Math.max(0, offset - pageSize))}
          disabled={offset === 0 || loading}
          className="btn btn-ghost"
        >
          ← PREV
        </button>
        <span className="tabular">
          {page} / {pageCount}
        </span>
        <button
          onClick={() => onOffsetChange(offset + pageSize)}
          disabled={offset + pageSize >= total || loading}
          className="btn btn-ghost"
        >
          NEXT →
        </button>
      </div>
    </div>
  );
}
