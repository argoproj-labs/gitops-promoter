import { FaGitAlt } from 'react-icons/fa';
import { StatusIcon } from '@lib/components/StatusIcon';
import type { StatusType } from '@lib/components/StatusIcon';
import { getLastCommitTime, formatDate } from '@shared/utils/util';
import { enrichPromotionStrategy, getPromotionStatus } from '@shared/utils/PSData';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './PromotionStrategyTiles.scss';

export const PromotionStrategyTile = ({ps, borderStatus, lastUpdated, onClick}:{
  ps: PromotionStrategy, namespace: string,
  borderStatus: 'success' | 'failure' | 'pending' | 'default',
  lastUpdated: string,
  onClick: () => void
}) => {
  const lastCommitTime = getLastCommitTime(ps);
  const actualLastUpdated = lastCommitTime ? formatDate(lastCommitTime.toISOString()) : lastUpdated;
  

  
  const enrichedEnvs = enrichPromotionStrategy(ps);
  const { overallStatus } = getPromotionStatus(ps);
  const statusIconPhase = overallStatus === 'default' ? 'unknown' : overallStatus;
  
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
      <div className="ps-tile__row"><span className="ps-tile__label">Promoted:</span> <span className="ps-tile__info"><StatusIcon phase={statusIconPhase} type="status" /></span></div>
      <div className="ps-tile__row"><span className="ps-tile__label">Last Updated:</span> <span className="ps-tile__info">{actualLastUpdated}</span></div>
      <div className="ps-tile__row"><span className="ps-tile__label">Environments:</span></div>
      <div className="ps-tile__envs">



        {enrichedEnvs.length > 0 && enrichedEnvs.map((env, idx) => (
          <div key={env.branch || idx} className="ps-tile__env-row-grid">
            <span className="ps-tile__env-branch">
              {env.branch}
              <span className="ps-tile__env-automerge" style={{ marginLeft: 8 }}>
                (autoMerge: {ps.spec.environments?.[idx]?.autoMerge ? 'true' : 'false'})
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