// PRD-43: route guard that redirects unauthenticated users to /login.
//
// Wrapped around the app's authenticated route tree. While AuthProvider
// is resolving its initial /auth/me call we render a minimal loading
// state so the page doesn't flash unauthenticated content.

import { Navigate } from "react-router-dom";
import type { ReactNode } from "react";
import { useAuth } from "./AuthProvider";

export default function AuthGate({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-surface-0 text-ink-2 font-mono text-[12px] tracking-widemech">
        AUTHENTICATING…
      </div>
    );
  }

  if (!user) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
