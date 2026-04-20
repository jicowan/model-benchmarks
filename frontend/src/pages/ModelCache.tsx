import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listModelCache, createModelCache, deleteModelCache, registerCustomModel } from "../api";
import type { ModelCache as ModelCacheEntry } from "../types";
import ModelCombobox from "../components/ModelCombobox";

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
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [formMode, setFormMode] = useState<FormMode>("none");
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const [cacheHfId, setCacheHfId] = useState("");
  const [cacheRevision, setCacheRevision] = useState("main");
  const [cacheToken, setCacheToken] = useState("");

  const [registerS3URI, setRegisterS3URI] = useState("");
  const [registerName, setRegisterName] = useState("");

  const fetchItems = useCallback(() => {
    listModelCache()
      .then((data) => {
        setItems(data || []);
        setLoading(false);
      })
      .catch((e) => {
        setError(e.message);
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    fetchItems();
  }, [fetchItems]);

  // Poll while any entry is actively caching
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
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : "Failed to register model");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(id: string, name: string) {
    if (!confirm(`Delete cached model "${name}"? This will remove the S3 data.`)) return;
    try {
      await deleteModelCache(id);
      fetchItems();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to delete");
    }
  }

  const cached = items.filter((i) => i.status === "cached");
  const caching = items.filter((i) => i.status === "caching" || i.status === "pending");
  const failed = items.filter((i) => i.status === "failed");
  const totalBytes = cached.reduce((sum, i) => sum + (i.size_bytes ?? 0), 0);

  return (
    <>
      <PageHeader
        path={["accelbench", "models"]}
        right={
          <div className="flex gap-2">
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
        }
      />

      <div className="p-6 max-w-[1600px] mx-auto animate-enter">
        {/* Intro / info */}
        <div className="mb-8">
          <div className="eyebrow mb-3">MODEL REGISTRY</div>
          <h1 className="font-sans text-[28px] leading-tight tracking-[-0.01em] max-w-2xl text-balance mb-3">
            Cache HuggingFace weights to S3 or register custom models for benchmarking.
          </h1>
          <p className="meta max-w-xl">
            Cached and registered models become available for benchmark runs. Loading from S3 via
            Run:ai streamer is typically 10–20× faster than pulling from HuggingFace.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-4 border-l border-t border-line mb-8">
          <StatCell label="TOTAL" value={loading ? "—" : items.length} sub="registered" index="01" />
          <StatCell
            label="CACHED"
            value={loading ? "—" : cached.length}
            sub={`${formatBytes(totalBytes)} in S3`}
            index="02"
            accent="signal"
          />
          <StatCell
            label="CACHING"
            value={loading ? "—" : caching.length}
            sub={caching.length > 0 ? "In progress" : "Idle"}
            accent={caching.length > 0 ? "warn" : undefined}
            index="03"
          />
          <StatCell
            label="FAILED"
            value={loading ? "—" : failed.length}
            sub={failed.length > 0 ? "Review below" : "—"}
            accent={failed.length > 0 ? "danger" : undefined}
            index="04"
          />
        </div>

        {/* Inline forms */}
        {formMode === "cache" && (
          <div className="mb-6 panel p-5 animate-enter">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-baseline gap-3">
                <span className="eyebrow">[ NEW CACHE JOB ]</span>
                <h3 className="font-sans text-[15px] text-ink-0">Download a HuggingFace model to S3</h3>
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
                  <label className="eyebrow block mb-1.5">HF TOKEN <span className="text-ink-2 normal-case">(optional, for gated models)</span></label>
                  <input
                    type="password"
                    value={cacheToken}
                    onChange={(e) => setCacheToken(e.target.value)}
                    placeholder="hf_…"
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
                <h3 className="font-sans text-[15px] text-ink-0">Register an existing S3 model</h3>
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
                {loading ? "loading…" : `${items.length} entries`}
              </span>
            </div>
          </div>
          <table className="data-table">
            <thead>
              <tr>
                <th className="w-28">STATUS</th>
                <th>NAME</th>
                <th>HF ID</th>
                <th>S3 URI</th>
                <th className="w-20 num">SIZE</th>
                <th className="w-44">CACHED</th>
                <th className="w-20"></th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={7} className="text-center py-12 caption">
                    <span className="inline-flex items-center gap-2">
                      <span className="w-1.5 h-1.5 bg-signal animate-pulse_signal" />
                      LOADING…
                    </span>
                  </td>
                </tr>
              ) : items.length === 0 ? (
                <tr>
                  <td colSpan={7} className="text-center py-16 caption">
                    <div className="mb-3">NO MODELS REGISTERED</div>
                    <button onClick={() => setFormMode("cache")} className="btn btn-primary">
                      CACHE FIRST MODEL
                    </button>
                  </td>
                </tr>
              ) : (
                items.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <div className="flex items-center">
                        <span className={`status-dot ${statusDotClass(item.status)}`} />
                        <span className="uppercase tracking-mech text-[11px]">{item.status}</span>
                      </div>
                      {item.status === "failed" && item.error_message && (
                        <p className="text-[10.5px] text-danger mt-1 max-w-xs truncate" title={item.error_message}>
                          {item.error_message}
                        </p>
                      )}
                    </td>
                    <td>
                      <div className="text-ink-0 truncate max-w-[280px]">{item.display_name}</div>
                    </td>
                    <td>
                      <span className="path text-ink-1">
                        {item.hf_id || <span className="text-ink-2 italic">CUSTOM</span>}
                      </span>
                    </td>
                    <td>
                      <span className="path text-ink-1 truncate max-w-[360px] block" title={item.s3_uri}>
                        {item.s3_uri}
                      </span>
                    </td>
                    <td className="num text-ink-1">{formatBytes(item.size_bytes)}</td>
                    <td className="text-ink-2 text-[11.5px]">{formatDate(item.cached_at)}</td>
                    <td>
                      <div className="flex gap-2 justify-end">
                        {item.hf_id && (
                          <Link
                            to={`/run?model=${encodeURIComponent(item.hf_id)}`}
                            className="text-[11px] font-mono tracking-mech text-signal hover:underline"
                          >
                            RUN →
                          </Link>
                        )}
                        <button
                          onClick={() => handleDelete(item.id, item.display_name)}
                          className="text-[11px] font-mono tracking-mech text-ink-2 hover:text-danger"
                        >
                          DEL
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* Footer */}
        <div className="mt-4 flex justify-between caption">
          <span>
            {loading ? "LOADING…" : `${items.length} TOTAL · ${cached.length} CACHED · ${formatBytes(totalBytes)} S3`}
          </span>
          {caching.length > 0 && (
            <span className="flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 bg-warn animate-pulse_signal" />
              AUTO-REFRESH WHILE CACHING
            </span>
          )}
        </div>
      </div>
    </>
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
