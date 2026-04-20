import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import Dashboard from "./pages/Dashboard";
import Catalog from "./pages/Catalog";
import Compare from "./pages/Compare";
import Estimate from "./pages/Estimate";
import Run from "./pages/Run";
import ResultDetail from "./pages/ResultDetail";
import SuiteResults from "./pages/SuiteResults";
import Runs from "./pages/Runs";
import ModelCachePage from "./pages/ModelCache";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          {/* New IA */}
          <Route path="/" element={<Dashboard />} />
          <Route path="/run" element={<Run />} />
          <Route path="/runs" element={<Runs />} />
          <Route path="/models" element={<ModelCachePage />} />
          <Route path="/estimate" element={<Estimate />} />
          <Route path="/catalog" element={<Catalog />} />

          {/* Contextual routes */}
          <Route path="/compare" element={<Compare />} />
          <Route path="/results/:id" element={<ResultDetail />} />
          <Route path="/suite-runs/:id" element={<SuiteResults />} />

          {/* Back-compat redirects for old paths */}
          <Route path="/jobs" element={<Navigate to="/runs" replace />} />
          <Route path="/model-cache" element={<Navigate to="/models" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
