import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { listRuns, listSuiteRuns, listCatalog, getDashboardStats } from "../api";
import type { RunListItem, CatalogEntry, DashboardStats } from "../types";
import type { SuiteRunListItem } from "../api";
import { useStatus } from "../hooks/useStatus";

/* ----------------------------- PageHeader ----------------------------- */

function PageHeader({ path }: { path: string[] }) {
  return (
    <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
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
    </div>
  );
}

/* ---------------------------- SectionHeader --------------------------- */

function SectionHeader({ index, label, action }: { index: string; label: string; action?: React.ReactNode }) {
  return (
    <div className="flex items-end justify-between mb-4 pb-3 border-b border-line">
      <div className="flex items-baseline gap-4">
        <span className="font-mono text-[11px] tracking-widemech text-ink-2">[{index}]</span>
        <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">
          {label}
        </h2>
      </div>
      {action}
    </div>
  );
}

/* --------------------------- ActivityPulse ---------------------------- */

// Generates a tiny bar chart from run timestamps (last N days). PRD-35
// adds a cost-per-day overlay on the hover caption + idle summary.
function ActivityPulse({
  runTimestamps,
  suiteTimestamps,
  costPerDay,
}: {
  runTimestamps: string[];
  suiteTimestamps: string[];
  costPerDay?: { day: string; cost_usd: number }[];
}) {
  const DAYS = 14;
  const CHART_HEIGHT = 48;
  const [hovered, setHovered] = useState<number | null>(null);

  // Align bucket boundaries to local midnight so same-day runs land in the
  // same bucket. Compare calendar dates rather than ms-floored arithmetic,
  // otherwise a run from e.g. 22h ago computes (22/24).floor = 0 and gets
  // double-booked into "today" instead of "yesterday".
  const runBuckets = Array(DAYS).fill(0);
  const suiteBuckets = Array(DAYS).fill(0);
  const costBuckets = Array<number>(DAYS).fill(0);
  const bucketIndex = (ts: string) => {
    const now = new Date();
    const todayMid = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
    const d = new Date(ts);
    const dayMid = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const daysAgo = Math.round((todayMid - dayMid) / (1000 * 60 * 60 * 24));
    return DAYS - 1 - Math.max(0, daysAgo);
  };
  runTimestamps.forEach((ts) => {
    const idx = bucketIndex(ts);
    if (idx >= 0 && idx < DAYS) runBuckets[idx]++;
  });
  suiteTimestamps.forEach((ts) => {
    const idx = bucketIndex(ts);
    if (idx >= 0 && idx < DAYS) suiteBuckets[idx]++;
  });
  // costPerDay from the API is ordered oldest → newest (last 14 UTC days).
  // Match that ordering so index 0 = -13d and index 13 = today.
  if (costPerDay) {
    for (let i = 0; i < Math.min(DAYS, costPerDay.length); i++) {
      costBuckets[i] = costPerDay[i].cost_usd;
    }
  }
  const buckets = runBuckets.map((n, i) => n + suiteBuckets[i]);
  const max = Math.max(1, ...buckets);
  const total = buckets.reduce((a, b) => a + b, 0);
  const cost14dSum = costBuckets.reduce((a, b) => a + b, 0);

  const hoveredLabel = (() => {
    if (hovered === null) return null;
    const daysAgo = DAYS - 1 - hovered;
    if (daysAgo === 0) return "TODAY";
    if (daysAgo === 1) return "1 DAY AGO";
    return `${daysAgo} DAYS AGO`;
  })();

  return (
    <div>
      <div className="flex items-end gap-[3px]" style={{ height: `${CHART_HEIGHT}px` }}>
        {buckets.map((b, i) => {
          const isEmpty = b === 0;
          const h = isEmpty ? 2 : Math.max(4, (b / max) * CHART_HEIGHT);
          const isHovered = hovered === i;
          return (
            <div
              key={i}
              onMouseEnter={() => setHovered(i)}
              onMouseLeave={() => setHovered((prev) => (prev === i ? null : prev))}
              className={`flex-1 transition-colors cursor-default ${
                isEmpty
                  ? isHovered ? "bg-line-strong" : "bg-line"
                  : isHovered ? "bg-signal" : "bg-signal/60"
              }`}
              style={{ height: `${h}px` }}
            />
          );
        })}
      </div>
      <div className="mt-2 flex justify-between items-center">
        <span className="caption">
          {hovered !== null ? (
            <>
              <span className="text-ink-0 font-mono tabular">{runBuckets[hovered]}</span>{" "}
              RUN{runBuckets[hovered] === 1 ? "" : "S"} ·{" "}
              <span className="text-ink-0 font-mono tabular">{suiteBuckets[hovered]}</span>{" "}
              SUITE{suiteBuckets[hovered] === 1 ? "" : "S"} · {hoveredLabel}
              {costPerDay && (
                <>
                  {" · "}
                  <span className="text-ink-0 font-mono tabular">
                    ${costBuckets[hovered].toFixed(2)}
                  </span>
                </>
              )}
            </>
          ) : (
            <>
              {total} TOTAL · PEAK {max}/DAY
              {costPerDay && (
                <>
                  {" · "}
                  <span className="text-ink-0 font-mono tabular">
                    ${cost14dSum.toFixed(2)}
                  </span>{" "}
                  (14D)
                </>
              )}
            </>
          )}
        </span>
        <span className="caption">-{DAYS - 1}d ← today</span>
      </div>
    </div>
  );
}

/* ----------------------------- Utilities ----------------------------- */

function timeAgo(iso: string): string {
  const sec = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  const min = sec / 60;
  if (min < 60) return `${Math.floor(min)}m ago`;
  const hr = min / 60;
  if (hr < 24) return `${Math.floor(hr)}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

function statusClass(s: string): string {
  switch (s) {
    case "running":
      return "status-running";
    case "pending":
      return "status-pending";
    case "completed":
      return "status-completed";
    case "failed":
      return "status-failed";
    default:
      return "status-pending";
  }
}

/* ------------------------------ Dashboard ----------------------------- */

function heroFor(state: "ok" | "degraded" | "down" | "unknown") {
  switch (state) {
    case "ok":
      return { word: "ready", color: "text-ink-0", cursor: "text-signal", pulse: true };
    case "degraded":
      return { word: "degraded", color: "text-warn", cursor: "text-warn", pulse: true };
    case "down":
      return { word: "offline", color: "text-danger", cursor: "text-danger", pulse: false };
    default:
      return { word: "…", color: "text-ink-2", cursor: "text-ink-2", pulse: false };
  }
}

export default function Dashboard() {
  // Runs + suites are fetched only for the "Recent runs" table and the
  // activity-pulse timestamps — NOT for the stat cards. Cards come from
  // the PRD-35 aggregate endpoint so they reflect lifetime totals, not a
  // paginated slice.
  const [runs, setRuns] = useState<RunListItem[]>([]);
  const [suiteRuns, setSuiteRuns] = useState<SuiteRunListItem[]>([]);
  const [catalog, setCatalog] = useState<CatalogEntry[]>([]);
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(true);
  const { state: healthState, detail: healthDetail } = useStatus();
  const hero = heroFor(healthState);

  // PRD-44: AdminRoute redirects non-admins here with ?flash=admins-only.
  // Read the flash param once, strip it from the URL so refresh doesn't
  // resurrect the banner, and render a dismissible notice.
  const [searchParams, setSearchParams] = useSearchParams();
  const [flash, setFlash] = useState<string | null>(null);
  useEffect(() => {
    const f = searchParams.get("flash");
    if (f) {
      setFlash(f);
      const next = new URLSearchParams(searchParams);
      next.delete("flash");
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  useEffect(() => {
    Promise.all([
      listRuns({ limit: 100 }).catch(() => [] as RunListItem[]),
      listSuiteRuns().catch(() => [] as SuiteRunListItem[]),
      // Top Models panel only — not for stat cards. The 100-row cap is fine
      // here since it's a "most-common model" frequency view.
      listCatalog({ limit: 100 }).then((p) => p.rows).catch(() => [] as CatalogEntry[]),
      getDashboardStats().catch(() => null),
    ]).then(([r, sr, c, s]) => {
      setRuns(r);
      setSuiteRuns(sr);
      setCatalog(c);
      setStats(s);
      setLoading(false);
    });
  }, []);

  const recentRuns = runs.slice(0, 8);

  return (
    <>
      <PageHeader path={["accelbench", "dashboard"]} />

      {flash === "admins-only" && (
        <div className="bg-warn/10 border-b border-warn/40 px-6 py-3 flex items-center justify-between font-mono text-[12px] tracking-mech">
          <span className="text-ink-0">
            <span className="text-warn uppercase tracking-widemech mr-3">Admins only</span>
            You don't have access to the Configuration page.
          </span>
          <button
            type="button"
            onClick={() => setFlash(null)}
            className="uppercase tracking-widemech text-ink-2 hover:text-ink-0 transition-colors"
            aria-label="Dismiss"
          >
            Dismiss
          </button>
        </div>
      )}

      <div className="p-6 max-w-[1400px] mx-auto animate-enter">
        {/* Hero / system state */}
        <div className="mb-10 flex items-end justify-between border-b border-line pb-8">
          <div>
            <div className="eyebrow mb-3">SYSTEM STATE</div>
            <h1 className="font-sans text-[44px] leading-[1] tracking-[-0.02em] text-balance">
              <span className="text-ink-2">&gt;</span>{" "}
              <span className={hero.color}>{hero.word}</span>
              <span className={`${hero.cursor} ${hero.pulse ? "animate-pulse_signal" : ""}`}>_</span>
            </h1>
            {healthDetail && (
              <div className="mt-4 flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px] tracking-mech uppercase">
                {Object.entries(healthDetail.components).map(([name, c]) => (
                  <span key={name} className="flex items-center gap-1.5">
                    <span
                      className={`w-1.5 h-1.5 ${
                        c.status === "ok" ? "bg-signal" : "bg-danger"
                      }`}
                    />
                    <span className="text-ink-2">{name}</span>
                    <span className={c.status === "ok" ? "text-ink-0" : "text-danger"}>
                      {c.status}
                    </span>
                    {c.latency_ms !== undefined && (
                      <span className="text-ink-2 tabular">{c.latency_ms}ms</span>
                    )}
                  </span>
                ))}
              </div>
            )}
            <p className="meta mt-3 max-w-md">
              {loading || !stats
                ? "Loading system state…"
                : `${stats.total_runs} runs recorded · ${stats.active_count} active · ${stats.cached_models} models in S3 cache`}
            </p>
          </div>
          <div className="flex gap-2">
            <Link to="/run" className="btn btn-primary">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
                <path d="M5 12h14M12 5l7 7-7 7" />
              </svg>
              NEW BENCHMARK
            </Link>
            <Link to="/runs" className="btn">
              BROWSE RUNS
            </Link>
          </div>
        </div>

        {/* Stat grid — driven by the server-side aggregate (PRD-35). */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-0 border-l border-t border-line mb-12">
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="TOTAL RUNS"
              value={!stats ? "—" : stats.total_runs}
              sub={
                stats
                  ? `${stats.total_single} single · ${stats.total_suites} suite`
                  : "—"
              }
              index="01"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="ACTIVE"
              value={!stats ? "—" : stats.active_count}
              sub={stats && stats.active_count > 0 ? "In progress now" : "Nothing running"}
              accent={stats && stats.active_count > 0 ? "signal" : undefined}
              index="02"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="SUCCESS RATE"
              value={!stats ? "—" : `${stats.success_rate.toFixed(1)}%`}
              sub={
                stats
                  ? `${stats.completed_count} / ${stats.completed_count + stats.failed_count} finished`
                  : "—"
              }
              accent={stats && stats.failed_count > stats.completed_count ? "warn" : "signal"}
              index="03"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="CACHED MODELS"
              value={!stats ? "—" : stats.cached_models}
              sub="In S3, ready to benchmark"
              index="04"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="TOTAL SPEND"
              value={!stats ? "—" : `$${stats.total_cost_usd.toFixed(2)}`}
              sub="Lifetime, all runs + suites"
              index="05"
            />
          </div>
        </div>

        {/* 14-day activity */}
        <section className="mb-12">
          <SectionHeader
            index="A"
            label="14-day activity"
            action={<Link to="/runs" className="btn btn-ghost">VIEW ALL →</Link>}
          />
          <div className="panel p-5">
            {loading ? (
              <div className="h-12 flex items-center caption">Loading…</div>
            ) : (
              <ActivityPulse
                runTimestamps={runs.map((r) => r.created_at)}
                suiteTimestamps={suiteRuns.map((s) => s.created_at)}
                costPerDay={stats?.cost_per_day}
              />
            )}
          </div>
        </section>

        {/* Two-column: recent runs + quick actions */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* Recent runs */}
          <section className="lg:col-span-2">
            <SectionHeader
              index="B"
              label="Recent runs"
              action={
                <Link to="/runs" className="btn btn-ghost">
                  VIEW ALL →
                </Link>
              }
            />
            <div className="panel overflow-hidden">
              <table className="data-table">
                <thead>
                  <tr>
                    <th className="w-28">STATUS</th>
                    <th>MODEL</th>
                    <th>INSTANCE</th>
                    <th className="w-24">AGE</th>
                  </tr>
                </thead>
                <tbody>
                  {loading ? (
                    <tr>
                      <td colSpan={4} className="text-center py-8 caption">
                        Loading…
                      </td>
                    </tr>
                  ) : recentRuns.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="text-center py-8 caption">
                        No runs yet. <Link to="/run" className="text-signal hover:underline">Start one →</Link>
                      </td>
                    </tr>
                  ) : (
                    recentRuns.map((r) => (
                      <tr key={r.id} className="cursor-pointer">
                        <td>
                          <Link to={`/results/${r.id}`} className="flex items-center">
                            <span className={`status-dot ${statusClass(r.status)}`} />
                            <span className="uppercase tracking-mech text-[11px]">{r.status}</span>
                          </Link>
                        </td>
                        <td>
                          <Link to={`/results/${r.id}`} className="path hover:text-signal truncate max-w-[280px] block">
                            {r.model_hf_id}
                          </Link>
                        </td>
                        <td className="text-ink-1">{r.instance_type_name}</td>
                        <td className="text-ink-2">{timeAgo(r.created_at)}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>

          {/* Sidebar: catalog + test suites, stacked and filling column */}
          <aside className="flex flex-col gap-6">
            <section className="flex flex-col flex-1 min-h-0">
              <SectionHeader
                index="C"
                label="Benchmarks"
                action={
                  <Link to="/catalog" className="btn btn-ghost">
                    OPEN →
                  </Link>
                }
              />
              <div className="panel p-4 flex-1 flex flex-col">
                <div className="flex items-baseline gap-3 mb-4">
                  <span className="font-mono text-[28px] tabular text-ink-0 leading-none">
                    {loading ? "—" : catalog.length}
                  </span>
                  <span className="caption">results indexed</span>
                </div>
                <div className="caption mb-3">TOP MODELS</div>
                <div className="flex-1 flex flex-col gap-1 -mx-4">
                  {loading ? (
                    <div className="caption px-4">Loading…</div>
                  ) : (
                    Object.entries(
                      catalog.reduce<Record<string, number>>((acc, c) => {
                        acc[c.model_hf_id] = (acc[c.model_hf_id] ?? 0) + 1;
                        return acc;
                      }, {})
                    )
                      .sort((a, b) => b[1] - a[1])
                      .slice(0, 5)
                      .map(([model, count]) => (
                        <div
                          key={model}
                          className="flex items-center justify-between py-1 px-4 hover:bg-surface-2"
                        >
                          <span className="path truncate pr-2">{model}</span>
                          <span className="caption tabular shrink-0">{count}×</span>
                        </div>
                      ))
                  )}
                </div>
              </div>
            </section>

            <section className="flex flex-col">
              <SectionHeader index="D" label="Test suites" />
              <div className="panel p-4">
                <div className="flex items-baseline gap-3 mb-3">
                  <span className="font-mono text-[28px] tabular text-ink-0 leading-none">
                    {loading ? "—" : suiteRuns.length}
                  </span>
                  <span className="caption">suite runs</span>
                </div>
                {suiteRuns.slice(0, 3).map((s) => (
                  <Link
                    key={s.id}
                    to={`/suite-runs/${s.id}`}
                    className="flex items-center justify-between py-1.5 border-t border-line/60 first:border-t-0 hover:bg-surface-2 -mx-4 px-4"
                  >
                    <div className="min-w-0">
                      <div className="path truncate">{s.model_hf_id}</div>
                      <div className="caption">{s.suite_id}</div>
                    </div>
                    <span className="caption shrink-0 ml-2 flex items-center">
                      <span className={`status-dot ${statusClass(s.status)}`} />
                      {s.status}
                    </span>
                  </Link>
                ))}
                {!loading && suiteRuns.length === 0 && (
                  <div className="caption">No suite runs yet</div>
                )}
              </div>
            </section>
          </aside>
        </div>

        {/* Footer */}
        <div className="mt-16 pt-6 border-t border-line caption flex justify-between">
          <span>ACCELBENCH · INDUSTRIAL TELEMETRY</span>
          <span>FETCH {loading ? "…" : "OK"} · {new Date().toISOString().slice(0, 19)}Z</span>
        </div>
      </div>
    </>
  );
}

function StatCardPlain({
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
    <div className="p-5 bg-surface-1 h-full">
      <div className="flex items-start justify-between mb-3">
        <span className="eyebrow">{label}</span>
        <span className="font-mono text-[10px] tracking-widemech text-ink-2">{index}</span>
      </div>
      <div className={`font-mono text-[32px] leading-none tabular ${accentClass}`}>
        {value}
      </div>
      {sub && <div className="meta mt-2">{sub}</div>}
    </div>
  );
}
