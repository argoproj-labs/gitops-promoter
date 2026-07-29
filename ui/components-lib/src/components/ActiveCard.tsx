import React from 'react';
import { StatusIcon, StatusType, statusLabel } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HealthSummary from './HealthSummary';
import PrIndicator from './PrIndicator';
import { Check, DeploymentCommit, PrTooltip, ReferenceCommit } from '@shared/types/promotion';
import './ActiveCard.scss';

export interface ActiveCardProps {
  branch: string;
  activeStatus?: StatusType;
  deploymentCommit: DeploymentCommit;
  codeCommit: ReferenceCommit | null;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  prUrl: string | null;
  prNumber?: string;
  prTooltip?: PrTooltip | null;
  checks: Check[];
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
      <div className="env-card__title">
        <div className="env-card__title-main">
          <div className="env-card__env-name-wrap">
            <span className="env-card__env-name">{branch}</span>
          </div>
          <span className="env-card__health-chip">
            <StatusIcon phase={activeStatus as StatusType} type="health" />
            {statusLabel(activeStatus as StatusType)}
          </span>
        </div>
        {prUrl && prNumber && (
          <PrIndicator prUrl={prUrl} prNumber={prNumber} prTooltip={prTooltip} variant="active" />
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
