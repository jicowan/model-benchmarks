import { lazy, Suspense } from "react";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import { AuthProvider } from "./components/AuthProvider";
import AuthGate from "./components/AuthGate";
import AdminRoute from "./components/AdminRoute";
import NonViewerRoute from "./components/NonViewerRoute";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";

// PRD-68 P5: route-level code splitting. Login + Dashboard stay in the
// initial chunk (every session lands on one of them); everything else —
// notably the Recharts-heavy report pages and the admin surface — loads on
// first navigation. Cuts the initial bundle roughly in half.
const Catalog = lazy(() => import("./pages/Catalog"));
const Compare = lazy(() => import("./pages/Compare"));
const Estimate = lazy(() => import("./pages/Estimate"));
const Run = lazy(() => import("./pages/Run"));
const Distributed = lazy(() => import("./pages/Distributed"));
const ResultDetail = lazy(() => import("./pages/ResultDetail"));
const DistributedReport = lazy(() => import("./pages/DistributedReport"));
const SuiteResults = lazy(() => import("./pages/SuiteResults"));
const Runs = lazy(() => import("./pages/Runs"));
const ModelCachePage = lazy(() => import("./pages/ModelCache"));
const Configuration = lazy(() => import("./pages/Configuration"));
const Users = lazy(() => import("./pages/Users"));

function RouteFallback() {
  return <div className="p-6 caption">Loading…</div>;
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Suspense fallback={<RouteFallback />}>
        <Routes>
          {/* PRD-43: /login is the only unauthenticated route. */}
          <Route path="/login" element={<Login />} />

          {/* Everything else requires an authenticated user. */}
          <Route element={<AuthGate><Layout /></AuthGate>}>
            <Route path="/" element={<Dashboard />} />
            <Route
              path="/run"
              element={
                <NonViewerRoute>
                  <Run />
                </NonViewerRoute>
              }
            />
            {/* PRD-57: multi-node distributed run composer. */}
            <Route
              path="/distributed"
              element={
                <NonViewerRoute>
                  <Distributed />
                </NonViewerRoute>
              }
            />
            <Route
              path="/runs"
              element={
                <NonViewerRoute>
                  <Runs />
                </NonViewerRoute>
              }
            />
            <Route
              path="/models"
              element={
                <NonViewerRoute>
                  <ModelCachePage />
                </NonViewerRoute>
              }
            />
            <Route
              path="/estimate"
              element={
                <NonViewerRoute>
                  <Estimate />
                </NonViewerRoute>
              }
            />
            <Route path="/catalog" element={<Catalog />} />
            <Route
              path="/configuration"
              element={
                <AdminRoute>
                  <Configuration />
                </AdminRoute>
              }
            />
            <Route
              path="/users"
              element={
                <AdminRoute>
                  <Users />
                </AdminRoute>
              }
            />

            {/* Contextual routes */}
            <Route path="/compare" element={<Compare />} />
            <Route path="/results/:id" element={<ResultDetail />} />
            {/* PRD-59: dedicated distributed / disaggregated run report. */}
            <Route path="/results/:id/distributed" element={<DistributedReport />} />
            <Route path="/suite-runs/:id" element={<SuiteResults />} />

            {/* Back-compat redirects for old paths */}
            <Route path="/jobs" element={<Navigate to="/runs" replace />} />
            <Route path="/model-cache" element={<Navigate to="/models" replace />} />
          </Route>
        </Routes>
        </Suspense>
      </AuthProvider>
    </BrowserRouter>
  );
}
