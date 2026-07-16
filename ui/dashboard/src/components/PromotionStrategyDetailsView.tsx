import React from 'react';
import Card from '@lib/components/Card';
import { type PromotionStrategy } from '@shared/utils/PSData';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy;
  highlightBranch?: string;
  onFocusChange?: (_branch: string | null) => void;
}

export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
  highlightBranch,
  onFocusChange,
}) => {
  //Pass raw data
  const environments = strategy.status?.environments || [];
  return (
    <Card
      environments={environments}
      highlightBranch={highlightBranch}
      onFocusChange={onFocusChange}
    />
  );
};

export default PromotionStrategyDetailsView;
