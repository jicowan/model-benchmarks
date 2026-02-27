import { useEffect, useMemo, useState } from "react";
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { useNavigate } from "react-router-dom";
import { listCatalog } from "../api";
import type { CatalogEntry, CatalogFilter } from "../types";
import FilterBar from "../components/FilterBar";

function fmtNum(v: number | undefined, d = 1): string {
  return v != null ? v.toFixed(d) : "--";
}

function fmtParams(v: number | undefined): string {
  if (v == null) return "--";
  if (v >= 1e9) return `${(v / 1e9).toFixed(0)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(0)}M`;
  return String(v);
}

export default function Catalog() {
  const [data, setData] = useState<CatalogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [sorting, setSorting] = useState<SortingState>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const navigate = useNavigate();

  const fetchData = (filter: CatalogFilter = {}) => {
    setLoading(true);
    listCatalog({ ...filter, limit: 500 })
      .then(setData)
      .catch(console.error)
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchData();
  }, []);

  const columns = useMemo<ColumnDef<CatalogEntry>[]>(
    () => [
      {
        id: "select",
        header: "",
        cell: ({ row }) => (
          <input
            type="checkbox"
            checked={selected.has(row.original.run_id)}
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
        accessorKey: "parameter_count",
        header: "Params",
        cell: (info) => fmtParams(info.getValue<number>()),
        size: 80,
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
        accessorKey: "accelerator_utilization_pct",
        header: "Util %",
        cell: (info) => fmtNum(info.getValue<number>(), 0),
        size: 70,
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
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Benchmark Catalog</h1>
        {selected.size > 0 && (
          <button
            onClick={() =>
              navigate(`/compare?ids=${Array.from(selected).join(",")}`)
            }
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
          >
            Compare ({selected.size})
          </button>
        )}
      </div>

      <FilterBar onFilter={fetchData} />

      {loading ? (
        <p className="text-gray-500">Loading...</p>
      ) : (
        <div className="overflow-x-auto border border-gray-200 rounded-lg">
          <table className="min-w-full divide-y divide-gray-200">
            <thead className="bg-gray-50">
              {table.getHeaderGroups().map((hg) => (
                <tr key={hg.id}>
                  {hg.headers.map((header) => (
                    <th
                      key={header.id}
                      onClick={header.column.getToggleSortingHandler()}
                      className="px-3 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider cursor-pointer select-none"
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
            <tbody className="bg-white divide-y divide-gray-200">
              {table.getRowModel().rows.map((row) => (
                <tr
                  key={row.id}
                  className="hover:bg-gray-50 cursor-pointer"
                  onClick={() => navigate(`/results/${row.original.run_id}`)}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td
                      key={cell.id}
                      className="px-3 py-3 whitespace-nowrap text-sm text-gray-700"
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
            <p className="text-center py-8 text-gray-500">
              No results found. Adjust filters or seed the catalog.
            </p>
          )}
        </div>
      )}
    </div>
  );
}
