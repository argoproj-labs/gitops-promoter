import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState, useCallback, useRef } from 'react';
import './HealthSummary.scss';

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
  const [hoveredCheckIndex, setHoveredCheckIndex] = useState<number | null>(null);
  const [tooltipPosition, setTooltipPosition] = useState({ top: 0, left: 0 });
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  
  if (!shouldDisplay) {
    return null;
  }

  const handleClick = () => {
    setIsExpanded(!isExpanded);
  };

  const handleMouseEnter = useCallback((index: number, event: React.MouseEvent<HTMLSpanElement | HTMLDivElement>) => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
    }
    
    // Only update position if hovering over the check item (not the tooltip)
    if ((event.currentTarget as HTMLElement).classList.contains('health-check-item')) {
      const rect = event.currentTarget.getBoundingClientRect();
      setTooltipPosition({
        top: rect.bottom + 4,
        left: rect.left
      });
    }
    
    setHoveredCheckIndex(index);
  }, []);

  const handleMouseLeave = useCallback(() => {
    timeoutRef.current = setTimeout(() => {
      setHoveredCheckIndex(null);
    }, 100);
  }, []);

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
            <div 
              key={index} 
              className="health-check-item"
              onMouseEnter={(e) => handleMouseEnter(index, e)}
              onMouseLeave={handleMouseLeave}
            >
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
          ))}
          {checks.map((check, index) => 
            check.description && hoveredCheckIndex === index && (
              <div
                key={`tooltip-${index}`}
                className="health-tooltip-container"
                style={{ 
                  top: `${tooltipPosition.top}px`, 
                  left: `${tooltipPosition.left}px` 
                }}
                onMouseEnter={(e) => handleMouseEnter(index, e)}
                onMouseLeave={handleMouseLeave}
              >
                <div className="health-tooltip">
                  {check.description}
                </div>
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
};

export default HealthSummary; 