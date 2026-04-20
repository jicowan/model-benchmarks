import type { OOMHistory } from "../types";

interface Props {
  history: OOMHistory | null;
}

export default function OOMWarning({ history }: Props) {
  if (!history || history.total_count === 0 || !history.events?.length) {
    return null;
  }

  const lastEvent = history.events[0];
  const timeAgo = lastEvent ? formatTimeAgo(new Date(lastEvent.occurred_at)) : "";

  return (
    <div className="border border-danger/40 bg-danger/5 p-4">
      <div className="flex items-start gap-3">
        <svg
          className="h-4 w-4 text-danger flex-shrink-0 mt-1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="square"
          viewBox="0 0 24 24"
        >
          <path d="M12 9v4M12 17h.01M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        </svg>
        <div className="flex-1">
          <h4 className="eyebrow text-danger mb-1.5">
            [ PREVIOUS OOM DETECTED ] {history.total_count} OCCURRENCE{history.total_count > 1 ? "S" : ""}
          </h4>
          <p className="font-mono text-[12.5px] text-ink-0">
            This model+instance combination has experienced OOM errors
            {timeAgo && ` (last: ${timeAgo})`}.
          </p>
          {lastEvent && (
            <div className="mt-2 caption text-ink-1">
              LAST CONFIG: TP={lastEvent.tensor_parallel_degree} · CONCURRENCY={lastEvent.concurrency} · MAX_LEN={lastEvent.max_model_len}
            </div>
          )}
          <p className="mt-3 eyebrow">SUGGESTIONS</p>
          <ul className="mt-1.5 font-mono text-[12px] text-ink-1 space-y-0.5">
            <li>→ Reduce concurrency (try {Math.max(1, Math.floor((lastEvent?.concurrency || 16) / 2))})</li>
            <li>→ Increase runtime overhead slider</li>
            <li>→ Reduce max_model_len</li>
          </ul>
        </div>
      </div>
    </div>
  );
}

function formatTimeAgo(date: Date): string {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

  if (diffDays === 0) return "today";
  if (diffDays === 1) return "yesterday";
  if (diffDays < 7) return `${diffDays} days ago`;
  if (diffDays < 30) return `${Math.floor(diffDays / 7)} weeks ago`;
  return `${Math.floor(diffDays / 30)} months ago`;
}
