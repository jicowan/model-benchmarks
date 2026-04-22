import { useCallback, useEffect, useState } from "react";
import {
  getCredentials,
  putHFToken,
  deleteHFToken,
  putDockerHubToken,
  getCatalogMatrix,
  putCatalogMatrix,
  listScenarioOverrides,
  putScenarioOverride,
  deleteScenarioOverride,
  getRegistry,
  listAuditLog,
  listInstanceTypes,
  listCapacityReservations,
  attachCapacityReservation,
  detachCapacityReservation,
} from "../api";
import type {
  CredentialsStatus,
  CredentialMetadata,
  CatalogMatrixPayload,
  CatalogModelEntry,
  ScenarioOverrideEntry,
  RegistryStatus,
  AuditLogEntry,
  InstanceType,
  NodePoolReservations,
  ReservationSummary,
} from "../types";
import ModelCombobox from "../components/ModelCombobox";

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

/* --------------------- Collapsible section wrapper -------------------- */
// Single source of the expand/collapse UI used across the Configuration
// page. Clicking the whole header bar toggles; the SHOW/HIDE indicator
// on the right mirrors the current state. `action` still renders when
// open (e.g., Save button on the matrix editor) without toggling.

function CollapsibleSection({
  index,
  label,
  defaultOpen = false,
  action,
  children,
}: {
  index: string;
  label: string;
  defaultOpen?: boolean;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section className="mb-10">
      <div className="flex items-end justify-between mb-4 pb-3 border-b border-line">
        <button
          onClick={() => setOpen(!open)}
          className="flex items-baseline gap-4 text-left flex-1 min-w-0"
        >
          <span className="font-mono text-[11px] tracking-widemech text-ink-2">[{index}]</span>
          <h2 className="font-sans text-[15px] font-medium tracking-mech text-ink-0">{label}</h2>
        </button>
        <div className="flex items-center gap-3">
          {open && action}
          <button
            onClick={() => setOpen(!open)}
            className="caption hover:text-ink-0 transition-colors"
          >
            {open ? "HIDE" : "SHOW"}
          </button>
        </div>
      </div>
      {open && children}
    </section>
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
    <CollapsibleSection index="A" label="Credentials" defaultOpen>
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
    </CollapsibleSection>
  );
}

/* --------------------- Seeding Matrix card (PRD-32) ------------------- */

function SeedingMatrixCard() {
  const [matrix, setMatrix] = useState<CatalogMatrixPayload | null>(null);
  const [availableInstances, setAvailableInstances] = useState<InstanceType[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedFlash, setSavedFlash] = useState(false);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      setMatrix(await getCatalogMatrix());
    } catch (err: any) {
      setError(err.message || "Failed to load seeding matrix");
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  // Accelerated instance types for the dropdown — pulled from the same
  // endpoint the Run form uses.
  useEffect(() => {
    listInstanceTypes().then(setAvailableInstances).catch(() => {});
  }, []);

  async function handleSave() {
    if (!matrix) return;
    setSaving(true);
    setError(null);
    try {
      const fresh = await putCatalogMatrix(matrix);
      setMatrix(fresh);
      setSavedFlash(true);
      setTimeout(() => setSavedFlash(false), 2000);
    } catch (err: any) {
      setError(err.message || "Save failed");
    } finally {
      setSaving(false);
    }
  }

  if (!matrix) {
    return (
      <CollapsibleSection index="B" label="Seeding Matrix">
        <div className="panel p-5 caption">{error ? error : "Loading…"}</div>
      </CollapsibleSection>
    );
  }

  const updateModel = (i: number, patch: Partial<CatalogModelEntry>) => {
    const next = [...matrix.models];
    next[i] = { ...next[i], ...patch };
    setMatrix({ ...matrix, models: next });
  };
  const addModel = () =>
    setMatrix({ ...matrix, models: [...matrix.models, { hf_id: "", enabled: true }] });
  const removeModel = (i: number) =>
    setMatrix({ ...matrix, models: matrix.models.filter((_, j) => j !== i) });

  const addInstance = () =>
    setMatrix({ ...matrix, instance_types: [...matrix.instance_types, { name: "", enabled: true }] });
  const updateInstance = (i: number, name: string) => {
    const next = [...matrix.instance_types];
    next[i] = { ...next[i], name };
    setMatrix({ ...matrix, instance_types: next });
  };
  const removeInstance = (i: number) =>
    setMatrix({ ...matrix, instance_types: matrix.instance_types.filter((_, j) => j !== i) });

  // Instance types already selected in the matrix — used to dim the dropdown
  // option so operators don't add duplicates. (The backend rejects dupes too.)
  const selectedInstanceNames = new Set(matrix.instance_types.map((it) => it.name));

  return (
    <CollapsibleSection
      index="B"
      label="Seeding Matrix"
      action={
        <div className="flex items-center gap-3">
          {savedFlash && <span className="font-mono text-[11px] tracking-mech uppercase text-signal">SAVED</span>}
          {error && <span className="font-mono text-[11.5px] text-danger">{error}</span>}
          <button onClick={handleSave} disabled={saving} className="btn btn-primary">
            {saving ? "SAVING…" : "SAVE"}
          </button>
        </div>
      }
    >
      <div className="panel p-5 mb-4">
        <div className="eyebrow mb-3">DEFAULTS</div>
        <div className="grid grid-cols-2 gap-4">
          <LabeledInput
            label="Framework Version"
            value={matrix.defaults.framework_version}
            onChange={(v) => setMatrix({ ...matrix, defaults: { ...matrix.defaults, framework_version: v } })}
          />
          <LabeledInput
            label="Scenario (default)"
            value={matrix.defaults.scenario}
            onChange={(v) => setMatrix({ ...matrix, defaults: { ...matrix.defaults, scenario: v } })}
          />
          <LabeledInput
            label="Dataset"
            value={matrix.defaults.dataset}
            onChange={(v) => setMatrix({ ...matrix, defaults: { ...matrix.defaults, dataset: v } })}
          />
          <LabeledInput
            label="Min Duration (seconds)"
            value={String(matrix.defaults.min_duration_seconds)}
            onChange={(v) =>
              setMatrix({ ...matrix, defaults: { ...matrix.defaults, min_duration_seconds: parseInt(v) || 0 } })
            }
            type="number"
          />
        </div>
      </div>

      <div className="panel p-5 mb-4">
        <div className="flex items-center justify-between mb-3">
          <span className="eyebrow">MODELS ({matrix.models.length})</span>
          <button onClick={addModel} className="btn btn-ghost">+ ADD</button>
        </div>
        <p className="meta mb-3">
          Search HuggingFace as you type. Any model in this list is included in seeding — to
          exclude one, remove its row.
        </p>
        <div className="space-y-2">
          {matrix.models.map((m, i) => (
            <div key={i} className="grid grid-cols-[1fr_auto] gap-2 items-start">
              <ModelCombobox
                value={m.hf_id}
                onChange={(hfID) => updateModel(i, { hf_id: hfID })}
              />
              <button
                onClick={() => removeModel(i)}
                className="btn btn-ghost text-danger mt-0.5"
                title="Remove"
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>

      <div className="panel p-5">
        <div className="flex items-center justify-between mb-3">
          <span className="eyebrow">INSTANCE TYPES ({matrix.instance_types.length})</span>
          <button onClick={addInstance} className="btn btn-ghost">+ ADD</button>
        </div>
        <p className="meta mb-3">
          Pick from AWS accelerated instance families. Any type in this list is included in
          seeding — to exclude one, remove its row.
        </p>
        <div className="space-y-1">
          {matrix.instance_types.map((it, i) => (
            <div key={i} className="grid grid-cols-[1fr_auto] gap-2 items-center">
              <select
                value={it.name}
                onChange={(e) => updateInstance(i, e.target.value)}
                className="input w-full"
              >
                <option value="">— select instance type —</option>
                {availableInstances.map((opt) => {
                  const alreadyUsed = opt.name !== it.name && selectedInstanceNames.has(opt.name);
                  return (
                    <option key={opt.name} value={opt.name} disabled={alreadyUsed}>
                      {opt.name}
                      {opt.accelerator_name ? ` · ${opt.accelerator_count ?? ""}x ${opt.accelerator_name}` : ""}
                      {alreadyUsed ? " (already added)" : ""}
                    </option>
                  );
                })}
              </select>
              <button
                onClick={() => removeInstance(i)}
                className="btn btn-ghost text-danger"
                title="Remove"
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>
    </CollapsibleSection>
  );
}

function LabeledInput({
  label, value, onChange, type = "text",
}: { label: string; value: string; onChange: (v: string) => void; type?: string }) {
  return (
    <label className="block">
      <div className="caption mb-1">{label}</div>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="input w-full"
      />
    </label>
  );
}

/* ------------------ Scenario Overrides card (PRD-32) ------------------ */

function ScenarioOverridesCard() {
  const [entries, setEntries] = useState<ScenarioOverrideEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setEntries(await listScenarioOverrides());
    } catch (err: any) {
      setError(err.message || "Failed to load");
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  if (!entries) {
    return (
      <CollapsibleSection index="C" label="Scenario Overrides">
        <div className="panel p-5 caption">{error ? error : "Loading…"}</div>
      </CollapsibleSection>
    );
  }

  return (
    <CollapsibleSection index="C" label="Scenario Overrides">
      <div className="panel p-5">
        <p className="meta mb-4 max-w-2xl">
          Each scenario's stage shape, dataset, and description are defined in code. These knobs can be
          overridden per-scenario from here. Leave a field blank to inherit the code default shown as placeholder.
        </p>
        <div className="space-y-4">
          {entries.map((e) => (
            <ScenarioRow key={e.scenario_id} entry={e} onChanged={refresh} />
          ))}
        </div>
      </div>
    </CollapsibleSection>
  );
}

function ScenarioRow({ entry, onChanged }: { entry: ScenarioOverrideEntry; onChanged: () => void }) {
  const ov = entry.override;
  const [numWorkers, setNumWorkers] = useState(ov?.num_workers != null ? String(ov.num_workers) : "");
  const [streaming, setStreaming] = useState<"" | "true" | "false">(
    ov?.streaming == null ? "" : ov.streaming ? "true" : "false",
  );
  const [inputMean, setInputMean] = useState(ov?.input_mean != null ? String(ov.input_mean) : "");
  const [outputMean, setOutputMean] = useState(ov?.output_mean != null ? String(ov.output_mean) : "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const hasOverride = !!entry.override;

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      const patch: Record<string, unknown> = {};
      patch.num_workers = numWorkers ? parseInt(numWorkers) : null;
      patch.streaming = streaming === "" ? null : streaming === "true";
      patch.input_mean = inputMean ? parseInt(inputMean) : null;
      patch.output_mean = outputMean ? parseInt(outputMean) : null;
      await putScenarioOverride(entry.scenario_id, patch);
      onChanged();
    } catch (err: any) {
      setError(err.message || "Save failed");
    } finally {
      setSaving(false);
    }
  }

  async function handleReset() {
    if (!confirm(`Reset overrides for ${entry.scenario_id}? The scenario returns to its code-defined values.`)) return;
    setSaving(true);
    setError(null);
    try {
      await deleteScenarioOverride(entry.scenario_id);
      setNumWorkers("");
      setStreaming("");
      setInputMean("");
      setOutputMean("");
      onChanged();
    } catch (err: any) {
      setError(err.message || "Reset failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="border border-line/60 p-4">
      <div className="flex items-baseline justify-between mb-3">
        <div className="flex items-baseline gap-3">
          <span className="font-mono text-[13px] text-ink-0">{entry.scenario_id}</span>
          <span className="caption">{entry.name}</span>
          {hasOverride && (
            <span className="font-mono text-[10px] tracking-widemech uppercase text-signal">OVERRIDDEN</span>
          )}
        </div>
        <div className="flex gap-2">
          {hasOverride && (
            <button onClick={handleReset} disabled={saving} className="btn btn-ghost">
              RESET
            </button>
          )}
          <button onClick={handleSave} disabled={saving} className="btn">
            {saving ? "SAVING…" : "SAVE"}
          </button>
        </div>
      </div>

      <div className="grid grid-cols-4 gap-3">
        <label className="block">
          <div className="caption mb-1">num_workers</div>
          <input
            type="number"
            value={numWorkers}
            onChange={(e) => setNumWorkers(e.target.value)}
            placeholder={String(entry.defaults.num_workers)}
            className="input w-full"
          />
        </label>
        <label className="block">
          <div className="caption mb-1">streaming</div>
          <select
            value={streaming}
            onChange={(e) => setStreaming(e.target.value as "" | "true" | "false")}
            className="input w-full"
          >
            <option value="">inherit ({entry.defaults.streaming ? "on" : "off"})</option>
            <option value="true">on</option>
            <option value="false">off</option>
          </select>
        </label>
        <label className="block">
          <div className="caption mb-1">input_mean</div>
          <input
            type="number"
            value={inputMean}
            onChange={(e) => setInputMean(e.target.value)}
            placeholder={String(entry.defaults.input_mean)}
            className="input w-full"
          />
        </label>
        <label className="block">
          <div className="caption mb-1">output_mean</div>
          <input
            type="number"
            value={outputMean}
            onChange={(e) => setOutputMean(e.target.value)}
            placeholder={String(entry.defaults.output_mean)}
            className="input w-full"
          />
        </label>
      </div>

      {error && <div className="caption text-danger mt-2">{error}</div>}
    </div>
  );
}

/* ------------------------- Registry card (PRD-32) --------------------- */

function RegistryCard() {
  const [reg, setReg] = useState<RegistryStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    getRegistry().then(setReg).catch((e) => setError(e.message || "Failed to load"));
  }, []);

  if (!reg) {
    return (
      <CollapsibleSection index="D" label="Registry">
        <div className="panel p-5 caption">{error ? error : "Loading…"}</div>
      </CollapsibleSection>
    );
  }

  async function copyHint() {
    if (!reg?.helm_hint) return;
    try {
      await navigator.clipboard.writeText(reg.helm_hint);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  }

  if (!reg.enabled) {
    return (
      <CollapsibleSection index="D" label="Registry">
        <div className="panel p-5">
          <div className="flex items-baseline gap-3 mb-3">
            <span className="font-mono text-[12.5px] text-ink-0">Pull-through cache</span>
            <span className="font-mono text-[11px] tracking-mech uppercase text-ink-2">DISABLED</span>
          </div>
          <p className="meta mb-3 max-w-xl">
            PRD-29's Docker Hub pull-through cache is not currently enabled on this cluster. To enable:
          </p>
          <div className="flex items-center gap-2">
            <code className="flex-1 bg-surface-0 border border-line p-2 font-mono text-[11px] text-ink-1 whitespace-pre-wrap">
              {reg.helm_hint}
            </code>
            <button onClick={copyHint} className="btn">{copied ? "COPIED" : "COPY"}</button>
          </div>
        </div>
      </CollapsibleSection>
    );
  }

  return (
    <CollapsibleSection index="D" label="Registry">
      <div className="panel p-5">
        <div className="flex items-baseline gap-3 mb-2">
          <span className="font-mono text-[12.5px] text-ink-0">Pull-through cache</span>
          <span className="font-mono text-[11px] tracking-mech uppercase text-signal">ENABLED</span>
        </div>
        <div className="meta mb-4 font-mono break-all">{reg.uri}</div>

        <div className="eyebrow mb-2">CACHED REPOSITORIES ({reg.repositories?.length ?? 0})</div>
        {(!reg.repositories || reg.repositories.length === 0) ? (
          <p className="caption">No repos cached yet — they appear after the first pull.</p>
        ) : (
          <div className="space-y-1">
            {reg.repositories.map((r) => (
              <div key={r.name} className="grid grid-cols-[1fr_120px_1fr] items-baseline gap-3">
                <span className="path">{r.name}</span>
                <span className="caption tabular">{formatBytes(r.size_bytes)}</span>
                <span className="caption">
                  {r.last_pulled_at ? `last pulled ${formatDate(r.last_pulled_at)}` : "never pulled"}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </CollapsibleSection>
  );
}

function formatBytes(n: number): string {
  if (n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i > 1 ? 1 : 0)} ${units[i]}`;
}

/* ---------------------- Audit log accordion (PRD-32) ------------------ */

/* -------------------- Capacity Reservations card (PRD-33) ------------- */

function CapacityReservationsCard() {
  const [pools, setPools] = useState<NodePoolReservations[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      setPools(await listCapacityReservations());
    } catch (err: any) {
      setError(err.message || "Failed to load capacity reservations");
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  if (!pools) {
    return (
      <CollapsibleSection index="E" label="Capacity Reservations">
        <div className="panel p-5 caption">{error ? error : "Loading…"}</div>
      </CollapsibleSection>
    );
  }

  return (
    <CollapsibleSection index="E" label="Capacity Reservations">
      <div className="panel p-5">
        <p className="meta mb-4 max-w-2xl">
          Attach EC2 on-demand capacity reservations (ODCRs) or Capacity Blocks for ML (CBRs) to the
          GPU and Neuron Karpenter NodeClasses. Karpenter prioritizes reserved capacity over
          on-demand; fallback to on-demand happens automatically when reservations are exhausted.
        </p>
        {error && <p className="caption text-danger mb-3">{error}</p>}

        <div className="space-y-6">
          {pools.map((np) => (
            <NodePoolReservationsBlock key={np.node_class} pool={np} onChanged={refresh} />
          ))}
        </div>
      </div>
    </CollapsibleSection>
  );
}

function NodePoolReservationsBlock({
  pool,
  onChanged,
}: {
  pool: NodePoolReservations;
  onChanged: () => void;
}) {
  const [showForm, setShowForm] = useState(false);
  const [newID, setNewID] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  async function handleAttach(e: React.FormEvent) {
    e.preventDefault();
    const id = newID.trim();
    if (!id) return;
    setSubmitting(true);
    setFormError(null);
    try {
      await attachCapacityReservation(pool.node_class, id);
      setNewID("");
      setShowForm(false);
      onChanged();
    } catch (err: any) {
      setFormError(err.message || "Attach failed");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDetach(id: string) {
    if (!confirm(`Remove reservation ${id} from ${pool.node_class}?`)) return;
    try {
      await detachCapacityReservation(pool.node_class, id);
      onChanged();
    } catch (err: any) {
      // Inline per-row error handling is noisy; surface via alert for now.
      alert(err.message || "Detach failed");
    }
  }

  return (
    <div className="border border-line/60 p-4">
      <div className="flex items-baseline justify-between mb-2">
        <div className="flex items-baseline gap-3">
          <span className="font-mono text-[13px] text-ink-0">{pool.node_class}</span>
          <span className="caption uppercase">NodePool</span>
        </div>
        {!showForm && (
          <button onClick={() => setShowForm(true)} className="btn btn-ghost">+ ATTACH RESERVATION</button>
        )}
      </div>

      <div className="caption mb-3">
        <span className="text-ink-2">families:</span> {pool.instance_families.join(", ") || "—"}
        <span className="mx-2 text-ink-2">·</span>
        <span className="text-ink-2">AZs:</span> {pool.subnet_azs.join(", ") || "—"}
        {!pool.capacity_type_includes_reserved && (
          <>
            <span className="mx-2 text-ink-2">·</span>
            <span className="text-danger">capacity-type does NOT include 'reserved'</span>
          </>
        )}
      </div>

      {showForm && (
        <form onSubmit={handleAttach} className="flex items-start gap-2 mb-3">
          <input
            value={newID}
            onChange={(e) => setNewID(e.target.value)}
            placeholder="cr-0abc1234567890abc"
            autoFocus
            className="input flex-1 font-mono"
          />
          <button type="submit" disabled={submitting || !newID.trim()} className="btn btn-primary">
            {submitting ? "ATTACHING…" : "ATTACH"}
          </button>
          <button
            type="button"
            onClick={() => { setShowForm(false); setNewID(""); setFormError(null); }}
            className="btn btn-ghost"
          >
            CANCEL
          </button>
        </form>
      )}
      {formError && <div className="caption text-danger mb-3">{formError}</div>}

      {pool.reservations.length === 0 ? (
        <p className="caption">No reservations attached.</p>
      ) : (
        <div className="space-y-2">
          {pool.reservations.map((r) => (
            <ReservationRow key={r.id} res={r} onDetach={() => handleDetach(r.id)} />
          ))}
        </div>
      )}
    </div>
  );
}

function ReservationRow({ res, onDetach }: { res: ReservationSummary; onDetach: () => void }) {
  const stateClass =
    res.state === "active" ? "text-signal" :
    res.state === "scheduled" || res.state === "pending" || res.state === "payment-pending" ? "text-warn" :
    res.state === "unknown" ? "text-ink-2" :
    "text-danger";

  const typeLabel = res.type === "capacity-block" ? "CAPACITY BLOCK" : res.type === "default" ? "ODCR" : res.type.toUpperCase();

  return (
    <div className="bg-surface-0 border border-line p-3">
      <div className="flex items-baseline justify-between gap-2 mb-1">
        <div className="flex items-baseline gap-3 min-w-0">
          <span className="path">{res.id}</span>
          <span className={`font-mono text-[10px] tracking-widemech uppercase ${stateClass}`}>
            {res.state}
          </span>
          <span className="caption">{typeLabel}</span>
        </div>
        <button onClick={onDetach} className="btn btn-ghost text-danger" title="Remove">×</button>
      </div>
      {res.state !== "unknown" ? (
        <div className="caption">
          <span>{res.instance_type || "—"}</span>
          <span className="mx-2 text-ink-2">·</span>
          <span>{res.availability_zone || "—"}</span>
          <span className="mx-2 text-ink-2">·</span>
          <span className="tabular">
            {res.available_instance_count} / {res.total_instance_count} available
          </span>
          {(res.start_date || res.end_date) && (
            <>
              <span className="mx-2 text-ink-2">·</span>
              <span>
                {res.start_date ? formatDate(res.start_date) : "—"}
                {res.end_date ? ` → ${formatDate(res.end_date)}` : " (no end)"}
              </span>
            </>
          )}
        </div>
      ) : (
        <div className="caption text-ink-2">
          EC2 returned no data for this reservation (may be in another region or access denied).
          Remove to clean up.
        </div>
      )}
      {res.drain_warning_at && (
        <div className="caption text-warn mt-1">
          ⚠ Karpenter drains nodes at {formatDate(res.drain_warning_at)} (~40 min before capacity block ends)
        </div>
      )}
    </div>
  );
}

function AuditLogAccordion() {
  const [entries, setEntries] = useState<AuditLogEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [requested, setRequested] = useState(false);

  // Lazy-load entries the first time the section opens. We can't cleanly
  // observe the open state from inside CollapsibleSection, so trigger the
  // fetch on first render and stop there.
  useEffect(() => {
    if (requested) return;
    setRequested(true);
    listAuditLog(50).then(setEntries).catch((e) => setError(e.message || "Failed to load"));
  }, [requested]);

  return (
    <CollapsibleSection index="F" label="Audit Log">
      <div className="panel p-5">
        {entries === null && !error && <p className="caption">Loading…</p>}
        {error && <p className="caption text-danger">{error}</p>}
        {entries !== null && entries.length === 0 && <p className="caption">No entries yet.</p>}
        {entries !== null && entries.length > 0 && (
          <div className="space-y-1">
            {entries.map((e) => (
              <div key={e.id} className="grid grid-cols-[180px_1fr_1fr] gap-3 font-mono text-[11.5px]">
                <span className="text-ink-2 tabular">{formatDate(e.at)}</span>
                <span className="text-ink-1 truncate">{e.action}</span>
                <span className="text-ink-0">{e.summary}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </CollapsibleSection>
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
            <SeedingMatrixCard />
            <ScenarioOverridesCard />
            <RegistryCard />
            <CapacityReservationsCard />
            <AuditLogAccordion />
          </>
        )}
      </div>

      <RotateModalComponent modal={modal} onClose={() => setModal(null)} onSaved={refresh} />
    </>
  );
}
