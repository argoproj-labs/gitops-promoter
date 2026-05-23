import React from 'react';
import { useNavigate } from 'react-router-dom';
import Card from '@lib/components/Card';
import { type PromotionStrategy } from '@shared/utils/PSData';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy;
}

export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
}) => {
  const navigate = useNavigate();

  if (!strategy) return <div>No strategy found</div>;

  const environments = strategy.status?.environments || [];
  const ns = strategy.metadata?.namespace || '';
  const name = strategy.metadata?.name || '';

  // Card invokes this when its History button is clicked. Routing here keeps
  // the per-environment card free of router knowledge and lets callers that
  // don't pass a handler (e.g. tests, the Argo CD extension) fall back to the
  // in-place side panel rendered by Card itself.
  const handleHistoryNavigate = (_branch: string) => {
    navigate(`/promotion-strategies/${ns}/${name}/history`);
  };

  return <Card environments={environments} onHistoryNavigate={handleHistoryNavigate} />;
};

export default PromotionStrategyDetailsView;
