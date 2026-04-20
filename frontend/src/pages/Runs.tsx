import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { listRuns, listSuiteRuns, cancelRun, deleteRun } from "../api";

/* ----------------------------- Types ----------------------------- */

interface JobItem {
  id: string;
  type: "run" | "suite";
  model_hf_id: string;
  instance_type_name: string;
  framework_or_suite: string;
  status: string;
  error_message?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

const STATUSES = ["all", "running", "pending", "completed", "failed"] as const;
type StatusFilter = (typeof STATUSES)[number];

const PAGE_SIZE = 50;

/* --------------------------- Utilities --------------------------- */

function formatDuration(item: JobItem): string {
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
  const navigate = useNavigate();
  const [jobs, setJobs] = useState<JobItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [modelSearch, setModelSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState<"all" | "run" | "suite">("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [offset, setOffset] = useState(0);

  const fetchJobs = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [runs, suiteRuns] = await Promise.all([
        listRuns({
          status: status === "all" ? undefined : status,
          model: modelSearch || undefined,
          limit: PAGE_SIZE,
          offset,
        }),
        listSuiteRuns(),
      ]);

      const runItems: JobItem[] = runs.map((r) => ({
        id: r.id,
        type: "run" as const,
        model_hf_id: r.model_hf_id,
        instance_type_name: r.instance_type_name,
        framework_or_suite: r.framework,
        status: r.status,
        error_message: r.error_message,
        created_at: r.created_at,
        started_at: r.started_at,
        completed_at: r.completed_at,
      }));

      let suiteItems: JobItem[] = suiteRuns.map((s) => ({
        id: s.id,
        type: "suite" as const,
        model_hf_id: s.model_hf_id,
        instance_type_name: s.instance_type_name,
        framework_or_suite: s.suite_id,
        status: s.status,
        created_at: s.created_at,
        started_at: s.started_at,
        completed_at: s.completed_at,
      }));

      if (status !== "all") {
        suiteItems = suiteItems.filter((s) => s.status === status);
      }
      if (modelSearch) {
        const q = modelSearch.toLowerCase();
        suiteItems = suiteItems.filter((s) => s.model_hf_id.toLowerCase().includes(q));
      }

      const combined = [...runItems, ...suiteItems].sort(
        (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      );

      setJobs(combined);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Unknown error");
    } finally {
      setLoading(false);
    }
  }, [status, modelSearch, offset]);

  useEffect(() => {
    fetchJobs();
  }, [fetchJobs]);

  useEffect(() => {
    setOffset(0);
  }, [status, modelSearch]);

  const filteredJobs = useMemo(() => {
    if (typeFilter === "all") return jobs;
    return jobs.filter((j) => j.type === typeFilter);
  }, [jobs, typeFilter]);

  const counts = useMemo(() => {
    const all = jobs.length;
    const running = jobs.filter((j) => j.status === "running").length;
    const pending = jobs.filter((j) => j.status === "pending").length;
    const completed = jobs.filter((j) => j.status === "completed").length;
    const failed = jobs.filter((j) => j.status === "failed").length;
    return { all, running, pending, completed, failed };
  }, [jobs]);

  const toggleSelected = (id: string) => {
    setSelected((prev) => {
      const n = new Set(prev);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  };

  const clearSelection = () => setSelected(new Set());

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

  const handleCompare = () => {
    if (selected.size < 2) return;
    const ids = Array.from(selected).join(",");
    navigate(`/compare?runs=${ids}`);
  };

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
            {/* Status tabs */}
            <div className="flex">
              {STATUSES.map((s) => {
                const n = counts[s === "all" ? "all" : s] ?? 0;
                return (
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
                    <span className="font-mono text-[10px] text-ink-2 tabular">{n}</span>
                  </button>
                );
              })}
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

          {/* Selection bar */}
          {selected.size > 0 && (
            <div className="flex items-center justify-between px-4 py-2.5 bg-signal/5 border-b border-signal/30">
              <div className="flex items-center gap-3 font-mono text-[12px]">
                <span className="text-signal">{selected.size} selected</span>
                <button onClick={clearSelection} className="btn-ghost text-[11px] tracking-mech px-2 py-1 uppercase">
                  clear
                </button>
              </div>
              <div className="flex gap-2">
                <button
                  onClick={handleCompare}
                  disabled={selected.size < 2}
                  className="btn btn-primary"
                >
                  COMPARE ({selected.size})
                </button>
              </div>
            </div>
          )}
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
              <tr>
                <th className="w-10"></th>
                <th className="w-28">STATUS</th>
                <th className="w-14">TYPE</th>
                <th className="w-20">ID</th>
                <th>MODEL</th>
                <th>INSTANCE</th>
                <th className="w-24">DURATION</th>
                <th className="w-24">AGE</th>
                <th className="w-20"></th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={9} className="text-center py-12 caption">
                    <span className="inline-flex items-center gap-2">
                      <span className="w-1.5 h-1.5 bg-signal animate-pulse_signal" />
                      LOADING…
                    </span>
                  </td>
                </tr>
              ) : filteredJobs.length === 0 ? (
                <tr>
                  <td colSpan={9} className="text-center py-16 caption">
                    NO RUNS MATCH CURRENT FILTERS
                  </td>
                </tr>
              ) : (
                filteredJobs.map((j) => {
                  const target = j.type === "suite" ? `/suite-runs/${j.id}` : `/results/${j.id}`;
                  return (
                    <tr key={`${j.type}-${j.id}`}>
                      <td className="pl-3 pr-0">
                        <input
                          type="checkbox"
                          checked={selected.has(j.id)}
                          onChange={() => toggleSelected(j.id)}
                          onClick={(e) => e.stopPropagation()}
                          className="accent-signal"
                        />
                      </td>
                      <td>
                        <Link to={target} className="flex items-center" title={j.error_message}>
                          <span className={`status-dot ${statusDotClass(j.status)}`} />
                          <span className="uppercase tracking-mech text-[11px]">{j.status}</span>
                        </Link>
                      </td>
                      <td>
                        <span
                          className={`font-mono text-[10px] tracking-widemech px-1.5 py-0.5 border ${
                            j.type === "suite"
                              ? "border-info/40 text-info"
                              : "border-line-strong text-ink-1"
                          }`}
                        >
                          {j.type === "suite" ? "SUITE" : "RUN"}
                        </span>
                      </td>
                      <td>
                        <Link to={target} className="path hover:text-signal">
                          {j.id.slice(0, 8)}
                        </Link>
                      </td>
                      <td>
                        <div className="flex flex-col min-w-0">
                          <Link to={target} className="path truncate max-w-[320px] hover:text-signal">
                            {j.model_hf_id}
                          </Link>
                          {j.error_message && (
                            <span className="text-[10.5px] text-danger truncate max-w-[320px]" title={j.error_message}>
                              {j.error_message}
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="text-ink-1">{j.instance_type_name}</td>
                      <td className="num text-ink-1">{formatDuration(j)}</td>
                      <td className="text-ink-2">{timeAgo(j.created_at)}</td>
                      <td>
                        <div className="flex gap-1 justify-end">
                          {j.status === "running" && j.type === "run" && (
                            <button
                              onClick={() => handleCancel(j.id)}
                              className="text-[11px] font-mono tracking-mech text-warn hover:text-danger px-1"
                              title="Cancel"
                            >
                              STOP
                            </button>
                          )}
                          {(j.status === "completed" || j.status === "failed") && j.type === "run" && (
                            <button
                              onClick={() => handleDelete(j.id)}
                              className="text-[11px] font-mono tracking-mech text-ink-2 hover:text-danger px-1"
                              title="Delete"
                            >
                              DEL
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Footer */}
        <div className="mt-4 flex items-center justify-between caption">
          <span>
            {loading
              ? "LOADING…"
              : `SHOWING ${filteredJobs.length} OF ${jobs.length} JOBS`}
          </span>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              disabled={offset === 0}
              className="btn btn-ghost"
            >
              ← PREV
            </button>
            <span className="tabular">OFFSET {offset}</span>
            <button
              onClick={() => setOffset(offset + PAGE_SIZE)}
              disabled={jobs.length < PAGE_SIZE}
              className="btn btn-ghost"
            >
              NEXT →
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
