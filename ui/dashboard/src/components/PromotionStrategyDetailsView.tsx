import React, { useCallback } from 'react';
import Card, { MergeResult } from '@lib/components/Card';
import { type PromotionStrategy } from '@shared/utils/PSData';
import { mergePullRequest } from '../services/api';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy;
}

export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
}) => {
  const handleMerge = useCallback(async (branch: string): Promise<MergeResult> => {
    const response = await mergePullRequest({
      namespace: strategy.metadata?.namespace || '',
      promotionStrategy: strategy.metadata?.name || '',
      branch,
    });

    return {
      success: response.state === 'merged',
      message: response.message,
    };
  }, [strategy.metadata?.namespace, strategy.metadata?.name]);

  if (!strategy) return <div>No strategy found</div>;

  //Pass raw data
  const environments = strategy.status?.environments || [];
  return <Card environments={environments} onMerge={handleMerge} />;
};

export default PromotionStrategyDetailsView; 