import React from 'react';
import { StatusIcon, StatusType } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HealthSummary from './HealthSummary';
import PrIndicator from './PrIndicator';
import {
  Check,
  DeploymentCommit,
  HealthSummaryResult,
  PrTooltip,
  ReferenceCommit,
} from '@shared/types/promotion';
import './ProposedChangesCard.scss';

export interface ProposedChangesCardProps {
  deploymentCommit: DeploymentCommit;
  codeCommit: ReferenceCommit | null;
  status?: StatusType;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  checks?: Check[];
  healthSummary?: HealthSummaryResult;
  prUrl: string | null;
  prNumber?: string;
  prTooltip?: PrTooltip | null;
}

function progressMessage(
  healthSummary?: Pick<HealthSummaryResult, 'successCount' | 'totalCount'>,
): string | null {
  if (!healthSummary || healthSummary.totalCount === 0) {
    return null;
  }
  const { successCount, totalCount } = healthSummary;
  const inProgress = totalCount - successCount;
  if (inProgress <= 0) {
    return `${totalCount} of ${totalCount} checks passed`;
  }
  return `${inProgress} of ${totalCount} checks in progress`;
}

const ProposedChangesCard: React.FC<ProposedChangesCardProps> = ({
  deploymentCommit,
  codeCommit,
  status = 'unknown',
  deploymentCommitUrl,
  codeCommitUrl,
  checks,
  healthSummary,
  prUrl,
  prNumber,
  prTooltip,
}) => {
  const message = progressMessage(healthSummary);

  return (
    <div className="proposed-changes-card">
      <div className="promote-flow" aria-hidden="true" />
      <div className="promote-banner">
        <svg className="promote-banner__icon" viewBox="0 0 16 7" fill="none" aria-hidden="true">
          <path
            d="M1 6 L8 1 L15 6"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className="promote-banner__label">Pushing to Active</span>
      </div>

      <div className="proposed-changes-card__header">
        <div className="proposed-changes-card__header-row">
          <span className="proposed-changes-card__status-icon">
            <StatusIcon phase={status} type="status" />
          </span>
          <h4 className="proposed-changes-card__title">Proposed changes</h4>
          {prUrl && prNumber && (
            <PrIndicator
              prUrl={prUrl}
              prNumber={prNumber}
              prTooltip={prTooltip}
              variant="proposed"
            />
          )}
        </div>
        {message && <div className="proposed-changes-card__progress">{message}</div>}
      </div>

      <CommitInfo
        deploymentCommit={deploymentCommit}
        codeCommit={codeCommit}
        deploymentCommitUrl={deploymentCommitUrl}
        codeCommitUrl={codeCommitUrl}
      />

      {healthSummary?.shouldDisplay && checks && (
        <HealthSummary
          checks={checks}
          title="Proposed Checks"
          status={status}
          healthSummary={healthSummary}
          variant="always-expanded"
        />
      )}
    </div>
  );
};

export default ProposedChangesCard;
