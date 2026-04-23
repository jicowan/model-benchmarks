import { useCallback, useEffect, useMemo, useState } from "react";
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
  }, [status, modelSearch, typeFilter, sorting]);

  const handleCancel = async (id: string) => {
    if (!window.confirm(`Cancel job ${id.slice(0, 8)}?`)) return;
    try {
      await cancelRun(id);
      fetchJobs();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Unknown error");
    }
  };

  const handleDelete = async (id: string) => {
    if (!window.confirm(`Delete job ${id.slice(0, 8)}? This cannot be undone.`)) return;
    try {
      await deleteRun(id);
      fetchJobs();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Unknown error");
    }
  };

  const columns = useMemo<ColumnDef<Job>[]>(
    () => [
      {
        accessorKey: "status",
        header: "STATUS",
        size: 112,
        cell: ({ row }) => {
          const j = row.original;
          return (
            <Link to={targetFor(j)} className="flex items-center" title={j.error_message}>
              <span className={`status-dot ${statusDotClass(j.status)}`} />
              <span className="uppercase tracking-mech text-[11px]">{j.status}</span>
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
      {
        id: "actions",
        header: "",
        enableSorting: false,
        size: 80,
        cell: ({ row }) => {
          const j = row.original;
          return (
            <div className="flex gap-1 justify-end">
              {(j.status === "running" || j.status === "pending") && (
                <button
                  onClick={() => handleCancel(j.id)}
                  className="text-[11px] font-mono tracking-mech text-warn hover:text-danger px-1"
                  title="Cancel"
                >
                  STOP
                </button>
              )}
              <button
                onClick={() => handleDelete(j.id)}
                className="text-[11px] font-mono tracking-mech text-ink-2 hover:text-danger px-1"
                title="Delete"
              >
                DEL
              </button>
            </div>
          );
        },
      },
    ],
    // handleCancel/handleDelete are stable closures over fetchJobs; safe to
    // skip as deps (react-table only needs a fresh column array on data shape
    // changes).
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
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
      <PageHeader
        path={["accelbench", "runs"]}
        right={
          <Link to="/run" className="btn btn-primary">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
              <path d="M12 5v14M5 12h14" />
            </svg>
            NEW BENCHMARK
          </Link>
        }
      />

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
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
