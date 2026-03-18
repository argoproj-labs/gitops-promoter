import { FiChevronDown, FiChevronUp, FiInfo } from 'react-icons/fi';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState } from 'react';
import { Tooltip } from './Tooltip';
import './HealthSummary.scss';

export interface HealthSummaryProps {
  checks: any[];
  title: string;
  status?: StatusType;
  healthSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
  additionalChecks?: any[];
  additionalChecksTitle?: string;
  additionalChecksTitleTooltip?: string;
  primaryChecksTitle?: string;
  primaryChecksTitleTooltip?: string;
}

const HealthSummary: React.FC<HealthSummaryProps> = ({ checks, title, status, healthSummary, additionalChecks, additionalChecksTitle, additionalChecksTitleTooltip, primaryChecksTitle, primaryChecksTitleTooltip }) => {
  const allChecks = additionalChecks ? [...checks, ...additionalChecks] : checks;
  const { successCount, totalCount, shouldDisplay } = healthSummary
    ? (additionalChecks
      ? {
          successCount: healthSummary.successCount + additionalChecks.filter(c => c.status === 'success').length,
          totalCount: healthSummary.totalCount + additionalChecks.length,
          shouldDisplay: healthSummary.shouldDisplay || additionalChecks.length > 0,
        }
      : healthSummary)
    : {
        successCount: allChecks.filter(check => check.status === 'success').length,
        totalCount: allChecks.length,
        shouldDisplay: allChecks && allChecks.length > 0,
      };

  // Auto-expand if less than 3 checks
  const shouldAutoExpand = totalCount < 3;
  const [isExpanded, setIsExpanded] = useState(shouldAutoExpand);
  
  if (!shouldDisplay) {
    return null;
  }

  const handleClick = () => {
    setIsExpanded(!isExpanded);
  };

  return (
    <div className="health-summary">
      <div className="health-header" onClick={handleClick}>
        <StatusIcon phase={status || 'unknown'} type="health" />
        <span className="health-count">{successCount}/{totalCount} Checks</span>
        <span className="health-toggle">
          {isExpanded ? <FiChevronUp /> : <FiChevronDown />}
        </span>
      </div>
      
      {isExpanded && (
        <div className="health-details">
          {primaryChecksTitle && (
            <div className="health-subheading">
              {primaryChecksTitle}
              {primaryChecksTitleTooltip && (
                <Tooltip content={primaryChecksTitleTooltip}>
                  <span className="health-subheading-info"><FiInfo /></span>
                </Tooltip>
              )}
            </div>
          )}
          {checks.map((check, index) => (
            <Tooltip key={index} content={check.description}>
              <div className="health-check-item">
                <StatusIcon phase={check.status as StatusType} type="status" />
                <span className="health-check-name">
                  <span className="check-name-text">{check.name}</span>
                  {check.description && (
                    <span className="check-description-preview">&nbsp;—&nbsp;{check.description}</span>
                  )}
                </span>
                {check.url && (
                  <a
                    href={check.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="health-check-link"
                    title="View details"
                  >
                    View Details
                  </a>
                )}
              </div>
            </Tooltip>
          ))}
          {additionalChecks && additionalChecks.length > 0 && (
            <>
              <div className="health-subheading">
                {additionalChecksTitle || 'Additional Checks'}
                {additionalChecksTitleTooltip && (
                  <Tooltip content={additionalChecksTitleTooltip}>
                    <span className="health-subheading-info"><FiInfo /></span>
                  </Tooltip>
                )}
              </div>
              {additionalChecks.map((check, index) => (
                <Tooltip key={`additional-${index}`} content={check.description}>
                  <div className="health-check-item">
                    <StatusIcon phase={check.status as StatusType} type="status" />
                    <span className="health-check-name">
                      <span className="check-name-text">{check.name}</span>
                      {check.description && (
                        <span className="check-description-preview">&nbsp;—&nbsp;{check.description}</span>
                      )}
                    </span>
                    {check.url && (
                      <a
                        href={check.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="health-check-link"
                        title="View details"
                      >
                        View Details
                      </a>
                    )}
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