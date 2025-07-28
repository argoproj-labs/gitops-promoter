import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { PromotionStrategy } from '@shared/utils/PSData';
import { PromotionStrategyTile } from '../PromotionStrategySummary/PromotionStrategyTile';
import { enrichPromotionStrategy } from '@shared/utils/PSData';
import { getLastCommitTime } from './promotionStrategyUtils';
import { formatDate } from '@shared/utils/util';
import './PromotionStrategyTiles.scss';

export interface PromotionStrategyTilesProps {
  promotionStrategies: PromotionStrategy[];
  namespace: string;
}

export const PromotionStrategiesTiles: React.FC<PromotionStrategyTilesProps> = ({ promotionStrategies, namespace }) => {
  const [enrichedList, setEnrichedList] = useState<any[][]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    async function enrichAll() {
      const results = await Promise.all(
        promotionStrategies.map(ps => enrichPromotionStrategy(ps))
      );
      
      setEnrichedList(results);
    }
    enrichAll();
  }, [promotionStrategies]);

  return (
    <div className="applications-tiles">
      {promotionStrategies.map((ps, idx) => {


        const enriched = enrichedList[idx]?.[0]; // Use the first env for summary tile
        const lastCommitTime = getLastCommitTime(ps);
        const lastUpdated = lastCommitTime ? formatDate(lastCommitTime.toISOString()) : '-';
        const phase = enriched?.promotionStatus || 'unknown';
        return (
          <PromotionStrategyTile
            key={ps.metadata?.name || `ps-${idx}`}
            ps={ps}
            namespace={namespace}
            borderStatus={phase}
            promotedPhase={phase}
            lastUpdated={lastUpdated}
            enrichedEnvList={enrichedList[idx]}
            onClick={() => navigate(`/promotion-strategies/${namespace}/${ps.metadata?.name || ''}`)}
          />
        );
      })}
    </div>
  );
};

export default PromotionStrategiesTiles; 