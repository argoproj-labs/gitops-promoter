import React from 'react';
import { useNavigate } from 'react-router-dom';
import type { PromotionStrategy } from '@shared/utils/PSData';
import { PromotionStrategyTile } from '../PromotionStrategySummary/PromotionStrategyTile';
import { getLastCommitTime, formatDate } from '@shared/utils/util';
import './PromotionStrategyTiles.scss';

export interface PromotionStrategyTilesProps {
  promotionStrategies: PromotionStrategy[];
  namespace: string;
}

export const PromotionStrategiesTiles: React.FC<PromotionStrategyTilesProps> = ({ promotionStrategies, namespace }) => {
  const navigate = useNavigate();

  return (
    <div className="applications-tiles">
      {promotionStrategies.map((ps, idx) => {
        const lastCommitTime = getLastCommitTime(ps);
        const lastUpdated = lastCommitTime ? formatDate(lastCommitTime.toISOString()) : '-';
        
        return (
          <PromotionStrategyTile
            key={ps.metadata?.name || `ps-${idx}`}
            ps={ps}
            namespace={namespace}
            borderStatus="default"
            lastUpdated={lastUpdated}
            onClick={() => navigate(`/promotion-strategies/${namespace}/${ps.metadata?.name || ''}`)}
          />
        );
      })}
    </div>
  );
};

export default PromotionStrategiesTiles; 