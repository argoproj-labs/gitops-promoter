import { FiChevronDown, FiChevronUp, FiInfo } from 'react-icons/fi';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState } from 'react';
import { Tooltip } from './Tooltip';
import { Check, HealthSummaryResult } from '@shared/types/promotion';
import './HealthSummary.scss';

export interface HealthSummaryProps {
  checks: Check[];
  healthSummary?: HealthSummaryResult;
  additionalChecks?: Check[];
  additionalChecksTitle?: string;
  additionalChecksTitleTooltip?: string;
  primaryChecksTitle?: string;
  primaryChecksTitleTooltip?: string;
  variant?: 'collapsible' | 'always-expanded';
  headerLabel?: string;
}

const HealthSummary: React.FC<HealthSummaryProps> = ({
  checks,
  healthSummary,
  additionalChecks,
  additionalChecksTitle,
  additionalChecksTitleTooltip,
  primaryChecksTitle,
  primaryChecksTitleTooltip,
  variant = 'collapsible',
  headerLabel = 'Current status',
}) => {
  const allChecks = additionalChecks ? [...checks, ...additionalChecks] : checks;
  const { totalCount, shouldDisplay } = healthSummary
    ? additionalChecks
      ? {
          totalCount: healthSummary.totalCount + additionalChecks.length,
          shouldDisplay: healthSummary.shouldDisplay || additionalChecks.length > 0,
        }
      : healthSummary
    : {
        totalCount: allChecks.length,
        shouldDisplay: allChecks && allChecks.length > 0,
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

  return (
    <div className="health-summary">
      {isAlwaysExpanded ? (
        <div className="health-header health-header--static">
          <span className="health-count">Checks</span>
        </div>
      ) : (
        <div className="health-header" onClick={handleClick}>
          <span className="health-count">{headerLabel}</span>
          <span className="health-toggle">{isExpanded ? <FiChevronUp /> : <FiChevronDown />}</span>
        </div>
      )}

      {showDetails && (
        <div className="health-details">
          {primaryChecksTitle && (
            <div className="health-subheading">
              {primaryChecksTitle}
              {primaryChecksTitleTooltip && (
                <Tooltip content={primaryChecksTitleTooltip}>
                  <button
                    type="button"
                    className="health-subheading-info"
                    aria-label="More information"
                  >
                    <FiInfo />
                  </button>
                </Tooltip>
              )}
            </div>
          )}
          {checks.map((check, index) => (
            <Tooltip key={index} content={check.description}>
              <div className="health-check-item">
                <StatusIcon phase={check.status as StatusType} type="status" />
                <div className="health-check-body">
                  {check.url ? (
                    <a
                      href={check.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="health-check-name-link"
                    >
                      {check.name}
                    </a>
                  ) : (
                    <span className="check-name-text">{check.name}</span>
                  )}
                </div>
              </div>
            </Tooltip>
          ))}
          {additionalChecks && additionalChecks.length > 0 && (
            <>
              <div className="health-subheading">
                {additionalChecksTitle || 'Additional Checks'}
                {additionalChecksTitleTooltip && (
                  <Tooltip content={additionalChecksTitleTooltip}>
                    <button
                      type="button"
                      className="health-subheading-info"
                      aria-label="More information"
                    >
                      <FiInfo />
                    </button>
                  </Tooltip>
                )}
              </div>
              {additionalChecks.map((check, index) => (
                <Tooltip key={`additional-${index}`} content={check.description}>
                  <div className="health-check-item">
                    <StatusIcon phase={check.status as StatusType} type="status" />
                    <div className="health-check-body">
                      {check.url ? (
                        <a
                          href={check.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="health-check-name-link"
                        >
                          {check.name}
                        </a>
                      ) : (
                        <span className="check-name-text">{check.name}</span>
                      )}
                    </div>
                  </div>
                </Tooltip>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
};

export default HealthSummary;
