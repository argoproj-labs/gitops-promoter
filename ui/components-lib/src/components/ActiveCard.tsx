import React from 'react';
import { GoGitPullRequest } from 'react-icons/go';
import { StatusIcon, StatusType, statusLabel } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HealthSummary from './HealthSummary';
import { Tooltip } from './Tooltip';
import { PrTooltip, ReferenceCommit } from '@shared/types/promotion';
import { formatDate, timeAgo } from '@shared/utils/util';
import './ActiveCard.scss';

export interface ActiveCardProps {
  branch: string;
  activeStatus?: StatusType;
  deploymentCommit: any;
  codeCommit: ReferenceCommit | null;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  prUrl: string | null;
  prNumber?: string;
  prTooltip?: PrTooltip | null;
  checks: any[];
  healthSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
}

// The active/live side of an environment card. In the row layout it renders as a
// detached card (chrome applied via the layout mixin); in the stacked layout it
// unwraps (`display: contents`) so its children participate directly in the
// `.env-card` grid.
const ActiveCard: React.FC<ActiveCardProps> = ({
  branch,
  activeStatus,
  deploymentCommit,
  codeCommit,
  deploymentCommitUrl,
  codeCommitUrl,
  prUrl,
  prNumber,
  prTooltip,
  checks,
  healthSummary,
}) => {
  return (
    <div className="active-card">
      <div
        className="env-card__title"
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          position: 'relative',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'flex-start', minWidth: 0 }}>
          <div style={{ minWidth: 0 }}>
            <span className="env-card__env-name">{branch}</span>
          </div>
          <span className="env-card__health-chip">
            <StatusIcon phase={activeStatus as StatusType} type="health" />
            {statusLabel(activeStatus as StatusType)}
          </span>
        </div>
        {prUrl && prNumber && (
          <Tooltip
            content={
              prTooltip
                ? `${prTooltip.label} ${formatDate(prTooltip.time)}`
                : `Open PR #${prNumber} on GitHub`
            }
          >
            <a
              href={prUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={`pr-indicator ${prTooltip?.label === 'merged' ? 'pr-merged' : ''}`}
            >
              <GoGitPullRequest className="pr-icon" />
              #{prNumber}
              {prTooltip && <span className="pr-merge-time">{timeAgo(prTooltip.time)}</span>}
            </a>
          </Tooltip>
        )}
      </div>

      <CommitInfo
        deploymentCommit={deploymentCommit}
        codeCommit={codeCommit}
        deploymentCommitUrl={deploymentCommitUrl}
        codeCommitUrl={codeCommitUrl}
      />

      <div className="env-card__current-status">
        <HealthSummary
          checks={checks}
          title="Current status"
          healthSummary={healthSummary}
          variant="collapsible"
          headerLabel="Current status"
        />
      </div>
    </div>
  );
};

export default ActiveCard;
