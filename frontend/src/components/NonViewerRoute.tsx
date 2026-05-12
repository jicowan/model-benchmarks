// PRD-48: route guard that blocks the `viewer` role. Mirrors AdminRoute's
// shape, but redirects viewers to "/" with a flash banner rather than
// blocking on !admin. Used to wrap every page a viewer shouldn't reach:
// Models, Estimate, New Benchmark, Runs. (Users + Configuration stay on
// AdminRoute.)

import { Navigate } from "react-router-dom";
import type { ReactNode } from "react";
import { useAuth } from "./AuthProvider";

export default function NonViewerRoute({ children }: { children: ReactNode }) {
  const { user, isViewer, loading } = useAuth();

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center text-ink-2 font-mono text-[12px] tracking-widemech">
        AUTHORIZING…
      </div>
    );
  }
  if (!user) return <Navigate to="/login" replace />;
  if (isViewer()) return <Navigate to="/?flash=viewer-only" replace />;
  return <>{children}</>;
}
