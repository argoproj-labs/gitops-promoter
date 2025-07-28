import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState } from 'react';

export interface HealthSummaryProps {
  checks: any[];
  title: string;
  status?: StatusType;
  healthSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean }; // Pre-calculated summary
}

const HealthSummary: React.FC<HealthSummaryProps> = ({ checks, title, status, healthSummary }) => {
  const { successCount, totalCount, shouldDisplay } = healthSummary || {
    successCount: checks.filter(check => check.status === 'success').length,
    totalCount: checks.length,
    shouldDisplay: checks && checks.length > 0
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
          {checks.map((check, index) => (
            <div key={index} className="health-check-item">
              <StatusIcon phase={check.status as StatusType} type="status" />
              <span className="health-check-name">{check.name}</span>
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
          ))}
        </div>
      )}
    </div>
  );
};

export default HealthSummary; 