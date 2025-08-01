import { FaGitAlt } from 'react-icons/fa';
import { StatusIcon } from '@lib/components/StatusIcon';
import type { StatusType } from '@lib/components/StatusIcon';
import { getLastCommitTime } from './promotionStrategyUtils';
import { formatDate } from '@shared/utils/util';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './PromotionStrategyTiles.scss';

interface EnrichedEnvironment {
  branch: string;
  autoMerge?: boolean;
  promotionStatus?: string;
}

export const PromotionStrategyTile = ({ps, borderStatus, promotedPhase, lastUpdated, enrichedEnvList, onClick}:{
  ps: PromotionStrategy, namespace: string,
  borderStatus: 'success' | 'failure' | 'pending' | 'default',
  promotedPhase: 'success' | 'failure' | 'pending' | 'default',
  lastUpdated: string,
  enrichedEnvList?: EnrichedEnvironment[],
  onClick: () => void
}) => {
  const lastCommitTime = getLastCommitTime(ps);
  const actualLastUpdated = lastCommitTime ? formatDate(lastCommitTime.toISOString()) : lastUpdated;
  
  return (
    <div
      className={`ps-tile ps-tile--${borderStatus}`}
      style={{ cursor: 'pointer' }}
      onClick={onClick}
    >
      <div className="ps-tile__header">
        <FaGitAlt className="ps-tile__icon" />
        <span className="ps-tile__name">{ps.metadata?.name || 'Unknown'}</span>
      </div>

      
      <div className="ps-tile__row"><span className="ps-tile__label">Repository:</span> <span className="ps-tile__info">{ps.spec.gitRepositoryRef.name}</span></div>
      <div className="ps-tile__row"><span className="ps-tile__label">Promoted:</span> <span className="ps-tile__info"><StatusIcon phase={enrichedEnvList?.[0]?.promotionStatus as StatusType || (promotedPhase === 'default' ? 'unknown' : promotedPhase)} type="status" /></span></div>
      <div className="ps-tile__row"><span className="ps-tile__label">Last Updated:</span> <span className="ps-tile__info">{actualLastUpdated}</span></div>
      <div className="ps-tile__row"><span className="ps-tile__label">Environments:</span></div>
      <div className="ps-tile__envs">



        {enrichedEnvList && enrichedEnvList.length > 0 && enrichedEnvList.map((env, idx) => (
          <div key={env.branch || idx} className="ps-tile__env-row-grid">
            <span className="ps-tile__env-branch">
              {env.branch}
              <span className="ps-tile__env-automerge" style={{ marginLeft: 8 }}>
                (autoMerge: {env.autoMerge ? 'true' : 'false'})
              </span>
            </span>
            <span className="ps-tile__env-status">
              <StatusIcon phase={env.promotionStatus as StatusType} type="status" />
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}; 