import React, { useState, useMemo, useCallback } from 'react';
import { FiChevronUp, FiChevronDown } from 'react-icons/fi';
import { StatusIcon, StatusType } from './StatusIcon';
import './HealthSummary.scss';

export interface Check {
  name: string;
  status: StatusType;
  url?: string;
  details?: string;
}

export interface HealthSummaryData {
  successCount: number;
  totalCount: number;
  shouldDisplay: boolean;
}

export interface HealthSummaryProps {
  checks: Check[];
  title: string;
  status?: StatusType;
  healthSummary?: HealthSummaryData;
}

const HealthSummary: React.FC<HealthSummaryProps> = ({
  checks,
  title,
  status = 'unknown',
  healthSummary
}) => {
  // Calculate health summary if not provided
  const calculatedSummary = useMemo((): HealthSummaryData => {
    if (healthSummary) return healthSummary;

    return {
      successCount: checks.filter(check => check.status === 'success').length,
      totalCount: checks.length,
      shouldDisplay: checks && checks.length > 0
    };
  }, [checks, healthSummary]);

  const { successCount, totalCount, shouldDisplay } = calculatedSummary;

  // Auto-expand if less than 3 checks
  const shouldAutoExpand = totalCount < 3;
  const [isExpanded, setIsExpanded] = useState(shouldAutoExpand);

  const handleToggleExpanded = useCallback(() => {
    setIsExpanded(prev => !prev);
  }, []);

  const handleKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      handleToggleExpanded();
    }
  }, [handleToggleExpanded]);

  if (!shouldDisplay) {
    return null;
  }

  const ChevronIcon = isExpanded ? FiChevronUp : FiChevronDown;

  return (
    <div className="health-summary">
      <div
        className="health-header"
        onClick={handleToggleExpanded}
        onKeyDown={handleKeyDown}
        role="button"
        tabIndex={0}
        aria-expanded={isExpanded}
        aria-controls="health-details"
        aria-label={`${title}: ${successCount} of ${totalCount} checks passed. Click to ${isExpanded ? 'collapse' : 'expand'} details.`}
      >
        <StatusIcon phase={status} type="health" />
        <span className="health-count">
          {successCount}/{totalCount} Checks
        </span>
        <span className="health-toggle">
          <ChevronIcon aria-hidden="true" />
        </span>
      </div>

      {isExpanded && (
        <div
          id="health-details"
          className="health-details"
          role="list"
          aria-label="Health check details"
        >
          {checks.map((check, index) => (
            <div
              key={`${check.name}-${index}`}
              className="health-check-item"
              role="listitem"
            >
              <StatusIcon phase={check.status} type="status" />
              <span className="health-check-name">{check.name}</span>

              {check.url && (
                <a
                  href={check.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="health-check-link"
                  title={`View details for ${check.name}`}
                  aria-label={`View details for ${check.name} check`}
                >
                  View Details
                </a>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default HealthSummary;
