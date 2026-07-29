import React from 'react';
import { GoGitPullRequest } from 'react-icons/go';
import { Tooltip } from './Tooltip';
import { PrTooltip } from '@shared/types/promotion';
import { formatDate, timeAgo } from '@shared/utils/util';

export interface PrIndicatorProps {
  prUrl: string;
  prNumber: string;
  prTooltip?: PrTooltip | null;
  variant: 'active' | 'proposed';
}

const PrIndicator: React.FC<PrIndicatorProps> = ({ prUrl, prNumber, prTooltip, variant }) => {
  const className =
    variant === 'active'
      ? `pr-indicator ${prTooltip?.label === 'merged' ? 'pr-merged' : ''}`
      : 'proposed-changes-card__pr';

  return (
    <Tooltip
      content={
        prTooltip
          ? `${prTooltip.label} ${formatDate(prTooltip.time)}`
          : `Open PR #${prNumber} on GitHub`
      }
    >
      <a href={prUrl} target="_blank" rel="noopener noreferrer" className={className}>
        <GoGitPullRequest className="pr-icon" />#{prNumber}
        {prTooltip && <span className="pr-merge-time">{timeAgo(prTooltip.time)}</span>}
      </a>
    </Tooltip>
  );
};

export default PrIndicator;
