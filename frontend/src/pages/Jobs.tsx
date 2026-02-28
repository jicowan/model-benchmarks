import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listRuns, cancelRun, deleteRun } from "../api";
import type { RunListItem } from "../types";

const STATUS_TABS = ["all", "pending", "running", "completed", "failed"] as const;
type StatusTab = (typeof STATUS_TABS)[number];

const statusColor: Record<string, string> = {
  pending: "bg-yellow-100 text-yellow-800",
  running: "bg-blue-100 text-blue-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
};

const PAGE_SIZE = 50;

function formatDuration(item: RunListItem): string {
  if (!item.started_at) return "\u2014";
  const start = new Date(item.started_at).getTime();
  const end = item.completed_at
    ? new Date(item.completed_at).getTime()
    : Date.now();
  const secs = Math.round((end - start) / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  return `${mins}m ${remSecs}s`;
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString();
}

export default function Jobs() {
  const [runs, setRuns] = useState<RunListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<StatusTab>("all");
  const [modelSearch, setModelSearch] = useState("");
  const [offset, setOffset] = useState(0);

  const fetchRuns = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const items = await listRuns({
        status: tab === "all" ? undefined : tab,
        model: modelSearch || undefined,
        limit: PAGE_SIZE,
        offset,
      });
      setRuns(items);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [tab, modelSearch, offset]);

  useEffect(() => {
    fetchRuns();
  }, [fetchRuns]);

  // Reset offset when filters change.
  useEffect(() => {
    setOffset(0);
  }, [tab, modelSearch]);

  const handleCancel = async (id: string) => {
    if (!window.confirm(`Cancel run ${id.slice(0, 8)}?`)) return;
    try {
      await cancelRun(id);
      fetchRuns();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!window.confirm(`Delete run ${id.slice(0, 8)}? This cannot be undone.`))
      return;
    try {
      await deleteRun(id);
      fetchRuns();
    } catch (e: any) {
      setError(e.message);
    }
  };

  return (
    <div>
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Jobs</h1>
        <button
          onClick={fetchRuns}
          disabled={loading}
          className="px-4 py-2 bg-white border border-gray-300 rounded-md text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
        >
          Refresh
        </button>
      </div>

      {/* Status tabs */}
      <div className="flex space-x-1 mb-4 border-b border-gray-200">
        {STATUS_TABS.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px ${
              tab === t
                ? "border-blue-500 text-blue-600"
                : "border-transparent text-gray-500 hover:text-gray-700"
            }`}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {/* Model search */}
      <div className="mb-4">
        <input
          type="text"
          placeholder="Search by model name..."
          value={modelSearch}
          onChange={(e) => setModelSearch(e.target.value)}
          className="w-full max-w-sm px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      {error && <p className="text-red-600 mb-4">{error}</p>}

      {/* Table */}
      <div className="overflow-x-auto bg-white border border-gray-200 rounded-lg">
        <table className="min-w-full divide-y divide-gray-200">
          <thead className="bg-gray-50">
            <tr>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Status
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Run ID
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Model
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Instance
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Framework
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Created
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Duration
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                Actions
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            {loading && runs.length === 0 ? (
              <tr>
                <td
                  colSpan={8}
                  className="px-4 py-8 text-center text-gray-500"
                >
                  Loading...
                </td>
              </tr>
            ) : runs.length === 0 ? (
              <tr>
                <td
                  colSpan={8}
                  className="px-4 py-8 text-center text-gray-500"
                >
                  No runs found.
                </td>
              </tr>
            ) : (
              runs.map((run) => (
                <tr key={run.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">
                    <span
                      className={`inline-block px-2 py-1 rounded-full text-xs font-medium ${
                        statusColor[run.status] ?? "bg-gray-100"
                      }`}
                    >
                      {run.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-sm">
                    <Link
                      to={`/results/${run.id}`}
                      className="text-blue-600 hover:underline font-mono"
                    >
                      {run.id.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-900">
                    {run.model_hf_id}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-900">
                    {run.instance_type_name}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-900">
                    {run.framework}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-500">
                    {formatTime(run.created_at)}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-500">
                    {formatDuration(run)}
                  </td>
                  <td className="px-4 py-3 text-sm space-x-2">
                    {(run.status === "pending" || run.status === "running") && (
                      <button
                        onClick={() => handleCancel(run.id)}
                        className="text-yellow-600 hover:text-yellow-800 font-medium"
                      >
                        Cancel
                      </button>
                    )}
                    <button
                      onClick={() => handleDelete(run.id)}
                      className="text-red-600 hover:text-red-800 font-medium"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between mt-4">
        <button
          onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
          disabled={offset === 0}
          className="px-3 py-1 bg-white border border-gray-300 rounded-md text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Previous
        </button>
        <span className="text-sm text-gray-500">
          Showing {offset + 1}–{offset + runs.length}
        </span>
        <button
          onClick={() => setOffset(offset + PAGE_SIZE)}
          disabled={runs.length < PAGE_SIZE}
          className="px-3 py-1 bg-white border border-gray-300 rounded-md text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Next
        </button>
      </div>
    </div>
  );
}
