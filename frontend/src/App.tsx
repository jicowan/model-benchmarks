import { BrowserRouter, Routes, Route } from "react-router-dom";
import Layout from "./components/Layout";
import Catalog from "./pages/Catalog";
import Compare from "./pages/Compare";
import Run from "./pages/Run";
import ResultDetail from "./pages/ResultDetail";
import Jobs from "./pages/Jobs";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Catalog />} />
          <Route path="/compare" element={<Compare />} />
          <Route path="/run" element={<Run />} />
          <Route path="/results/:id" element={<ResultDetail />} />
          <Route path="/jobs" element={<Jobs />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
