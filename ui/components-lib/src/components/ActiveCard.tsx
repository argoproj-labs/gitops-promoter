import React from 'react';
import { StatusIcon, StatusType, statusLabel } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HealthSummary from './HealthSummary';
import PrIndicator from './PrIndicator';
import { Tooltip } from './Tooltip';
import {
  Check,
  DeploymentCommit,
  HealthSummaryResult,
  PrTooltip,
  ReferenceCommit,
} from '@shared/types/promotion';
import './ActiveCard.scss';

export interface ActiveCardProps {
  branch: string;
  activeStatus: StatusType;
  deploymentCommit: DeploymentCommit;
  codeCommit: ReferenceCommit | null;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  prUrl: string | null;
  prNumber?: string;
  prTooltip?: PrTooltip | null;
  checks: Check[];
  healthSummary?: HealthSummaryResult;
}

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
            <Tooltip content={branch}>
              <span className="env-card__env-name">{branch}</span>
            </Tooltip>
          </div>
          <span className="env-card__health-chip">
            <StatusIcon phase={activeStatus} type="health" />
            {statusLabel(activeStatus)}
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
          healthSummary={healthSummary}
          variant="collapsible"
          headerLabel="Current status"
        />
      </div>
    </div>
  );
};

export default ActiveCard;
