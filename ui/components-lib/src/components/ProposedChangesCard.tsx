import React from 'react';
import { GoGitPullRequest } from 'react-icons/go';
import { StatusIcon, StatusType } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HealthSummary from './HealthSummary';
import { Tooltip } from './Tooltip';
import { PrTooltip, ReferenceCommit } from '@shared/types/promotion';
import { formatDate, timeAgo } from '@shared/utils/util';
import './ProposedChangesCard.scss';

export interface ProposedChangesCardProps {
  deploymentCommit: any;
  codeCommit: ReferenceCommit | null;
  status?: StatusType;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  checks?: any[];
  healthSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
  prUrl: string | null;
  prNumber?: string;
  prTooltip?: PrTooltip | null;
}

// Build a faithful, simple progress message from the proposed checks summary.
// Anything that is not yet a success is treated as "in progress"; when every
// check has passed we report completion instead.
function progressMessage(healthSummary?: {
  successCount: number;
  totalCount: number;
}): string | null {
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

// The proposed-changes card. Emitted in a single DOM slot as a child of the
// environment card (after the active commit group). Row-vs-stacked presentation
// is chosen entirely in CSS by the layout mixins keyed off `.proposed-changes-card`.
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
      <div className="proposed-changes-card__header">
        <div className="proposed-changes-card__header-row">
          <span className="proposed-changes-card__status-icon">
            <StatusIcon phase={status} type="status" />
          </span>
          <h4 className="proposed-changes-card__title">Proposed changes</h4>
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
                className="proposed-changes-card__pr"
              >
                <GoGitPullRequest className="pr-icon" />
                #{prNumber}
                {prTooltip && <span className="pr-merge-time">{timeAgo(prTooltip.time)}</span>}
              </a>
            </Tooltip>
          )}
        </div>
        {message && <div className="proposed-changes-card__progress">{message}</div>}
      </div>

      {/* Reuse CommitInfo's commit rendering (title-less path renders just the
          commits section) rather than duplicating the commit JSX. */}
      <CommitInfo
        deploymentCommit={deploymentCommit}
        codeCommit={codeCommit}
        deploymentCommitUrl={deploymentCommitUrl}
        codeCommitUrl={codeCommitUrl}
        prUrl={prUrl}
        status={status}
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
