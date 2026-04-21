import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { useNavigate } from "react-router-dom";
import { listCatalog, seedCatalog, getCatalogSeedStatus } from "../api";
import type { CatalogEntry, CatalogFilter, CatalogSeedStatus } from "../types";
import FilterBar from "../components/FilterBar";

function fmtNum(v: number | undefined, d = 1): string {
  return v != null ? v.toFixed(d) : "--";
}

export default function Catalog() {
  const [data, setData] = useState<CatalogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [sorting, setSorting] = useState<SortingState>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [seedStatus, setSeedStatus] = useState<CatalogSeedStatus["status"]>("none");
  const [seedError, setSeedError] = useState<string | null>(null);
  const [seedFlash, setSeedFlash] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const navigate = useNavigate();

  const fetchData = useCallback((filter: CatalogFilter = {}) => {
    setLoading(true);
    listCatalog({ ...filter, limit: 500 })
      .then(setData)
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const startPolling = useCallback(() => {
    stopPolling();
    pollRef.current = setInterval(async () => {
      try {
        const s = await getCatalogSeedStatus();
        setSeedStatus(s.status);
        if (s.status === "succeeded") {
          stopPolling();
          setSeedFlash(true);
          fetchData();
          setTimeout(() => setSeedFlash(false), 3000);
        } else if (s.status === "failed") {
          stopPolling();
          setSeedError("Seed job failed");
          setTimeout(() => setSeedError(null), 5000);
        }
      } catch {
        stopPolling();
      }
    }, 5000);
  }, [stopPolling, fetchData]);

  const handleSeed = async () => {
    setSeedError(null);
    try {
      await seedCatalog();
      setSeedStatus("active");
      startPolling();
    } catch (err: any) {
      setSeedError(err.message || "Failed to start seed job");
      setTimeout(() => setSeedError(null), 5000);
    }
  };

  useEffect(() => {
    fetchData();
    // Check if a seed is already running on mount.
    getCatalogSeedStatus().then((s) => {
      setSeedStatus(s.status);
      if (s.status === "active") startPolling();
    }).catch(() => {});
    return stopPolling;
  }, [fetchData, startPolling, stopPolling]);

  const columns = useMemo<ColumnDef<CatalogEntry>[]>(
    () => [
      {
        id: "select",
        header: "",
        cell: ({ row }) => (
          <input
            type="checkbox"
            checked={selected.has(row.original.run_id)}
            className="accent-signal"
            onClick={(e) => e.stopPropagation()}
            onChange={() => {
              setSelected((prev) => {
                const next = new Set(prev);
                if (next.has(row.original.run_id)) {
                  next.delete(row.original.run_id);
                } else if (next.size < 4) {
                  next.add(row.original.run_id);
                }
                return next;
              });
            }}
          />
        ),
        enableSorting: false,
        size: 40,
      },
      {
        accessorKey: "model_hf_id",
        header: "Model",
        cell: (info) => (
          <span className="font-medium">{info.getValue<string>()}</span>
        ),
      },
      {
        accessorKey: "instance_type_name",
        header: "Instance",
      },
      {
        accessorKey: "accelerator_name",
        header: "Accelerator",
        cell: (info) => {
          const row = info.row.original;
          return `${row.accelerator_count}x ${info.getValue<string>()}`;
        },
      },
      {
        accessorKey: "ttft_p50_ms",
        header: "TTFT p50",
        cell: (info) => fmtNum(info.getValue<number>()),
        size: 90,
      },
      {
        accessorKey: "ttft_p99_ms",
        header: "TTFT p99",
        cell: (info) => fmtNum(info.getValue<number>()),
        size: 90,
      },
      {
        accessorKey: "e2e_latency_p50_ms",
        header: "E2E p50",
        cell: (info) => fmtNum(info.getValue<number>()),
        size: 90,
      },
      {
        accessorKey: "itl_p50_ms",
        header: "ITL p50",
        cell: (info) => fmtNum(info.getValue<number>()),
        size: 90,
      },
      {
        accessorKey: "throughput_aggregate_tps",
        header: "Throughput",
        cell: (info) => fmtNum(info.getValue<number>(), 0),
        size: 100,
      },
      {
        accessorKey: "requests_per_second",
        header: "RPS",
        cell: (info) => fmtNum(info.getValue<number>(), 2),
        size: 80,
      },
      {
        accessorKey: "accelerator_utilization_avg_pct",
        header: "Busy % (avg)",
        cell: (info) =>
          fmtNum(
            info.getValue<number>() ??
              info.row.original.accelerator_utilization_pct,
            0
          ),
        size: 80,
      },
      {
        accessorKey: "sm_active_avg_pct",
        header: "SM % (avg)",
        cell: (info) => fmtNum(info.getValue<number>(), 0),
        size: 80,
      },
    ],
    [selected]
  );

  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  return (
    <>
      <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
        <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
          <span className="text-ink-1">accelbench</span>
          <span className="text-ink-2">/</span>
          <span className="text-ink-0">catalog</span>
        </div>
      </div>
      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
      <div className="flex items-center justify-between mb-4">
        <div>
          <div className="eyebrow mb-2">BENCHMARK RESULTS INDEX</div>
          <h1 className="font-sans text-[22px] leading-tight tracking-[-0.01em]">Catalog</h1>
        </div>
        <div className="flex items-center gap-3">
          {seedFlash && (
            <span className="font-mono text-[11.5px] tracking-mech uppercase text-signal">Seed complete</span>
          )}
          {seedError && (
            <span className="font-mono text-[11.5px] text-danger">{seedError}</span>
          )}
          <button
            onClick={handleSeed}
            disabled={seedStatus === "active"}
            className="btn btn-primary"
          >
            {seedStatus === "active" ? (
              <span className="flex items-center gap-2">
                <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                Seeding...
              </span>
            ) : (
              "Seed Catalog"
            )}
          </button>
        </div>
      </div>

      <FilterBar onFilter={fetchData} />

      {/* Selection bar — appears when rows are selected */}
      {selected.size > 0 && (
        <div className="panel mb-4">
          <div className="flex items-center justify-between px-4 py-2.5 bg-signal/5 border-signal/30">
            <div className="flex items-center gap-3 font-mono text-[12px]">
              <span className="text-signal">{selected.size} SELECTED</span>
              <button
                onClick={() => setSelected(new Set())}
                className="btn-ghost text-[11px] tracking-mech px-2 py-1 uppercase"
              >
                clear
              </button>
              <span className="caption">(up to 4)</span>
            </div>
            <div className="flex gap-2">
              <button
                onClick={() =>
                  navigate(`/compare?ids=${Array.from(selected).join(",")}`)
                }
                disabled={selected.size < 2}
                className="btn btn-primary"
              >
                COMPARE ({selected.size})
              </button>
            </div>
          </div>
        </div>
      )}

      {loading ? (
        <p className="caption">LOADING…</p>
      ) : (
        <div className="panel overflow-x-auto">
          <table className="data-table min-w-full">
            <thead className="bg-surface-1">
              {table.getHeaderGroups().map((hg) => (
                <tr key={hg.id}>
                  {hg.headers.map((header) => (
                    <th
                      key={header.id}
                      onClick={header.column.getToggleSortingHandler()}
                      className="eyebrow text-left py-2 px-3 border-b border-line bg-surface-1 cursor-pointer select-none hover:text-ink-0 transition-colors"
                      style={{ width: header.getSize() }}
                    >
                      <div className="flex items-center gap-1">
                        {flexRender(
                          header.column.columnDef.header,
                          header.getContext()
                        )}
                        {{ asc: " ^", desc: " v" }[
                          header.column.getIsSorted() as string
                        ] ?? ""}
                      </div>
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {table.getRowModel().rows.map((row) => (
                <tr
                  key={row.id}
                  className="hover:bg-surface-1 cursor-pointer"
                  onClick={() => navigate(`/results/${row.original.run_id}`)}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td
                      key={cell.id}
                      className="py-2.5 px-3 border-b border-line/60 whitespace-nowrap text-ink-0 font-mono text-[12.5px]"
                      onClick={
                        cell.column.id === "select"
                          ? (e) => e.stopPropagation()
                          : undefined
                      }
                    >
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext()
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {data.length === 0 && (
            <p className="text-center py-8 caption">
              No results found. Adjust filters or seed the catalog.
            </p>
          )}
        </div>
      )}
      </div>
    </>
  );
}
