import { Link, NavLink, Outlet } from "react-router-dom";
import { useEffect, useMemo, useState } from "react";
import ThemeToggle from "./ThemeToggle";
import { useStatus } from "../hooks/useStatus";
import { useAuth } from "./AuthProvider";

type NavItem = {
  to: string;
  label: string;
  icon: React.ReactNode;
  shortcut?: string;
  // PRD-44: items marked adminOnly are hidden for non-admin users
  // (Configuration today; future Users entry lands here too).
  adminOnly?: boolean;
};

const iconCls = "shrink-0";
const navItems: NavItem[] = [
  {
    to: "/",
    label: "Dashboard",
    shortcut: "D",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <rect x="3" y="3" width="8" height="10" />
        <rect x="13" y="3" width="8" height="6" />
        <rect x="3" y="15" width="8" height="6" />
        <rect x="13" y="11" width="8" height="10" />
      </svg>
    ),
  },
  {
    to: "/catalog",
    label: "Benchmarks",
    shortcut: "B",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <path d="M4 4h16v4H4zM4 10h16v4H4zM4 16h16v4H4z" />
      </svg>
    ),
  },
  {
    to: "/models",
    label: "Models",
    shortcut: "M",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <path d="M3 7l9-4 9 4-9 4-9-4z" />
        <path d="M3 12l9 4 9-4" />
        <path d="M3 17l9 4 9-4" />
      </svg>
    ),
  },
  {
    to: "/estimate",
    label: "Estimate",
    shortcut: "E",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5l3 2" />
      </svg>
    ),
  },
  {
    to: "/run",
    label: "New Benchmark",
    shortcut: "N",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <path d="M5 3v18l7-5 7 5V3z" />
      </svg>
    ),
  },
  {
    to: "/runs",
    label: "Runs",
    shortcut: "R",
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <path d="M3 6h18M3 12h18M3 18h18" />
      </svg>
    ),
  },
  {
    to: "/configuration",
    label: "Configuration",
    shortcut: "C",
    adminOnly: true,
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
      </svg>
    ),
  },
  {
    to: "/users",
    label: "Users",
    shortcut: "U",
    adminOnly: true,
    icon: (
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="square" className={iconCls}>
        <circle cx="12" cy="8" r="4" />
        <path d="M4 21v-1a8 8 0 0 1 16 0v1" />
      </svg>
    ),
  },
];

const COLLAPSED_KEY = "accelbench.nav.collapsed";

export default function Layout() {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return localStorage.getItem(COLLAPSED_KEY) === "1";
  });
  const { state: healthState, detail: healthDetail } = useStatus();
  const { isAdmin } = useAuth();

  // PRD-44: filter admin-only entries for non-admin users. Memoized so
  // the keyboard-shortcut effect below and the render loop see the
  // same list.
  const visibleNavItems = useMemo(
    () => navItems.filter((n) => !n.adminOnly || isAdmin()),
    [isAdmin]
  );

  useEffect(() => {
    localStorage.setItem(COLLAPSED_KEY, collapsed ? "1" : "0");
  }, [collapsed]);

  // Keyboard shortcuts: letter keys navigate (not when typing in input/textarea)
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)) {
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const item = visibleNavItems.find((n) => n.shortcut?.toLowerCase() === e.key.toLowerCase());
      if (item) {
        e.preventDefault();
        window.location.hash = "";
        window.history.pushState({}, "", item.to);
        window.dispatchEvent(new PopStateEvent("popstate"));
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [visibleNavItems]);

  const railWidth = collapsed ? "w-14" : "w-56";

  return (
    <div className="min-h-screen flex bg-surface-0 text-ink-0">
      {/* Left rail */}
      <aside
        className={`${railWidth} shrink-0 flex flex-col border-r border-line bg-surface-1 transition-[width] duration-150 sticky top-0 h-screen`}
      >
        {/* Wordmark */}
        <div className="h-14 flex items-center px-4 border-b border-line">
          <Link to="/" className="flex items-center gap-2 group">
            <div className="w-6 h-6 relative flex items-center justify-center">
              <div className="absolute inset-0 rounded-full border-2 border-ink-2 group-hover:border-signal/60 transition-colors" />
              <div className="absolute inset-[3px] rounded-full border border-signal/40" />
              <div className="w-2.5 h-2.5 rounded-full bg-signal animate-hal-iris" />
            </div>
            {!collapsed && (
              <span className="font-mono text-[13px] tracking-widemech text-ink-0 group-hover:text-signal transition-colors">
                ACCELBENCH
              </span>
            )}
          </Link>
        </div>

        {/* Nav items */}
        <nav className="flex-1 py-3 flex flex-col gap-0.5">
          {visibleNavItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              title={collapsed ? item.label : undefined}
              className={({ isActive }) =>
                [
                  "relative flex items-center gap-3 h-9 mx-2 px-2 font-mono text-[12.5px] tracking-mech transition-colors",
                  isActive
                    ? "text-ink-0 bg-surface-2"
                    : "text-ink-1 hover:text-ink-0 hover:bg-surface-2/60",
                ].join(" ")
              }
            >
              {({ isActive }) => (
                <>
                  {/* Active indicator: signal bar on left */}
                  <span
                    aria-hidden
                    className={`absolute left-0 top-1.5 bottom-1.5 w-[2px] ${
                      isActive ? "bg-signal" : "bg-transparent"
                    }`}
                  />
                  {item.icon}
                  {!collapsed && (
                    <>
                      <span className="flex-1">{item.label}</span>
                      {item.shortcut && (
                        <kbd className="opacity-60 group-hover:opacity-100">{item.shortcut}</kbd>
                      )}
                    </>
                  )}
                </>
              )}
            </NavLink>
          ))}
        </nav>

        {/* Footer controls */}
        <div className="border-t border-line p-3 flex flex-col gap-2">
          <div className={`flex ${collapsed ? "flex-col" : "flex-row"} gap-2 items-center justify-between`}>
            <ThemeToggle />
            <button
              onClick={() => setCollapsed(!collapsed)}
              title={collapsed ? "Expand nav" : "Collapse nav"}
              className="w-9 h-9 flex items-center justify-center border border-line bg-surface-1 text-ink-1 hover:text-ink-0 hover:bg-surface-2 hover:border-line-strong transition-colors"
              aria-label={collapsed ? "Expand nav" : "Collapse nav"}
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
                {collapsed ? (
                  <path d="M9 6l6 6-6 6" />
                ) : (
                  <path d="M15 6l-6 6 6 6" />
                )}
              </svg>
            </button>
          </div>
          {!collapsed && (
            <div className="caption pt-1 flex items-center justify-between">
              <span>v0.19.0</span>
              <StatusPill state={healthState} detail={healthDetail} />
            </div>
          )}
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 min-w-0 relative z-10">
        <UserBadge />
        <Outlet />
      </main>
    </div>
  );
}

// PRD-43: user identity + logout. Fixed to the viewport top-right with
// the same h-14 height as every page's sticky PageHeader (z-20) so the
// badge sits on the header's center line. z-30 keeps it above the bar.
function UserBadge() {
  const { user, logout } = useAuth();
  if (!user) return null;
  return (
    <div className="fixed top-0 right-4 z-30 h-14 flex items-center gap-3 font-mono text-[11px] tracking-mech text-ink-2 no-print">
      <span className="hidden sm:inline">{user.email}</span>
      <button
        type="button"
        onClick={logout}
        className="uppercase tracking-widemech text-ink-2 hover:text-signal transition-colors"
      >
        Logout
      </button>
    </div>
  );
}

function StatusPill({
  state,
  detail,
}: {
  state: "ok" | "degraded" | "down" | "unknown";
  detail: import("../types").StatusResponse | null;
}) {
  const { dot, label } = (() => {
    switch (state) {
      case "ok":
        return { dot: "bg-signal animate-pulse_signal", label: "ONLINE" };
      case "degraded":
        return { dot: "bg-warn animate-pulse_signal", label: "DEGRADED" };
      case "down":
        return { dot: "bg-danger", label: "OFFLINE" };
      default:
        return { dot: "bg-ink-2", label: "…" };
    }
  })();

  const tooltip = detail
    ? Object.entries(detail.components)
        .map(([k, c]) => `${k}: ${c.status}${c.latency_ms ? ` (${c.latency_ms}ms)` : ""}${c.error ? ` — ${c.error}` : ""}`)
        .join("\n")
    : "Status check failed";

  return (
    <span className="flex items-center gap-1.5" title={tooltip}>
      <span className={`w-1.5 h-1.5 ${dot}`} />
      {label}
    </span>
  );
}
