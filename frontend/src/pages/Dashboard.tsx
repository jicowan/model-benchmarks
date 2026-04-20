import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listRuns, listSuiteRuns, listModelCache, listCatalog } from "../api";
import type { RunListItem, ModelCache, CatalogEntry } from "../types";
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

// Generates a tiny bar chart from run timestamps (last N days).
function ActivityPulse({ runs }: { runs: RunListItem[] }) {
  const DAYS = 14;
  const CHART_HEIGHT = 48;
  const [hovered, setHovered] = useState<number | null>(null);

  // Align bucket boundaries to local midnight so same-day runs land in the same bucket.
  const todayStart = new Date();
  todayStart.setHours(0, 0, 0, 0);
  const buckets = Array(DAYS).fill(0);
  runs.forEach((r) => {
    const t = new Date(r.created_at).getTime();
    const daysAgo = Math.floor((todayStart.getTime() - t) / (1000 * 60 * 60 * 24));
    const idx = DAYS - 1 - Math.max(0, daysAgo);
    if (idx >= 0 && idx < DAYS) buckets[idx]++;
  });
  const max = Math.max(1, ...buckets);
  const total = buckets.reduce((a, b) => a + b, 0);

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
              <span className="text-ink-0 font-mono tabular">{buckets[hovered]}</span>{" "}
              RUN{buckets[hovered] === 1 ? "" : "S"} · {hoveredLabel}
            </>
          ) : (
            <>{total} RUNS · PEAK {max}/DAY</>
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
  const [runs, setRuns] = useState<RunListItem[]>([]);
  const [suiteRuns, setSuiteRuns] = useState<SuiteRunListItem[]>([]);
  const [catalog, setCatalog] = useState<CatalogEntry[]>([]);
  const [models, setModels] = useState<ModelCache[]>([]);
  const [loading, setLoading] = useState(true);
  const { state: healthState, detail: healthDetail } = useStatus();
  const hero = heroFor(healthState);

  useEffect(() => {
    Promise.all([
      listRuns({ limit: 100 }).catch(() => [] as RunListItem[]),
      listSuiteRuns().catch(() => [] as SuiteRunListItem[]),
      listCatalog({ limit: 100 }).catch(() => [] as CatalogEntry[]),
      listModelCache().catch(() => [] as ModelCache[]),
    ]).then(([r, sr, c, m]) => {
      setRuns(r);
      setSuiteRuns(sr);
      setCatalog(c);
      setModels(m);
      setLoading(false);
    });
  }, []);

  const activeRuns = runs.filter((r) => r.status === "running" || r.status === "pending");
  const failedCount = runs.filter((r) => r.status === "failed").length;
  const completedCount = runs.filter((r) => r.status === "completed").length;
  const successRate = runs.length > 0 ? ((completedCount / runs.length) * 100).toFixed(1) : "—";
  const cachedCount = models.filter((m) => m.status === "cached").length;

  const recentRuns = runs.slice(0, 8);

  return (
    <>
      <PageHeader path={["accelbench", "dashboard"]} />

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
              {loading
                ? "Loading system state…"
                : `${runs.length} runs recorded · ${activeRuns.length} active · ${cachedCount} models in S3 cache`}
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

        {/* Stat grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-0 border-l border-t border-line mb-12">
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="TOTAL RUNS"
              value={loading ? "—" : runs.length}
              sub={`${completedCount} completed · ${failedCount} failed`}
              index="01"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="ACTIVE"
              value={loading ? "—" : activeRuns.length}
              sub={activeRuns.length > 0 ? "In progress now" : "Nothing running"}
              accent={activeRuns.length > 0 ? "signal" : undefined}
              index="02"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="SUCCESS RATE"
              value={loading ? "—" : `${successRate}%`}
              sub={`${completedCount} / ${runs.length} runs`}
              accent={failedCount > completedCount ? "warn" : "signal"}
              index="03"
            />
          </div>
          <div className="border-r border-b border-line">
            <StatCardPlain
              label="CACHED MODELS"
              value={loading ? "—" : cachedCount}
              sub="In S3, ready to benchmark"
              index="04"
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
              <ActivityPulse runs={runs} />
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
                label="Catalog"
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
