import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import { AuthProvider } from "./components/AuthProvider";
import AuthGate from "./components/AuthGate";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import Catalog from "./pages/Catalog";
import Compare from "./pages/Compare";
import Estimate from "./pages/Estimate";
import Run from "./pages/Run";
import ResultDetail from "./pages/ResultDetail";
import SuiteResults from "./pages/SuiteResults";
import Runs from "./pages/Runs";
import ModelCachePage from "./pages/ModelCache";
import Configuration from "./pages/Configuration";

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          {/* PRD-43: /login is the only unauthenticated route. */}
          <Route path="/login" element={<Login />} />

          {/* Everything else requires an authenticated user. */}
          <Route element={<AuthGate><Layout /></AuthGate>}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/run" element={<Run />} />
            <Route path="/runs" element={<Runs />} />
            <Route path="/models" element={<ModelCachePage />} />
            <Route path="/estimate" element={<Estimate />} />
            <Route path="/catalog" element={<Catalog />} />
            <Route path="/configuration" element={<Configuration />} />

            {/* Contextual routes */}
            <Route path="/compare" element={<Compare />} />
            <Route path="/results/:id" element={<ResultDetail />} />
            <Route path="/suite-runs/:id" element={<SuiteResults />} />

            {/* Back-compat redirects for old paths */}
            <Route path="/jobs" element={<Navigate to="/runs" replace />} />
            <Route path="/model-cache" element={<Navigate to="/models" replace />} />
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}
