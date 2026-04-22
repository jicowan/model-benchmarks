import { useCallback, useEffect, useState } from "react";
import {
  getCredentials,
  putHFToken,
  deleteHFToken,
  putDockerHubToken,
} from "../api";
import type { CredentialsStatus, CredentialMetadata } from "../types";

/* ----------------------------- PageHeader ----------------------------- */

function PageHeader({ path }: { path: string[] }) {
  return (
    <div className="h-14 border-b border-line flex items-center px-6 bg-surface-0 sticky top-0 z-20">
      <div className="flex items-center gap-2 font-mono text-[12px] tracking-mech">
        {path.map((p, i) => (
          <span key={i} className="flex items-center gap-2">
            <span className="text-ink-2">{i === 0 ? "" : "/"}</span>
            <span className={i === path.length - 1 ? "text-ink-0" : "text-ink-1"}>{p}</span>
          </span>
        ))}
      </div>
    </div>
  );
}

/* ------------------------- SectionHeader ---------------------------- */

function SectionHeader({ index, label }: { index: string; label: string }) {
  return (
    <div className="flex items-end justify-between mb-4 pb-3 border-b border-line">
      <div className="flex items-baseline gap-4">
        <span className="font-mono text-[11px] tracking-widemech text-ink-2">[{index}]</span>
        <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">{label}</h2>
      </div>
    </div>
  );
}

/* ----------------------------- Utilities ---------------------------- */

function formatDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return `${d.toISOString().slice(0, 10)} ${d.toISOString().slice(11, 16)}Z`;
}

/* ------------------------- Rotate modal ------------------------------ */

type RotateModal =
  | { kind: "hf" }
  | { kind: "dockerhub" }
  | null;

function RotateModalComponent({
  modal,
  onClose,
  onSaved,
}: {
  modal: RotateModal;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [reveal, setReveal] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Reset state when modal opens/closes.
  useEffect(() => {
    setToken("");
    setUsername("");
    setReveal(false);
    setError(null);
  }, [modal]);

  if (!modal) return null;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      if (!modal) return;
      if (modal.kind === "hf") {
        await putHFToken(token);
      } else {
        await putDockerHubToken(username, token);
      }
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err.message || "Save failed");
    } finally {
      setSubmitting(false);
    }
  }

  const title = modal.kind === "hf" ? "Rotate HuggingFace Token" : "Rotate Docker Hub Token";

  return (
    <div
      className="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <form
        onClick={(e) => e.stopPropagation()}
        onSubmit={handleSubmit}
        className="bg-surface-1 border border-line w-full max-w-md p-6"
      >
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="font-sans text-[16px] font-medium tracking-mech text-ink-0">{title}</h3>
          <button
            type="button"
            onClick={onClose}
            className="caption hover:text-ink-0 transition-colors"
          >
            ESC
          </button>
        </div>

        {modal.kind === "dockerhub" && (
          <label className="block mb-4">
            <div className="eyebrow mb-1">Username</div>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
              autoComplete="off"
              className="w-full bg-surface-0 border border-line px-3 py-2 font-mono text-[12.5px] text-ink-0 focus:outline-none focus:border-signal"
            />
          </label>
        )}

        <label className="block mb-2">
          <div className="eyebrow mb-1">
            {modal.kind === "hf" ? "Token" : "Access Token"}
          </div>
          <div className="flex gap-2">
            <input
              type={reveal ? "text" : "password"}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoFocus={modal.kind === "hf"}
              autoComplete="off"
              spellCheck={false}
              className="flex-1 bg-surface-0 border border-line px-3 py-2 font-mono text-[12.5px] text-ink-0 focus:outline-none focus:border-signal"
              placeholder={modal.kind === "hf" ? "hf_..." : "dckr_pat_..."}
            />
            <button
              type="button"
              onClick={() => setReveal(!reveal)}
              className="px-3 caption hover:text-ink-0 border border-line bg-surface-0 transition-colors"
              title={reveal ? "Hide" : "Reveal"}
            >
              {reveal ? "HIDE" : "SHOW"}
            </button>
          </div>
        </label>

        <div className="meta mb-6">
          Values are stored in AWS Secrets Manager, never displayed in the UI after save.
        </div>

        {error && <div className="caption text-danger mb-3">{error}</div>}

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="btn">
            CANCEL
          </button>
          <button
            type="submit"
            disabled={submitting || !token || (modal.kind === "dockerhub" && !username)}
            className="btn btn-primary"
          >
            {submitting ? "SAVING…" : "SAVE"}
          </button>
        </div>
      </form>
    </div>
  );
}

/* --------------------------- Credentials card -------------------------- */

function CredentialRow({
  label,
  meta,
  onRotate,
  onDelete,
}: {
  label: string;
  meta: CredentialMetadata | undefined;
  onRotate: () => void;
  onDelete?: () => void;
}) {
  const set = meta?.set ?? false;
  return (
    <div className="flex items-center justify-between py-3 border-b border-line/60 last:border-b-0">
      <div className="flex items-baseline gap-4">
        <span className="font-mono text-[12.5px] text-ink-0 w-48">{label}</span>
        <span className={`font-mono text-[11px] tracking-mech uppercase ${set ? "text-signal" : "text-ink-2"}`}>
          {set ? "SET" : "NOT SET"}
        </span>
        {set && (
          <span className="caption">{formatDate(meta?.updated_at)}</span>
        )}
      </div>
      <div className="flex gap-2">
        {set && onDelete && (
          <button onClick={onDelete} className="btn btn-ghost">
            CLEAR
          </button>
        )}
        <button onClick={onRotate} className="btn">
          {set ? "ROTATE" : "SET"}
        </button>
      </div>
    </div>
  );
}

function CredentialsCard({
  creds,
  onChanged,
  setModal,
}: {
  creds: CredentialsStatus | null;
  onChanged: () => void;
  setModal: (m: RotateModal) => void;
}) {
  const [clearing, setClearing] = useState(false);

  async function handleClearHF() {
    if (!confirm("Delete the platform HuggingFace token? Benchmarks for gated models will fall back to the per-run field.")) {
      return;
    }
    setClearing(true);
    try {
      await deleteHFToken();
      onChanged();
    } finally {
      setClearing(false);
    }
  }

  return (
    <section className="mb-10">
      <SectionHeader index="A" label="Credentials" />
      <div className="panel px-5">
        <CredentialRow
          label="HuggingFace token"
          meta={creds?.hf_token}
          onRotate={() => setModal({ kind: "hf" })}
          onDelete={clearing ? undefined : handleClearHF}
        />
        <CredentialRow
          label="Docker Hub token"
          meta={creds?.dockerhub_token}
          onRotate={() => setModal({ kind: "dockerhub" })}
        />
      </div>
      <p className="meta mt-3 max-w-xl">
        Tokens are stored in AWS Secrets Manager and automatically injected into benchmark runs,
        cache jobs, and catalog seeds. The UI never displays saved values — to change, use rotate.
      </p>
    </section>
  );
}

/* ----------------------- Placeholder cards (PRD-32) ------------------- */

function PlaceholderCard({ index, label, note }: { index: string; label: string; note: string }) {
  return (
    <section className="mb-10">
      <SectionHeader index={index} label={label} />
      <div className="panel p-5">
        <p className="caption">{note}</p>
      </div>
    </section>
  );
}

/* ---------------------------- Page entry ----------------------------- */

export default function Configuration() {
  const [creds, setCreds] = useState<CredentialsStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [modal, setModal] = useState<RotateModal>(null);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const c = await getCredentials();
      setCreds(c);
    } catch (err: any) {
      setError(err.message || "Failed to load credentials");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <>
      <PageHeader path={["accelbench", "configuration"]} />
      <div className="p-6 max-w-[1000px] mx-auto animate-enter">
        <div className="mb-8">
          <div className="eyebrow mb-2">PLATFORM CONFIGURATION</div>
          <h1 className="font-sans text-[22px] leading-tight tracking-[-0.01em]">Configuration</h1>
        </div>

        {loading && <p className="caption">LOADING…</p>}
        {error && <p className="caption text-danger">{error}</p>}

        {!loading && !error && (
          <>
            <CredentialsCard creds={creds} onChanged={refresh} setModal={setModal} />
            <PlaceholderCard
              index="B"
              label="Seeding Matrix"
              note="Editable matrix of models × instance types used by 'Seed Benchmarks'. Ships in a follow-up PRD."
            />
            <PlaceholderCard
              index="C"
              label="Loadgen Defaults"
              note="Editable defaults for inference-perf (input length, workers, streaming, stage QPS rates). Ships in a follow-up PRD."
            />
            <PlaceholderCard
              index="D"
              label="Registry"
              note="Read-only view of the Docker Hub pull-through cache and its mirrored repos. Ships in a follow-up PRD."
            />
          </>
        )}
      </div>

      <RotateModalComponent modal={modal} onClose={() => setModal(null)} onSaved={refresh} />
    </>
  );
}
