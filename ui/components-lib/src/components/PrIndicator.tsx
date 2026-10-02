import React from 'react';
import { GoGitPullRequest } from 'react-icons/go';
import { Tooltip } from './Tooltip';
import { PrTooltip } from '@shared/types/promotion';
import { formatDate, timeAgo } from '@shared/utils/util';
import './PrIndicator.scss';

export interface PrIndicatorProps {
  prUrl: string;
  prNumber: string;
  prTooltip?: PrTooltip | null;
  variant: 'active' | 'proposed';
}

const PrIndicator: React.FC<PrIndicatorProps> = ({ prUrl, prNumber, prTooltip, variant }) => {
  const terminalClass =
    prTooltip?.status === 'merged'
      ? 'pr-merged'
      : prTooltip?.status === 'closed'
        ? 'pr-closed'
        : '';
  const className =
    variant === 'active' ? `pr-indicator ${terminalClass}` : 'proposed-changes-card__pr';

  return (
    <Tooltip
      content={
        prTooltip
          ? prTooltip.time
            ? `${prTooltip.label} ${formatDate(prTooltip.time)}`
            : prTooltip.label
          : `Open PR #${prNumber}`
      }
    >
      <a href={prUrl} target="_blank" rel="noopener noreferrer" className={className}>
        <GoGitPullRequest className="pr-icon" />#{prNumber}
        {prTooltip?.time && <span className="pr-merge-time">{timeAgo(prTooltip.time)}</span>}
      </a>
    </Tooltip>
  );
};

export default PrIndicator;
