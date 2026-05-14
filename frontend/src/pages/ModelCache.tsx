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
import {
  cancelModelCache,
  createModelCache,
  deleteModelCache,
  getModelCacheStats,
  listModelCache,
  registerCustomModel,
} from "../api";
import type { ModelCache as ModelCacheEntry, ModelCacheStats } from "../types";
import ModelCombobox from "../components/ModelCombobox";
import Pagination from "../components/Pagination";
import { PAGE_SIZE } from "../lib/pagination";

// Map react-table column IDs to the sort keys the /model-cache endpoint
// accepts (see internal/database/model_cache.go).
const MODEL_CACHE_SORT_KEY: Record<string, string> = {
  status: "status",
  display_name: "hf_id", // no server-side column for display_name; fall back to hf_id
  hf_id: "hf_id",
  size_bytes: "size_bytes",
  cached_at: "cached_at",
};

/* ----------------------------- Utilities ----------------------------- */

function formatBytes(bytes?: number): string {
  if (!bytes) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let size = bytes;
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024;
    i++;
  }
  return `${size.toFixed(i > 1 ? 1 : 0)} ${units[i]}`;
}

function formatDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return `${d.toISOString().slice(0, 10)} ${d.toISOString().slice(11, 16)}`;
}

function statusDotClass(s: string): string {
  switch (s) {
    case "caching":
    case "pending":
      return "status-pending";
    case "cached":
      return "status-cached";
    case "failed":
      return "status-failed";
    case "deleting":
      return "status-deleting";
    default:
      return "status-completed";
  }
}

/* ------------------------- PageHeader --------------------------- */

function PageHeader({ path, right }: { path: string[]; right?: React.ReactNode }) {
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
      {right}
    </div>
  );
}

/* ----------------------------- Models ----------------------------- */

type FormMode = "none" | "cache" | "register";

export default function Models() {
  const [items, setItems] = useState<ModelCacheEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [sorting, setSorting] = useState<SortingState>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [cancelling, setCancelling] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  // PRD-35: server-side aggregate for the stat cards. The list endpoint is
  // paginated, so filtering `items` locally undercounts past page 1.
  const [stats, setStats] = useState<ModelCacheStats | null>(null);

  const [formMode, setFormMode] = useState<FormMode>("none");
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const [cacheHfId, setCacheHfId] = useState("");
  const [cacheRevision, setCacheRevision] = useState("main");
  const [cacheToken, setCacheToken] = useState("");

  const [registerS3URI, setRegisterS3URI] = useState("");
  const [registerName, setRegisterName] = useState("");

  const fetchItems = useCallback(() => {
    const colId = sorting[0]?.id;
    const sortKey = colId ? MODEL_CACHE_SORT_KEY[colId] : undefined;
    const sortDir: "asc" | "desc" | undefined = sorting[0]
      ? sorting[0].desc ? "desc" : "asc"
      : undefined;
    listModelCache({
      sort: sortKey,
      order: sortDir,
      limit: PAGE_SIZE,
      offset,
    })
      .then((resp) => {
        setItems(resp.rows);
        setTotal(resp.total);
        setLoading(false);
      })
      .catch((e) => {
        setError(e.message);
        setLoading(false);
      });
  }, [sorting, offset]);

  const fetchStats = useCallback(() => {
    getModelCacheStats().then(setStats).catch(() => setStats(null));
  }, []);

  useEffect(() => {
    fetchItems();
  }, [fetchItems]);

  useEffect(() => {
    fetchStats();
  }, [fetchStats]);

  // Reset to page 1 when sort changes.
  useEffect(() => {
    setOffset(0);
  }, [sorting]);

  // Poll while any entry on the current page is actively caching.
  useEffect(() => {
    const active = items.some((i) => i.status === "caching" || i.status === "pending");
    if (!active) return;
    const iv = setInterval(fetchItems, 10000);
    return () => clearInterval(iv);
  }, [items, fetchItems]);

  async function handleCache(e: React.FormEvent) {
    e.preventDefault();
    setFormError("");
    setSubmitting(true);
    try {
      await createModelCache({
        model_hf_id: cacheHfId,
        hf_revision: cacheRevision || undefined,
        hf_token: cacheToken || undefined,
      });
      setCacheHfId("");
      setCacheRevision("main");
      setCacheToken("");
      setFormMode("none");
      fetchItems();
      fetchStats();
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : "Failed to start caching");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRegister(e: React.FormEvent) {
    e.preventDefault();
    setFormError("");
    setSubmitting(true);
    try {
      await registerCustomModel({
        s3_uri: registerS3URI,
        display_name: registerName,
      });
      setRegisterS3URI("");
      setRegisterName("");
      setFormMode("none");
      fetchItems();
      fetchStats();
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : "Failed to register model");
    } finally {
      setSubmitting(false);
    }
  }

  const bulkCancel = async (ids: string[]) => {
    const activeIds = ids.filter((id) => {
      const item = items.find((i) => i.id === id);
      return item && (item.status === "caching" || item.status === "pending");
    });
    if (activeIds.length === 0) return;
    setCancelling((prev) => {
      const next = new Set(prev);
      activeIds.forEach((id) => next.add(id));
      return next;
    });
    const errors: string[] = [];
    for (const id of activeIds) {
      try {
        await cancelModelCache(id);
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
    fetchItems();
    fetchStats();
  };

  const bulkDelete = async (ids: string[]) => {
    const errors: string[] = [];
    for (const id of ids) {
      try {
        await deleteModelCache(id);
      } catch (e) {
        errors.push(`${id.slice(0, 8)}: ${e instanceof Error ? e.message : "failed"}`);
      }
    }
    if (errors.length > 0) setError(errors.join(" · "));
    fetchItems();
    fetchStats();
  };

  // Stat values come from the aggregate endpoint (PRD-35) so they reflect
  // the full registry even when the list below is paginated. The `caching`
  // count on the current page is still used by the auto-refresh loop, since
  // that check only needs to know "is anything on-screen actively caching".
  const pageCaching = items.filter((i) => i.status === "caching" || i.status === "pending");

  const columns = useMemo<ColumnDef<ModelCacheEntry>[]>(
    () => [
      {
        id: "select",
        header: () => {
          const onPage = items;
          const allSelected = onPage.length > 0 && onPage.every((i) => selected.has(i.id));
          const anySelected = onPage.some((i) => selected.has(i.id));
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
                  if (e.target.checked) onPage.forEach((i) => copy.add(i.id));
                  else onPage.forEach((i) => copy.delete(i.id));
                  return copy;
                });
              }}
            />
          );
        },
        enableSorting: false,
        size: 32,
        cell: ({ row }) => {
          const item = row.original;
          const checked = selected.has(item.id);
          return (
            <input
              type="checkbox"
              aria-label={`select ${item.display_name}`}
              checked={checked}
              onChange={(e) => {
                setSelected((prev) => {
                  const copy = new Set(prev);
                  if (e.target.checked) copy.add(item.id);
                  else copy.delete(item.id);
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
        size: 140,
        cell: ({ row }) => {
          const item = row.original;
          const isCancelling = cancelling.has(item.id);
          const label = isCancelling && (item.status === "caching" || item.status === "pending")
            ? "cancelling"
            : item.status;
          return (
            <>
              <div className="flex items-center">
                <span className={`status-dot ${statusDotClass(item.status)}`} />
                <span className="uppercase tracking-mech text-[11px]">{label}</span>
              </div>
              {item.status === "failed" && item.error_message && (
                <p className="text-[10.5px] text-danger mt-1 max-w-xs truncate" title={item.error_message}>
                  {item.error_message}
                </p>
              )}
            </>
          );
        },
      },
      {
        accessorKey: "display_name",
        header: "NAME",
        cell: ({ getValue }) => (
          <div className="text-ink-0 truncate max-w-[280px]">{getValue<string>()}</div>
        ),
      },
      {
        accessorKey: "hf_id",
        header: "HF ID",
        cell: ({ getValue }) => {
          const v = getValue<string | undefined>();
          return (
            <span className="path text-ink-1">
              {v || <span className="text-ink-2 italic">CUSTOM</span>}
            </span>
          );
        },
      },
      {
        accessorKey: "s3_uri",
        header: "S3 URI",
        enableSorting: false,
        cell: ({ getValue }) => {
          const v = getValue<string>();
          return (
            <span className="path text-ink-1 truncate max-w-[360px] block" title={v}>
              {v}
            </span>
          );
        },
      },
      {
        accessorKey: "size_bytes",
        header: "SIZE",
        size: 80,
        cell: ({ getValue }) => (
          <span className="num text-ink-1">{formatBytes(getValue<number | undefined>())}</span>
        ),
      },
      {
        accessorKey: "cached_at",
        header: "CACHED",
        size: 176,
        cell: ({ getValue }) => (
          <span className="text-ink-2 text-[11.5px]">{formatDate(getValue<string | undefined>())}</span>
        ),
      },
      {
        id: "actions",
        header: "",
        enableSorting: false,
        size: 72,
        cell: ({ row }) => {
          const item = row.original;
          if (!item.hf_id) return null;
          return (
            <div className="flex justify-end">
              <Link
                to={`/run?model=${encodeURIComponent(item.hf_id)}`}
                className="text-[11px] font-mono tracking-mech text-signal hover:underline"
              >
                RUN →
              </Link>
            </div>
          );
        },
      },
    ],
    // Rebuild columns when items, selected, or cancelling change so the
    // header checkbox and per-row status text reflect current state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [items, selected, cancelling],
  );

  const table = useReactTable({
    data: items,
    columns,
    manualSorting: true, // rows are ordered by the API
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  return (
    <>
      <PageHeader path={["accelbench", "models"]} />

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
        {/* Intro / info + action buttons aligned with the intro paragraph */}
        <div className="mb-8">
          <div className="eyebrow mb-3">MODEL REGISTRY</div>
          <h1 className="font-sans text-[28px] leading-tight tracking-[-0.01em] max-w-2xl text-balance mb-3">
            Cache HuggingFace weights to S3 or register custom models for benchmarking.
          </h1>
          <div className="flex items-center justify-between gap-6 flex-wrap">
            <p className="meta max-w-xl">
              Cached and registered models become available for benchmark runs. Loading from S3 via
              Run:ai streamer is typically 10–20× faster than pulling from HuggingFace.
            </p>
            <div className="flex items-center gap-3 shrink-0">
              <button
                onClick={() => { setFormMode(formMode === "register" ? "none" : "register"); setFormError(""); }}
                className={`btn ${formMode === "register" ? "bg-surface-2" : ""}`}
              >
                REGISTER S3 MODEL
              </button>
              <button
                onClick={() => { setFormMode(formMode === "cache" ? "none" : "cache"); setFormError(""); }}
                className={`btn btn-primary ${formMode === "cache" ? "!bg-signal/20" : ""}`}
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
                  <path d="M12 5v14M5 12h14" />
                </svg>
                CACHE FROM HUGGINGFACE
              </button>
            </div>
          </div>
        </div>

        {/* Stats — driven by the server-side aggregate (PRD-35). */}
        <div className="grid grid-cols-4 border-l border-t border-line mb-8">
          <StatCell
            label="TOTAL"
            value={!stats ? "—" : stats.total}
            sub="registered"
            index="01"
          />
          <StatCell
            label="CACHED"
            value={!stats ? "—" : stats.cached}
            sub={stats ? `${formatBytes(stats.total_bytes)} in S3` : "—"}
            index="02"
            accent="signal"
          />
          <StatCell
            label="CACHING"
            value={!stats ? "—" : stats.caching}
            sub={stats && stats.caching > 0 ? "In progress" : "Idle"}
            accent={stats && stats.caching > 0 ? "warn" : undefined}
            index="03"
          />
          <StatCell
            label="FAILED"
            value={!stats ? "—" : stats.failed}
            sub={stats && stats.failed > 0 ? "Review below" : "—"}
            accent={stats && stats.failed > 0 ? "danger" : undefined}
            index="04"
          />
        </div>

        {/* Inline forms */}
        {formMode === "cache" && (
          <div className="mb-6 panel p-5 animate-enter">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-baseline gap-3">
                <span className="eyebrow">[ NEW CACHE JOB ]</span>
                <h3 className="font-mono text-[13px] tracking-mech text-ink-0">Download a HuggingFace model to S3</h3>
              </div>
              <button onClick={() => setFormMode("none")} className="btn btn-ghost">✕ CLOSE</button>
            </div>
            <form onSubmit={handleCache} className="space-y-3">
              <div>
                <label className="eyebrow block mb-1.5">MODEL ID</label>
                <ModelCombobox value={cacheHfId} onChange={setCacheHfId} hfToken={cacheToken} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="eyebrow block mb-1.5">REVISION</label>
                  <input
                    type="text"
                    value={cacheRevision}
                    onChange={(e) => setCacheRevision(e.target.value)}
                    placeholder="main"
                    className="input w-full"
                  />
                </div>
                <div>
                  <label className="eyebrow block mb-1.5">HF TOKEN <span className="text-ink-2 normal-case">(optional, overrides platform default)</span></label>
                  <input
                    type="password"
                    value={cacheToken}
                    onChange={(e) => setCacheToken(e.target.value)}
                    placeholder="Uses platform token — leave blank for default"
                    className="input w-full"
                  />
                </div>
              </div>
              {formError && (
                <div className="text-danger font-mono text-[12px]">{formError}</div>
              )}
              <div className="flex gap-2 pt-2">
                <button type="submit" disabled={submitting || !cacheHfId} className="btn btn-primary">
                  {submitting ? "STARTING…" : "▶ START CACHING"}
                </button>
                <button type="button" onClick={() => setFormMode("none")} className="btn btn-ghost">
                  CANCEL
                </button>
              </div>
            </form>
          </div>
        )}

        {formMode === "register" && (
          <div className="mb-6 panel p-5 animate-enter">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-baseline gap-3">
                <span className="eyebrow">[ REGISTER ]</span>
                <h3 className="font-mono text-[13px] tracking-mech text-ink-0">Register an existing S3 model</h3>
              </div>
              <button onClick={() => setFormMode("none")} className="btn btn-ghost">✕ CLOSE</button>
            </div>
            <form onSubmit={handleRegister} className="space-y-3">
              <div>
                <label className="eyebrow block mb-1.5">S3 URI</label>
                <input
                  type="text"
                  value={registerS3URI}
                  onChange={(e) => setRegisterS3URI(e.target.value)}
                  placeholder="s3://bucket/models/my-model"
                  className="input w-full"
                  required
                />
              </div>
              <div>
                <label className="eyebrow block mb-1.5">DISPLAY NAME</label>
                <input
                  type="text"
                  value={registerName}
                  onChange={(e) => setRegisterName(e.target.value)}
                  placeholder="My Fine-tuned Llama"
                  className="input w-full"
                  required
                />
              </div>
              {formError && (
                <div className="text-danger font-mono text-[12px]">{formError}</div>
              )}
              <div className="flex gap-2 pt-2">
                <button type="submit" disabled={submitting || !registerS3URI || !registerName} className="btn btn-primary">
                  {submitting ? "REGISTERING…" : "▶ REGISTER"}
                </button>
                <button type="button" onClick={() => setFormMode("none")} className="btn btn-ghost">
                  CANCEL
                </button>
              </div>
            </form>
          </div>
        )}

        {error && (
          <div className="mb-4 border border-danger/50 bg-danger/5 p-3 font-mono text-[12px] text-danger">
            ERROR: {error}
          </div>
        )}

        {/* Registry table */}
        <div className="panel overflow-x-auto">
          <div className="flex items-center justify-between px-4 h-11 border-b border-line">
            <div className="flex items-baseline gap-3">
              <span className="eyebrow">[ REGISTRY ]</span>
              <span className="font-mono text-[12px] text-ink-1">
                {loading ? "loading…" : `${total} entries`}
              </span>
            </div>
            <BulkActions
              selected={selected}
              items={items}
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
                          canSort ? "cursor-pointer select-none hover:text-ink-0 transition-colors" : ""
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
                  <td colSpan={8} className="text-center py-12 caption">
                    <span className="inline-flex items-center gap-2">
                      <span className="w-1.5 h-1.5 bg-signal animate-pulse_signal" />
                      LOADING…
                    </span>
                  </td>
                </tr>
              ) : items.length === 0 ? (
                <tr>
                  <td colSpan={8} className="text-center py-16 caption">
                    <div className="mb-3">NO MODELS REGISTERED</div>
                    <button onClick={() => setFormMode("cache")} className="btn btn-primary">
                      CACHE FIRST MODEL
                    </button>
                  </td>
                </tr>
              ) : (
                table.getRowModel().rows.map((row) => (
                  <tr key={row.id}>
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

        {pageCaching.length > 0 && (
          <div className="mt-2 flex justify-end caption">
            <span className="flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 bg-warn animate-pulse_signal" />
              AUTO-REFRESH WHILE CACHING
            </span>
          </div>
        )}
      </div>
    </>
  );
}

function BulkActions({
  selected,
  items,
  onCancel,
  onDelete,
}: {
  selected: Set<string>;
  items: ModelCacheEntry[];
  onCancel: (ids: string[]) => Promise<void>;
  onDelete: (ids: string[]) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

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
  const selectedItems = items.filter((i) => selected.has(i.id));
  const count = selectedIds.length;
  const cancellableCount = selectedItems.filter(
    (i) => i.status === "caching" || i.status === "pending"
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
                ? "None of the selected rows are caching or pending"
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
              Delete {count} model{count === 1 ? "" : "s"}?
            </h3>
            <p className="caption mb-4">
              This cannot be undone. Active caches will be cancelled first, then S3 objects removed.
            </p>
            <ul className="mb-4 max-h-40 overflow-y-auto font-mono text-[12px] text-ink-1">
              {selectedItems.map((i) => (
                <li key={i.id} className="truncate">
                  {i.display_name}
                </li>
              ))}
              {count > selectedItems.length && (
                <li className="caption text-ink-2">
                  …and {count - selectedItems.length} more on other pages
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

function StatCell({
  label,
  value,
  sub,
  accent,
  index,
}: {
  label: string;
  value: string | number;
  sub?: string;
  accent?: "signal" | "warn" | "danger";
  index: string;
}) {
  const accentClass =
    accent === "signal"
      ? "text-signal"
      : accent === "warn"
      ? "text-warn"
      : accent === "danger"
      ? "text-danger"
      : "text-ink-0";
  return (
    <div className="p-5 border-r border-b border-line bg-surface-1">
      <div className="flex items-start justify-between mb-3">
        <span className="eyebrow">{label}</span>
        <span className="font-mono text-[10px] tracking-widemech text-ink-2">{index}</span>
      </div>
      <div className={`font-mono text-[32px] leading-none tabular ${accentClass}`}>{value}</div>
      {sub && <div className="meta mt-2">{sub}</div>}
    </div>
  );
}
