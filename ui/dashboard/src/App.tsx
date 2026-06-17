import { BrowserRouter, Routes, Route, Navigate, useParams } from 'react-router-dom';
import DashboardPage from './pages/DashboardPage';
import PromotionStrategyPage from './pages/PromotionStrategyPage';
import HistoryPage from './pages/HistoryPage';
import { TopBar } from '../src/components/TopBar';

function PromotionStrategyPageWithNamespace() {
  const { namespace } = useParams();
  return <PromotionStrategyPage namespace={namespace} />;
}

function App() {
  // Vite injects BASE_URL at build time. For local dev it's '/' (no-op);
  // for static-host deploys under a subpath (e.g. GitHub Pages) it becomes
  // '/<repo-name>/'. Stripping the trailing slash gives a valid Router
  // basename for both cases.
  const basename = import.meta.env.BASE_URL.replace(/\/$/, '');
  return (
    <BrowserRouter basename={basename}>
      <TopBar />
      <Routes>
        <Route path="/" element={<Navigate to="/promotion-strategies" replace />} />
        <Route path="/promotion-strategies" element={<DashboardPage />} />
        <Route
          path="/promotion-strategies/:namespace/:name"
          element={<PromotionStrategyPageWithNamespace />}
        />
        <Route
          path="/promotion-strategies/:namespace/:name/history/*"
          element={<HistoryPage />}
        />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
