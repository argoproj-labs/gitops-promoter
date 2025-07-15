import { FaGitAlt } from 'react-icons/fa';
import { StatusIcon } from '@lib/components/StatusIcon';
import type { StatusType } from '@lib/components/StatusIcon';
import './PromotionStrategyTiles.scss';

export const PromotionStrategyTile = ({ps, borderStatus, promotedPhase, lastUpdated, enrichedEnvList, onClick}:{
  ps: any, namespace: string,
  borderStatus: 'success' | 'failure' | 'pending' | 'default',
  promotedPhase: 'success' | 'failure' | 'pending' | 'default',
  lastUpdated: string,
  enrichedEnvList?: any[],
  onClick: () => void
}) => (
  <div
    className={`ps-tile ps-tile--${borderStatus}`}
    style={{ cursor: 'pointer' }}
    onClick={onClick}
  >
    <div className="ps-tile__header">
      <FaGitAlt className="ps-tile__icon" />
      <span className="ps-tile__name">{ps.metadata.name}</span>
    </div>
    <div className="ps-tile__row"><span className="ps-tile__label">Repository:</span> <span className="ps-tile__info">{ps.spec.gitRepositoryRef.name}</span></div>
    <div className="ps-tile__row"><span className="ps-tile__label">Promoted:</span> <span className="ps-tile__info"><StatusIcon phase={enrichedEnvList?.[0]?.promotionStatus || (promotedPhase === 'default' ? 'unknown' : promotedPhase)} type="status" /></span></div>
    <div className="ps-tile__row"><span className="ps-tile__label">Last Updated:</span> <span className="ps-tile__info">{enrichedEnvList?.[0]?.lastSync || lastUpdated}</span></div>
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