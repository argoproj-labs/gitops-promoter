import React from 'react';
import { useNavigate } from 'react-router-dom';
import type { PromotionStrategy } from '@shared/utils/PSData';
import { PromotionStrategyTile } from '../PromotionStrategySummary/PromotionStrategyTile';
import { getLastCommitTime, formatDate, getOverallPromotionStatus } from '@shared/utils/util';
import { enrichFromCRD } from '@shared/utils/PSData';
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
        
        const enrichedEnvs = enrichFromCRD(ps);
        const environmentStatuses = enrichedEnvs.map(env => env.promotionStatus || 'unknown');
        const borderStatus = getOverallPromotionStatus(environmentStatuses);
        
        return (
          <PromotionStrategyTile
            key={ps.metadata?.name || `ps-${idx}`}
            ps={ps}
            namespace={namespace}
            borderStatus={borderStatus}
            lastUpdated={lastUpdated}
            onClick={() => navigate(`/promotion-strategies/${namespace}/${ps.metadata?.name || ''}`)}
          />
        );
      })}
    </div>
  );
};

export default PromotionStrategiesTiles; 