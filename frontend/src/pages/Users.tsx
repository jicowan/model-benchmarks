import { useCallback, useEffect, useRef, useState } from "react";
import type { CognitoUser } from "../types";
import {
  listUsers,
  createUser,
  updateUserRole,
  disableUser,
  enableUser,
  resetUserPassword,
  deleteUser,
} from "../api";
import { useAuth } from "../components/AuthProvider";

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

/* ----------------------------- Utilities ----------------------------- */

function timeAgo(iso: string): string {
  if (!iso) return "—";
  const sec = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  const min = sec / 60;
  if (min < 60) return `${Math.floor(min)}m ago`;
  const hr = min / 60;
  if (hr < 24) return `${Math.floor(hr)}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

/* ------------------------------- Users ------------------------------- */

export default function Users() {
  const { user: authUser } = useAuth();
  const [rows, setRows] = useState<CognitoUser[]>([]);
  const [nextToken, setNextToken] = useState<string>("");
  const [tokenStack, setTokenStack] = useState<string[]>([]); // for Prev
  const [filter, setFilter] = useState("");
  const [debouncedFilter, setDebouncedFilter] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [inviting, setInviting] = useState(false);
  // Set of selected subs. Actions from the header Actions menu apply to
  // every selected row that passes the self-mutation guard for that
  // action (the backend enforces the guard too).
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // 300ms debounce on the search input.
  useEffect(() => {
    const id = setTimeout(() => setDebouncedFilter(filter), 300);
    return () => clearTimeout(id);
  }, [filter]);

  // When the filter changes, reset pagination to the first page.
  const fetchPage = useCallback(
    async (token: string) => {
      setLoading(true);
      setError("");
      try {
        const resp = await listUsers({ limit: 60, next_token: token || undefined, filter: debouncedFilter || undefined });
        setRows(resp.rows);
        setNextToken(resp.next_token ?? "");
      } catch (e) {
        setError(e instanceof Error ? e.message : "Failed to load users");
      } finally {
        setLoading(false);
      }
    },
    [debouncedFilter]
  );

  useEffect(() => {
    setTokenStack([]);
    fetchPage("");
  }, [fetchPage]);

  const goNext = () => {
    if (!nextToken) return;
    setTokenStack((s) => [...s, nextToken]);
    fetchPage(nextToken);
  };
  const goStart = () => {
    setTokenStack([]);
    fetchPage("");
  };

  // Refresh with 5s cooldown.
  const lastRefresh = useRef(0);
  const refresh = () => {
    const now = Date.now();
    if (now - lastRefresh.current < 5000) return;
    lastRefresh.current = now;
    const top = tokenStack[tokenStack.length - 1] ?? "";
    fetchPage(top);
  };

  const isSelf = (sub: string) => authUser?.sub === sub;

  return (
    <>
      <PageHeader path={["accelbench", "users"]} />

      <div className="p-6 max-w-[1400px] mx-auto animate-enter">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex-1 flex items-center gap-2">
            <input
              type="search"
              placeholder="Filter by email prefix…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              className="w-64 px-3 py-2 bg-surface-1 border border-line font-mono text-[12px] text-ink-0 placeholder:text-ink-2"
            />
            <button onClick={refresh} className="btn btn-ghost" type="button">
              REFRESH
            </button>
          </div>
          <BulkActions
            selected={selected}
            rows={rows}
            isSelf={isSelf}
            onDone={() => {
              setSelected(new Set());
              refresh();
            }}
          />
          <button onClick={() => setInviting(true)} className="btn btn-primary" type="button">
            + INVITE USER
          </button>
        </div>

        {error && <div className="caption text-danger mb-3">{error}</div>}

        <div className="panel overflow-hidden">
          <table className="data-table">
            <thead>
              <tr>
                <th className="w-10">
                  <input
                    type="checkbox"
                    aria-label="select all on this page"
                    checked={rows.length > 0 && rows.every((r) => selected.has(r.sub))}
                    ref={(el) => {
                      if (!el) return;
                      const anySelected = rows.some((r) => selected.has(r.sub));
                      const allSelected = rows.length > 0 && rows.every((r) => selected.has(r.sub));
                      el.indeterminate = anySelected && !allSelected;
                    }}
                    onChange={(e) => {
                      setSelected((prev) => {
                        const copy = new Set(prev);
                        if (e.target.checked) rows.forEach((r) => copy.add(r.sub));
                        else rows.forEach((r) => copy.delete(r.sub));
                        return copy;
                      });
                    }}
                  />
                </th>
                <th>EMAIL</th>
                <th className="w-32">ROLE</th>
                <th className="w-40">STATUS</th>
                <th className="w-28">CREATED</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={5} className="text-center py-8 caption">
                    Loading…
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={5} className="text-center py-8 caption">
                    No users found.
                  </td>
                </tr>
              ) : (
                rows.map((u) => (
                  <UserRow
                    key={u.sub}
                    user={u}
                    isSelf={isSelf(u.sub)}
                    selected={selected.has(u.sub)}
                    onSelect={(next) => {
                      setSelected((prev) => {
                        const copy = new Set(prev);
                        if (next) copy.add(u.sub);
                        else copy.delete(u.sub);
                        return copy;
                      });
                    }}
                    onChanged={refresh}
                  />
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination controls */}
        {(nextToken || tokenStack.length > 0) && (
          <div className="mt-4 flex items-center justify-between font-mono text-[11px] tracking-mech">
            <button
              onClick={goStart}
              disabled={tokenStack.length === 0}
              className="btn btn-ghost disabled:opacity-40"
              type="button"
            >
              ← BACK TO START
            </button>
            <span className="caption">
              page {tokenStack.length + 1}
              {nextToken ? "" : " (end)"}
            </span>
            <button onClick={goNext} disabled={!nextToken} className="btn btn-ghost disabled:opacity-40" type="button">
              NEXT →
            </button>
          </div>
        )}
      </div>

      {inviting && (
        <InviteModal
          onClose={() => setInviting(false)}
          onInvited={() => {
            setInviting(false);
            goStart();
          }}
        />
      )}
    </>
  );
}

/* ------------------------------ UserRow ------------------------------ */

function UserRow({
  user,
  isSelf,
  selected,
  onSelect,
  onChanged,
}: {
  user: CognitoUser;
  isSelf: boolean;
  selected: boolean;
  onSelect: (next: boolean) => void;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);

  const flip = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      onChanged();
    } catch (e) {
      alert(e instanceof Error ? e.message : "Action failed");
    } finally {
      setBusy(false);
    }
  };

  // Current role on the user; empty attribute is treated as "user"
  // (matches the middleware default at internal/auth/middleware.go).
  const currentRole: "admin" | "user" = user.role === "admin" ? "admin" : "user";
  // Self-demote is the only role change blocked: an admin changing
  // their own role to user would lock them out. Everything else is fine.
  const roleSelectDisabled = busy || (isSelf && currentRole === "admin");

  return (
    <tr className={busy ? "opacity-60" : ""}>
      <td className="w-10">
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onSelect(e.target.checked)}
          aria-label={`select ${user.email}`}
          className="align-middle"
        />
      </td>
      <td>
        <span className="path">{user.email}</span>
        {isSelf && <span className="caption ml-2">(you)</span>}
      </td>
      <td>
        <select
          value={currentRole}
          disabled={roleSelectDisabled}
          onChange={(e) => {
            const next = e.target.value as "admin" | "user";
            if (next === currentRole) return;
            flip(() => updateUserRole(user.sub, next));
          }}
          className="bg-surface-0 border border-line font-mono text-[11px] tracking-widemech px-2 py-1 text-ink-0 disabled:opacity-50 disabled:cursor-not-allowed"
          title={roleSelectDisabled && isSelf ? "You cannot demote yourself" : ""}
        >
          <option value="user">USER</option>
          <option value="admin">ADMIN</option>
        </select>
      </td>
      <td>
        <span className="font-mono text-[11px] tracking-mech">
          {user.enabled ? user.status : `${user.status} · DISABLED`}
        </span>
      </td>
      <td title={user.created_at} className="text-ink-2">{timeAgo(user.created_at)}</td>
    </tr>
  );
}

/* ---------------------------- InviteModal ---------------------------- */

function InviteModal({
  onClose,
  onInvited,
}: {
  onClose: () => void;
  onInvited: () => void;
}) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"admin" | "user">("user");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      await createUser(email, role);
      onInvited();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Invite failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-surface-0/80 z-40 flex items-center justify-center p-6">
      <div className="bg-surface-1 border border-line w-full max-w-md p-6">
        <h3 className="font-sans text-[16px] tracking-mech mb-4">Invite user</h3>
        <label className="block caption mb-1">EMAIL</label>
        <input
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="alice@example.com"
          className="w-full px-3 py-2 bg-surface-0 border border-line font-mono text-[13px] mb-4"
          autoFocus
        />
        <label className="block caption mb-1">ROLE</label>
        <div className="flex gap-2 mb-4">
          {(["user", "admin"] as const).map((r) => (
            <button
              key={r}
              onClick={() => setRole(r)}
              className={`flex-1 py-2 font-mono text-[11px] tracking-widemech border ${
                role === r ? "border-signal text-signal bg-signal/10" : "border-line text-ink-2"
              }`}
              type="button"
            >
              {r.toUpperCase()}
            </button>
          ))}
        </div>
        {error && <div className="caption text-danger mb-3">{error}</div>}
        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="btn" type="button">
            CANCEL
          </button>
          <button
            onClick={submit}
            disabled={busy || !email}
            className="btn btn-primary disabled:opacity-40"
            type="button"
          >
            {busy ? "SENDING…" : "SEND INVITE"}
          </button>
        </div>
      </div>
    </div>
  );
}

/* ---------------------------- BulkActions ---------------------------- */

function BulkActions({
  selected,
  rows,
  isSelf,
  onDone,
}: {
  selected: Set<string>;
  rows: CognitoUser[];
  isSelf: (sub: string) => boolean;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Click-outside + Esc close the dropdown.
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

  // Filter selected to only subs that are actually on the current page
  // (selection survives pagination by design; actions only apply to
  // rows we can resolve to a CognitoUser here).
  const selectedRows = rows.filter((r) => selected.has(r.sub));
  const count = selectedRows.length;
  const disabled = busy || count === 0;

  const run = async (value: string) => {
    setOpen(false);
    if (count === 0) return;
    setBusy(true);
    try {
      for (const r of selectedRows) {
        try {
          if (value === "disable") {
            if (isSelf(r.sub)) continue;
            await disableUser(r.sub);
          } else if (value === "enable") {
            await enableUser(r.sub);
          } else if (value === "reset-pw") {
            await resetUserPassword(r.sub);
          }
        } catch (e) {
          alert(`${r.email}: ${e instanceof Error ? e.message : "failed"}`);
        }
      }
      onDone();
    } finally {
      setBusy(false);
    }
  };

  const runDelete = async () => {
    setBusy(true);
    try {
      for (const r of selectedRows) {
        if (isSelf(r.sub)) continue;
        try {
          await deleteUser(r.sub);
        } catch (e) {
          alert(`${r.email}: ${e instanceof Error ? e.message : "delete failed"}`);
        }
      }
      setConfirmDelete(false);
      onDone();
    } finally {
      setBusy(false);
    }
  };

  // DevExtreme-style split button: one outlined rounded control with two
  // visually-joined segments. Clicking either segment toggles the menu
  // (we don't split into "default action" + "more" because every menu
  // item is a distinct action with different semantics).
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
          className="absolute right-0 top-full mt-1 z-30 min-w-[200px] bg-surface-1 border border-line shadow-lg font-mono text-[12px] tracking-mech"
        >
          <button
            type="button"
            onClick={() => run("reset-pw")}
            className="block w-full text-left px-3 py-2 text-ink-0 hover:bg-surface-2"
          >
            Reset password
          </button>
          <button
            type="button"
            onClick={() => run("disable")}
            className="block w-full text-left px-3 py-2 text-ink-0 hover:bg-surface-2"
          >
            Disable selected
          </button>
          <button
            type="button"
            onClick={() => run("enable")}
            className="block w-full text-left px-3 py-2 text-ink-0 hover:bg-surface-2"
          >
            Enable selected
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
              Delete {count} user{count === 1 ? "" : "s"}?
            </h3>
            <p className="caption mb-4">
              This cannot be undone. The current user (if selected) is skipped automatically.
            </p>
            <ul className="mb-4 max-h-40 overflow-y-auto font-mono text-[12px] text-ink-1">
              {selectedRows.map((r) => (
                <li key={r.sub} className={isSelf(r.sub) ? "line-through text-ink-2" : ""}>
                  {r.email}
                  {isSelf(r.sub) && <span className="caption ml-2">(self — skipped)</span>}
                </li>
              ))}
            </ul>
            <div className="flex justify-end gap-2">
              <button onClick={() => setConfirmDelete(false)} className="btn" type="button">
                CANCEL
              </button>
              <button onClick={runDelete} disabled={busy} className="btn btn-primary disabled:opacity-40" type="button">
                {busy ? "DELETING…" : "DELETE"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

