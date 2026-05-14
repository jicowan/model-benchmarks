import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { listJobs, cancelRun, deleteRun } from "../api";
import type { Job } from "../types";
import Pagination from "../components/Pagination";
import { PAGE_SIZE } from "../lib/pagination";

const STATUSES = ["all", "running", "pending", "completed", "failed"] as const;
type StatusFilter = (typeof STATUSES)[number];
type TypeFilter = "all" | "run" | "suite";

// Map react-table column IDs to the sort keys /api/v1/jobs accepts
// (see internal/database/jobs.go:jobsAllowedSortColumns).
const JOBS_SORT_KEY: Record<string, string> = {
  status: "status",
  model_hf_id: "model",
  instance_type_name: "instance",
  duration: "duration",
  created_at: "created_at",
};

/* --------------------------- Utilities --------------------------- */

function formatDuration(item: Job): string {
  if (!item.started_at) return "—";
  const start = new Date(item.started_at).getTime();
  const end = item.completed_at
    ? new Date(item.completed_at).getTime()
    : Date.now();
  const secs = Math.round((end - start) / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  return `${mins}m${remSecs.toString().padStart(2, "0")}s`;
}

function timeAgo(iso: string): string {
  const sec = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  const min = sec / 60;
  if (min < 60) return `${Math.floor(min)}m ago`;
  const hr = min / 60;
  if (hr < 24) return `${Math.floor(hr)}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

function statusDotClass(s: string): string {
  return "status-" + (s === "pending" ? "pending" : s);
}

function targetFor(j: Job): string {
  return j.type === "suite" ? `/suite-runs/${j.id}` : `/results/${j.id}`;
}

/* ------------------------- PageHeader --------------------------- */

function PageHeader({
  path,
  right,
}: {
  path: string[];
  right?: React.ReactNode;
}) {
  return (
    <div className="h-14 border-b border-line flex items-center justify-between px-6 bg-surface-0 sticky top-0 z-20">
      <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
        {path.map((p, i) => (
          <span key={i} className="flex items-center gap-2">
            <span className="text-ink-2">{i === 0 ? "" : "/"}</span>
            <span className={i === path.length - 1 ? "text-ink-0" : "text-ink-1"}>
              {p}
            </span>
          </span>
        ))}
      </div>
      {right && <div>{right}</div>}
    </div>
  );
}

/* ----------------------------- Runs ----------------------------- */

export default function Runs() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [sorting, setSorting] = useState<SortingState>([
    { id: "created_at", desc: true },
  ]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [modelSearch, setModelSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("all");
  // Rows on which the user has clicked STOP. The API returns 202 immediately
  // (PRD-40 sets a DB flag; the owning pod's cancel poller terminates the
  // goroutine within ~5s), so without this local tracking the row still
  // shows status="running" right after the click and users think STOP
  // didn't work. We show "CANCELLING…" for these rows until the next
  // listJobs response reflects the terminal status.
  const [cancelling, setCancelling] = useState<Set<string>>(new Set());
  // Bulk selection (survives pagination by design — the user can select
  // rows on page 1, navigate, and the selection sticks until actions are
  // applied or a filter changes).
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const fetchJobs = useCallback(async () => {
    setLoading(true);
    setError("");
    const colId = sorting[0]?.id;
    const sortKey = colId ? JOBS_SORT_KEY[colId] : undefined;
    const sortDir: "asc" | "desc" | undefined = sorting[0]
      ? sorting[0].desc ? "desc" : "asc"
      : undefined;
    try {
      const resp = await listJobs({
        type: typeFilter === "all" ? undefined : typeFilter,
        status: status === "all" ? undefined : status,
        model: modelSearch || undefined,
        sort: sortKey,
        order: sortDir,
        limit: PAGE_SIZE,
        offset,
      });
      setJobs(resp.rows);
      setTotal(resp.total);
      // Clear any "cancelling" markers whose rows have reached a terminal
      // state on the server. The marker is purely a UX hint while the
      // owner pod processes the DB flag; once the server reports the
      // real status we defer to that.
      setCancelling((prev) => {
        if (prev.size === 0) return prev;
        const next = new Set(prev);
        for (const j of resp.rows) {
          if (j.status !== "running" && j.status !== "pending") {
            next.delete(j.id);
          }
        }
        return next;
      });
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Unknown error");
    } finally {
      setLoading(false);
    }
  }, [status, modelSearch, typeFilter, sorting, offset]);

  useEffect(() => {
    fetchJobs();
  }, [fetchJobs]);

  // Reset to page 1 whenever a filter or sort changes.
  useEffect(() => {
    setOffset(0);
    // Clear selection when the result set changes meaningfully. Rows that
    // scroll out of view stay in `selected` via the "survives pagination"
    // design, but a new filter means the user's intent has shifted.
    setSelected(new Set());
  }, [status, modelSearch, typeFilter, sorting]);

  // Bulk cancel: only running/pending jobs can actually cancel; other
  // selections are silently skipped (backend would reject them anyway).
  const bulkCancel = async (ids: string[]) => {
    const activeIds = ids.filter((id) => {
      const j = jobs.find((x) => x.id === id);
      return j && (j.status === "running" || j.status === "pending");
    });
    if (activeIds.length === 0) return;
    // Optimistic markers so rows flip to CANCELLING… immediately.
    setCancelling((prev) => {
      const next = new Set(prev);
      activeIds.forEach((id) => next.add(id));
      return next;
    });
    const errors: string[] = [];
    for (const id of activeIds) {
      try {
        await cancelRun(id);
      } catch (e) {
        errors.push(`${id.slice(0, 8)}: ${e instanceof Error ? e.message : "failed"}`);
        setCancelling((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    }
    if (errors.length > 0) setError(errors.join(" · "));
    fetchJobs();
    // Owner pod's cancel poller runs every 5s. Refresh once ~7s later
    // so the terminal status shows up without requiring another click.
    setTimeout(() => fetchJobs(), 7000);
  };

  const bulkDelete = async (ids: string[]) => {
    const errors: string[] = [];
    for (const id of ids) {
      try {
        await deleteRun(id);
      } catch (e) {
        errors.push(`${id.slice(0, 8)}: ${e instanceof Error ? e.message : "failed"}`);
      }
    }
    if (errors.length > 0) setError(errors.join(" · "));
    fetchJobs();
  };

  const columns = useMemo<ColumnDef<Job>[]>(
    () => [
      {
        id: "select",
        header: () => {
          const onPage = jobs;
          const allSelected = onPage.length > 0 && onPage.every((j) => selected.has(j.id));
          const anySelected = onPage.some((j) => selected.has(j.id));
          return (
            <input
              type="checkbox"
              aria-label="select all on this page"
              checked={allSelected}
              ref={(el) => {
                if (!el) return;
                el.indeterminate = anySelected && !allSelected;
              }}
              onChange={(e) => {
                setSelected((prev) => {
                  const copy = new Set(prev);
                  if (e.target.checked) onPage.forEach((j) => copy.add(j.id));
                  else onPage.forEach((j) => copy.delete(j.id));
                  return copy;
                });
              }}
            />
          );
        },
        enableSorting: false,
        size: 32,
        cell: ({ row }) => {
          const j = row.original;
          const checked = selected.has(j.id);
          return (
            <input
              type="checkbox"
              aria-label={`select run ${j.id.slice(0, 8)}`}
              checked={checked}
              onChange={(e) => {
                setSelected((prev) => {
                  const copy = new Set(prev);
                  if (e.target.checked) copy.add(j.id);
                  else copy.delete(j.id);
                  return copy;
                });
              }}
            />
          );
        },
      },
      {
        accessorKey: "status",
        header: "STATUS",
        size: 112,
        cell: ({ row }) => {
          const j = row.original;
          const isCancelling = cancelling.has(j.id);
          const label = isCancelling && (j.status === "running" || j.status === "pending")
            ? "cancelling"
            : j.status;
          return (
            <Link to={targetFor(j)} className="flex items-center" title={j.error_message}>
              <span className={`status-dot ${statusDotClass(j.status)}`} />
              <span className="uppercase tracking-mech text-[11px]">{label}</span>
            </Link>
          );
        },
      },
      {
        id: "type",
        header: "TYPE",
        size: 56,
        enableSorting: false,
        cell: ({ row }) => {
          const j = row.original;
          return (
            <span
              className={`font-mono text-[10px] tracking-widemech px-1.5 py-0.5 border ${
                j.type === "suite"
                  ? "border-info/40 text-info"
                  : "border-line-strong text-ink-1"
              }`}
            >
              {j.type === "suite" ? "SUITE" : "RUN"}
            </span>
          );
        },
      },
      {
        id: "id_short",
        header: "ID",
        size: 80,
        enableSorting: false,
        cell: ({ row }) => {
          const j = row.original;
          return (
            <Link to={targetFor(j)} className="path hover:text-signal">
              {j.id.slice(0, 8)}
            </Link>
          );
        },
      },
      {
        accessorKey: "model_hf_id",
        header: "MODEL",
        cell: ({ row }) => {
          const j = row.original;
          return (
            <div className="flex flex-col min-w-0">
              <Link to={targetFor(j)} className="path truncate max-w-[320px] hover:text-signal">
                {j.model_hf_id}
              </Link>
              {j.error_message && (
                <span className="text-[10.5px] text-danger truncate max-w-[320px]" title={j.error_message}>
                  {j.error_message}
                </span>
              )}
            </div>
          );
        },
      },
      {
        accessorKey: "instance_type_name",
        header: "INSTANCE",
        cell: ({ getValue }) => (
          <span className="text-ink-1">{getValue<string>()}</span>
        ),
      },
      {
        id: "duration",
        header: "DURATION",
        size: 96,
        cell: ({ row }) => (
          <span className="num text-ink-1">{formatDuration(row.original)}</span>
        ),
      },
      {
        accessorKey: "created_at",
        header: "AGE",
        size: 96,
        cell: ({ getValue }) => (
          <span className="text-ink-2">{timeAgo(getValue<string>())}</span>
        ),
      },
    ],
    // Rebuild columns when jobs, selected, or cancelling change so the
    // header checkbox and per-row status text reflect current state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [jobs, selected, cancelling],
  );

  const table = useReactTable({
    data: jobs,
    columns,
    manualSorting: true, // server-side sort
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  return (
    <>
      <PageHeader path={["accelbench", "runs"]} />

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex-1" />
          <Link to="/run" className="btn btn-primary">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
              <path d="M12 5v14M5 12h14" />
            </svg>
            NEW BENCHMARK
          </Link>
        </div>
        {/* Filter strip */}
        <div className="mb-6 panel">
          <div className="flex items-center border-b border-line">
            {/* Status tabs. Counts are no longer shown because the Runs feed
                is server-paginated and we don't have a per-status count for
                free; the Dashboard owns those stats (see PRD-35). */}
            <div className="flex">
              {STATUSES.map((s) => (
                <button
                  key={s}
                  onClick={() => setStatus(s)}
                  className={`h-11 px-5 font-mono text-[11.5px] tracking-mech uppercase border-r border-line flex items-center gap-2 transition-colors ${
                    status === s
                      ? "text-ink-0 bg-surface-2"
                      : "text-ink-1 hover:text-ink-0 hover:bg-surface-2/60"
                  }`}
                >
                  {s !== "all" && <span className={`status-dot ${statusDotClass(s)}`} />}
                  <span>{s}</span>
                </button>
              ))}
            </div>

            {/* Search */}
            <div className="flex-1 relative">
              <span className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-2 pointer-events-none">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
                  <circle cx="11" cy="11" r="7" />
                  <path d="M21 21l-4.35-4.35" />
                </svg>
              </span>
              <input
                type="text"
                value={modelSearch}
                onChange={(e) => setModelSearch(e.target.value)}
                placeholder="filter by model id…"
                className="w-full h-11 pl-9 pr-3 bg-transparent font-mono text-[12px] tracking-mech text-ink-0 placeholder:text-ink-2 focus:outline-none focus:bg-surface-2"
              />
            </div>

            {/* Type filter */}
            <div className="flex border-l border-line">
              {(["all", "run", "suite"] as const).map((t) => (
                <button
                  key={t}
                  onClick={() => setTypeFilter(t)}
                  className={`h-11 px-4 font-mono text-[11px] tracking-mech uppercase border-r border-line last:border-r-0 transition-colors ${
                    typeFilter === t
                      ? "text-ink-0 bg-surface-2"
                      : "text-ink-1 hover:text-ink-0 hover:bg-surface-2/60"
                  }`}
                >
                  {t === "all" ? "all types" : t}
                </button>
              ))}
            </div>
          </div>
        </div>

        {error && (
          <div className="mb-4 border border-danger/50 bg-danger/5 p-3 font-mono text-[12px] text-danger">
            ERROR: {error}
          </div>
        )}

        {/* Table */}
        <div className="panel overflow-x-auto">
          <div className="flex items-center justify-between px-4 h-11 border-b border-line">
            <div className="flex items-baseline gap-3">
              <span className="eyebrow">[ RUNS ]</span>
              <span className="font-mono text-[12px] text-ink-1">
                {loading ? "loading…" : `${total} entries`}
              </span>
            </div>
            <BulkActions
              selected={selected}
              jobs={jobs}
              onCancel={async (ids) => {
                await bulkCancel(ids);
              }}
              onDelete={async (ids) => {
                await bulkDelete(ids);
                setSelected(new Set());
              }}
            />
          </div>
          <table className="data-table">
            <thead>
              {table.getHeaderGroups().map((hg) => (
                <tr key={hg.id}>
                  {hg.headers.map((header) => {
                    const canSort = header.column.getCanSort();
                    return (
                      <th
                        key={header.id}
                        onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                        className={`eyebrow text-left py-2 px-3 ${
                          canSort
                            ? "cursor-pointer select-none hover:text-ink-0 transition-colors"
                            : ""
                        }`}
                        style={{ width: header.getSize() }}
                      >
                        <div className="flex items-center gap-1">
                          {flexRender(header.column.columnDef.header, header.getContext())}
                          {canSort &&
                            ({ asc: " ^", desc: " v" }[header.column.getIsSorted() as string] ?? "")}
                        </div>
                      </th>
                    );
                  })}
                </tr>
              ))}
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={columns.length} className="text-center py-12 caption">
                    <span className="inline-flex items-center gap-2">
                      <span className="w-1.5 h-1.5 bg-signal animate-pulse_signal" />
                      LOADING…
                    </span>
                  </td>
                </tr>
              ) : jobs.length === 0 ? (
                <tr>
                  <td colSpan={columns.length} className="text-center py-16 caption">
                    NO RUNS MATCH CURRENT FILTERS
                  </td>
                </tr>
              ) : (
                table.getRowModel().rows.map((row) => (
                  <tr key={`${row.original.type}-${row.original.id}`}>
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id} className="py-2.5 px-3">
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        <Pagination
          offset={offset}
          pageSize={PAGE_SIZE}
          total={total}
          onOffsetChange={setOffset}
          loading={loading}
        />
      </div>
    </>
  );
}

/* --------------------------- BulkActions --------------------------- */

// BulkActions renders a "split-button-lookalike" Actions dropdown next to
// + NEW BENCHMARK. Mirrors the Users page pattern (see Users.tsx:BulkActions).
// Selected IDs are collected across pages; we resolve them against the
// current-page `jobs` list so we can report counts and exclude already-
// terminal rows from the Cancel action.
function BulkActions({
  selected,
  jobs,
  onCancel,
  onDelete,
}: {
  selected: Set<string>;
  jobs: Job[];
  onCancel: (ids: string[]) => Promise<void>;
  onDelete: (ids: string[]) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Click-outside + Esc close the dropdown.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const selectedIds = Array.from(selected);
  const selectedJobs = jobs.filter((j) => selected.has(j.id));
  const count = selectedIds.length;
  const cancellableCount = selectedJobs.filter(
    (j) => j.status === "running" || j.status === "pending"
  ).length;
  const disabled = busy || count === 0;

  const runCancel = async () => {
    setOpen(false);
    if (cancellableCount === 0) return;
    setBusy(true);
    try {
      await onCancel(selectedIds);
    } finally {
      setBusy(false);
    }
  };

  const runDelete = async () => {
    setBusy(true);
    try {
      await onDelete(selectedIds);
      setConfirmDelete(false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        className={[
          "inline-flex items-stretch h-8 font-mono text-[12px] tracking-mech",
          "border bg-surface-1 text-ink-0 transition-colors",
          "hover:bg-surface-2 hover:border-line-strong",
          "disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-surface-1 disabled:hover:border-line",
          "focus:outline-none focus:ring-1 focus:ring-signal/60",
          open ? "border-signal/60 ring-1 ring-signal/40" : "border-line",
        ].join(" ")}
      >
        <span className="flex items-center gap-1 px-3">
          ACTIONS
          {count > 0 && <span className="text-ink-2">({count})</span>}
        </span>
        <span className="flex items-center px-2 border-l border-line">
          <svg
            width="10"
            height="10"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="square"
            className={`transition-transform ${open ? "rotate-180" : ""}`}
          >
            <path d="M6 9l6 6 6-6" />
          </svg>
        </span>
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 top-full mt-1 z-30 min-w-[220px] bg-surface-1 border border-line shadow-lg font-mono text-[12px] tracking-mech"
        >
          <button
            type="button"
            onClick={runCancel}
            disabled={cancellableCount === 0}
            className="block w-full text-left px-3 py-2 text-ink-0 hover:bg-surface-2 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-transparent"
            title={
              cancellableCount === 0
                ? "None of the selected rows are running or pending"
                : undefined
            }
          >
            Cancel selected
            {cancellableCount !== count && cancellableCount > 0 && (
              <span className="text-ink-2 ml-1">({cancellableCount} active)</span>
            )}
          </button>
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              setConfirmDelete(true);
            }}
            className="block w-full text-left px-3 py-2 text-danger hover:bg-surface-2 border-t border-line"
          >
            Delete selected…
          </button>
        </div>
      )}
      {confirmDelete && (
        <div className="fixed inset-0 bg-surface-0/80 z-40 flex items-center justify-center p-6">
          <div className="bg-surface-1 border border-line w-full max-w-md p-6">
            <h3 className="font-sans text-[16px] tracking-mech mb-3">
              Delete {count} run{count === 1 ? "" : "s"}?
            </h3>
            <p className="caption mb-4">
              This cannot be undone. Active runs will be cancelled first.
            </p>
            <ul className="mb-4 max-h-40 overflow-y-auto font-mono text-[12px] text-ink-1">
              {selectedJobs.map((j) => (
                <li key={j.id} className="truncate">
                  {j.id.slice(0, 8)} · {j.model_hf_id}
                </li>
              ))}
              {count > selectedJobs.length && (
                <li className="caption text-ink-2">
                  …and {count - selectedJobs.length} more on other pages
                </li>
              )}
            </ul>
            <div className="flex justify-end gap-2">
              <button
                onClick={() => setConfirmDelete(false)}
                className="btn"
                type="button"
                disabled={busy}
              >
                CANCEL
              </button>
              <button
                onClick={runDelete}
                disabled={busy}
                className="btn btn-primary disabled:opacity-40"
                type="button"
              >
                {busy ? "DELETING…" : "DELETE"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
