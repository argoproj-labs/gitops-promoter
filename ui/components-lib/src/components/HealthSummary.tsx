import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import React, { useState } from 'react';
import { Check, HealthSummaryResult } from '@shared/types/promotion';
import { HealthCheckItem } from './HealthCheckItem';
import './HealthSummary.scss';

export interface HealthSummaryProps {
  checks: Check[];
  healthSummary?: HealthSummaryResult;
  variant?: 'collapsible' | 'always-expanded';
  headerLabel?: string;
}

const HealthSummary: React.FC<HealthSummaryProps> = ({
  checks,
  healthSummary,
  variant = 'collapsible',
  headerLabel = 'Current status',
}) => {
  const { totalCount, shouldDisplay } = healthSummary
    ? healthSummary
    : {
        totalCount: checks.length,
        shouldDisplay: checks.length > 0,
      };

  const isAlwaysExpanded = variant === 'always-expanded';

  // Auto-expand if less than 3 checks
  const shouldAutoExpand = totalCount < 3;
  const [isExpanded, setIsExpanded] = useState(shouldAutoExpand);

  if (!shouldDisplay) {
    return null;
  }

  const handleClick = () => {
    setIsExpanded(!isExpanded);
  };

  const showDetails = isAlwaysExpanded || isExpanded;

  const handleHeaderKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter') {
      handleClick();
    } else if (event.key === ' ') {
      event.preventDefault();
      handleClick();
    }
  };

  return (
    <div className="health-summary">
      {isAlwaysExpanded ? (
        <div className="health-header health-header--static">
          <span className="health-count">{headerLabel}</span>
        </div>
      ) : (
        <div
          className="health-header"
          onClick={handleClick}
          role="button"
          tabIndex={0}
          aria-expanded={isExpanded}
          onKeyDown={handleHeaderKeyDown}
        >
          <span className="health-count">{headerLabel}</span>
          <span className="health-toggle">{isExpanded ? <FiChevronUp /> : <FiChevronDown />}</span>
        </div>
      )}

      {showDetails && (
        <div className="health-details">
          {checks.map((check, index) => (
            <HealthCheckItem key={index} check={check} />
          ))}
        </div>
      )}
    </div>
  );
};

export default HealthSummary;
