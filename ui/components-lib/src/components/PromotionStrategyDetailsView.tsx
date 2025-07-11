import React, { useEffect, useState } from 'react';
import EnvironmentCard from './EnvironmentCard';
import { enrichPromotionStrategy } from '../../../shared/src/utils/PSData';
import type { PromotionStrategyType } from '../../../shared/src/models/PromotionStrategyType';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategyType;
}

// This component receives a PromotionStrategy
export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
}) => {
  const [enrichedEnvs, setEnrichedEnvs] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function enrich() {
      if (strategy) {
        setLoading(true);
        const enriched = await enrichPromotionStrategy(strategy);
        setEnrichedEnvs(enriched);
        setLoading(false);
      }
    }
    enrich();
  }, [strategy]);

  if (!strategy) return <div>No strategy found</div>;

  if (loading) return <div>Loading environment details...</div>;

  return <EnvironmentCard environments={enrichedEnvs} />;
};

export default PromotionStrategyDetailsView;