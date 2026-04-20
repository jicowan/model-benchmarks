import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import Catalog from "./pages/Catalog";
import Compare from "./pages/Compare";
import Estimate from "./pages/Estimate";
import Run from "./pages/Run";
import ResultDetail from "./pages/ResultDetail";
import SuiteResults from "./pages/SuiteResults";
import Jobs from "./pages/Jobs";
import ModelCachePage from "./pages/ModelCache";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          {/* New IA */}
          <Route path="/" element={<Catalog />} />
          <Route path="/run" element={<Run />} />
          <Route path="/runs" element={<Jobs />} />
          <Route path="/models" element={<ModelCachePage />} />
          <Route path="/estimate" element={<Estimate />} />

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
