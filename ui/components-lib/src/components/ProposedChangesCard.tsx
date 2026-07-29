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
      {/* "Pushing to Active" promotion indicator: a banner across the card top in
          the row layout, restyled by the stacked-layout mixin into a pulsing
          chevron badge on the active/proposed seam. */}
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

      {/* Reuse CommitInfo to render the commit rows rather than duplicating the
          commit JSX. */}
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
