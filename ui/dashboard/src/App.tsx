import { BrowserRouter, Routes, Route, Navigate, useParams } from 'react-router-dom';
import DashboardPage from './pages/DashboardPage';
import PromotionStrategyPage from './pages/PromotionStrategyPage';
import { TopBar } from '../src/components/TopBar';

function PromotionStrategyPageWithNamespace() {
  const { namespace } = useParams();
  return <PromotionStrategyPage namespace={namespace} />;
}

function App() {
  return (
    <BrowserRouter>
      <TopBar />
      <Routes>
        <Route path="/" element={<Navigate to="/promotion-strategies" replace />} />
        <Route path="/promotion-strategies" element={<DashboardPage />} />
        <Route path="/promotion-strategies/:namespace/:name" element={<PromotionStrategyPageWithNamespace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;