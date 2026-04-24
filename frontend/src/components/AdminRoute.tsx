// PRD-44: admin-only route guard. Redirects non-admins to "/" with a
// flash query param so the Dashboard can surface a "admins only"
// banner. Runs inside <AuthGate> which already handles the
// unauthenticated case, but we still fall through to /login if the
// user is somehow null (defensive).

import { Navigate } from "react-router-dom";
import type { ReactNode } from "react";
import { useAuth } from "./AuthProvider";

export default function AdminRoute({ children }: { children: ReactNode }) {
  const { user, isAdmin, loading } = useAuth();

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center text-ink-2 font-mono text-[12px] tracking-widemech">
        AUTHORIZING…
      </div>
    );
  }
  if (!user) return <Navigate to="/login" replace />;
  if (!isAdmin()) return <Navigate to="/?flash=admins-only" replace />;
  return <>{children}</>;
}
