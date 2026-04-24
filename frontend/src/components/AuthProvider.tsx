// PRD-43: auth context + silent refresh timer.
//
// On mount, calls GET /api/v1/auth/me to restore the session. If the
// user is present, starts a setInterval that silently refreshes the
// access token every 10 minutes. Access tokens last 60 minutes, so
// this is conservative — 5 successful refreshes per hour keeps the
// session alive without interaction.

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { authLogin, authLogout, authMe, authRefresh } from "../api";
import type { AuthUser } from "../types";

type AuthState = {
  user: AuthUser | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
};

const AuthCtx = createContext<AuthState | undefined>(undefined);

const REFRESH_INTERVAL_MS = 10 * 60 * 1000;

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);

  // Session restore on mount.
  useEffect(() => {
    let cancelled = false;
    authMe()
      .then((u) => {
        if (!cancelled) setUser(u);
      })
      .catch(() => {
        // 401 on /auth/me just means "not logged in" — AuthGate handles
        // the redirect. We don't treat it as an error state.
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Silent refresh loop — only runs while user is populated.
  useEffect(() => {
    if (!user) return;
    const id = setInterval(async () => {
      const ok = await authRefresh();
      if (!ok) {
        setUser(null);
      }
    }, REFRESH_INTERVAL_MS);
    return () => clearInterval(id);
  }, [user]);

  const login = useCallback(async (email: string, password: string) => {
    const u = await authLogin(email, password);
    setUser(u);
  }, []);

  const logout = useCallback(async () => {
    await authLogout();
    setUser(null);
    window.location.href = "/login";
  }, []);

  const value = useMemo<AuthState>(
    () => ({ user, loading, login, logout }),
    [user, loading, login, logout]
  );

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthCtx);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
