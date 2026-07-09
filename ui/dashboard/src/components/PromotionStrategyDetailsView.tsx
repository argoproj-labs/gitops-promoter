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

  const environments = strategy.status?.environments || [];
  const ns = strategy.metadata.namespace || '';
  const name = strategy.metadata.name || '';

  const handleHistoryNavigate = (_branch: string) => {
    navigate(`/promotion-strategies/${ns}/${name}/history`);
  };

  return <Card environments={environments} onHistoryNavigate={handleHistoryNavigate} />;
};

export default PromotionStrategyDetailsView;
