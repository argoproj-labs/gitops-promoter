import React, { useEffect, useState } from 'react';
import Card from './Card';
import { enrichPromotionStrategy, type PromotionStrategy } from '../../../shared/src/utils/PSData';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy;
}

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

  return <Card environments={enrichedEnvs} />;
};

export default PromotionStrategyDetailsView;