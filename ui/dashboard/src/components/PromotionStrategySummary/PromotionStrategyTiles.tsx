import React from 'react';
import type { PromotionStrategy } from '@shared/utils/PSData';
import { useNavigateWithParams } from '../../hooks/useNavigateWithParams';
import { PromotionStrategyTile } from '../PromotionStrategySummary/PromotionStrategyTile';
import { getLastCommitTime, formatDate, getOverallPromotionStatus } from '@shared/utils/util';
import { getEnvironmentStatus } from '@shared/utils/getStatus';
import './PromotionStrategyTiles.scss';

export interface PromotionStrategyTilesProps {
  promotionStrategies: PromotionStrategy[];
  namespace: string;
}

const PromotionStrategiesTiles: React.FC<PromotionStrategyTilesProps> = ({
  promotionStrategies,
  namespace,
}) => {
  const navigate = useNavigateWithParams();

  return (
    <div className="applications-tiles">
      {promotionStrategies.map((ps) => {
        const lastCommitTime = getLastCommitTime(ps);
        const lastUpdated = lastCommitTime ? formatDate(lastCommitTime.toISOString()) : '-';

        const environmentStatuses = (ps.status?.environments ?? []).map(getEnvironmentStatus);
        const borderStatus = getOverallPromotionStatus(environmentStatuses);

        return (
          <PromotionStrategyTile
            key={ps.metadata.name}
            ps={ps}
            namespace={namespace}
            borderStatus={borderStatus}
            lastUpdated={lastUpdated}
            onClick={() => navigate(`/promotion-strategies/${namespace}/${ps.metadata.name}`)}
          />
        );
      })}
    </div>
  );
};

export default PromotionStrategiesTiles;
