import { GoArchive } from "react-icons/go";
import { BsBraces } from 'react-icons/bs';
import { GoGitPullRequest } from 'react-icons/go';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState, useRef, useCallback } from 'react';
import TimeAgo from './TimeAgo';
import HealthSummary from './HealthSummary';
import './CommitInfo.scss';
import {ReferenceCommit} from "@shared/types/promotion";

export interface CommitInfoProps {
  title?: string;
  deploymentCommit: any;
  codeCommit: ReferenceCommit | null;
  isActive?: boolean;
  status?: StatusType;
  className?: string;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
  checks?: any[];
  healthSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
  prUrl: string | null;
  prNumber?: string;
}

// Combined component to display commit information and groups
const CommitInfo: React.FC<CommitInfoProps> = ({ 
  title,
  deploymentCommit, 
  codeCommit, 
  isActive = false, 
  status = 'unknown', 
  className = '', 
  deploymentCommitUrl, 
  codeCommitUrl, 
  checks,
  healthSummary,
  prUrl, 
  prNumber
}) => {
  const [showDeploymentTooltip, setShowDeploymentTooltip] = useState(false);
  const [showCodeTooltip, setShowCodeTooltip] = useState(false);
  const deploymentTimeoutRef = useRef<number | null>(null);
  const codeTimeoutRef = useRef<number | null>(null);

  const getIcon = (iconType: 'file' | 'code') => {
    if (iconType === 'code') return <BsBraces className="commit-icon" />;
    return <GoArchive className="commit-icon" />;
  };

  const getStatusClass = (type: 'deployment' | 'code') => {
    if (type === 'deployment') return 'commit-deployment';
    return 'commit-code';
  };

  const renderSha = (commit: any, commitUrl?: string) => {
    const sha = commit.sha?.substring(0, 8) || 'N/A';
    if (commitUrl && commit.sha) {
      return (
        <a 
          href={commitUrl} 
          target="_blank" 
          rel="noopener noreferrer"
          className="commit-sha-link"
          title={`View commit ${sha}`}
        >
          {sha}
        </a>
      );
    }
    return <span className="commit-sha">{sha}</span>;
  };

  // Create tooltip on subject and body
  const getTooltipContent = (commit: any) => {
    const subject = commit.subject || '';
    const body = commit.body || '';
    
    if (subject && body) {
      return (
        <div className="github-tooltip">
          <div className="tooltip-subject">{subject}</div>
          <div className="tooltip-body">{body}</div>
        </div>
      );
    }
    
    if (body) {
      return <div className="github-tooltip">{body}</div>;
    }
    
    if (subject) {
      return <div className="github-tooltip">{subject}</div>;
    }
    
    return '';
  };

  const handleMouseEnter = useCallback((type: 'deployment' | 'code') => {
    const timeoutRef = type === 'deployment' ? deploymentTimeoutRef : codeTimeoutRef;
    const setShowTooltip = type === 'deployment' ? setShowDeploymentTooltip : setShowCodeTooltip;

    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
    }
    setShowTooltip(true);
  }, []);

  const handleMouseLeave = useCallback((type: 'deployment' | 'code') => {
    const timeoutRef = type === 'deployment' ? deploymentTimeoutRef : codeTimeoutRef;
    const setShowTooltip = type === 'deployment' ? setShowDeploymentTooltip : setShowCodeTooltip;

    timeoutRef.current = setTimeout(() => {
      setShowTooltip(false);
    }, 100); // Small delay to prevent flickering
  }, []);

  const renderCommit = (commit: any, type: 'deployment' | 'code', commitUrl?: string) => {
    const iconType = type === 'deployment' ? 'file' : 'code';
    const showTooltip = type === 'deployment' ? showDeploymentTooltip : showCodeTooltip;

    if (commit && (commit.sha || commit.subject || commit.author)) {
      return (
        <div className={`commit-info ${getStatusClass(type)}`}>
          <div className="commit-content">
            <div className="commit-header">
              {renderSha(commit, commitUrl)}
              <span 
                className="commit-subject" 
                onMouseEnter={() => handleMouseEnter(type)}
                onMouseLeave={() => handleMouseLeave(type)}
              >
                {commit.subject || 'N/A'}
              </span>
            </div>
            <div className="commit-meta">
              <span className="commit-author">by {commit.author || 'N/A'}</span>
              {commit.date && (
                <span className="commit-date">
                  authored <span title={commit.date}><TimeAgo date={commit.date} /></span>
                </span>
              )}
            </div>
            {showTooltip && (
              <div
                className="tooltip-container anchored-tooltip"
                onMouseEnter={() => handleMouseEnter(type)}
                onMouseLeave={() => handleMouseLeave(type)}
              >
                {getTooltipContent(commit)}
              </div>
            )}
          </div>
          <div className="commit-icon-wrapper">
            {getIcon(iconType)}
          </div>
        </div>
      );
    } else {
      return (
        <div className={`commit-info ${getStatusClass(type)}`}>
          <div className="commit-content">
            <div className="commit-header">
              <span className="commit-sha">N/A</span>
              <span className="commit-subject"></span>
            </div>
            <div className="commit-meta">
              <span className="commit-author"></span>
            </div>
          </div>
          <div className="commit-icon-wrapper">
            {getIcon(iconType)}
          </div>
        </div>
      );
    }
  };

  if (!title) {
    return (
      <div className="commits-section">
        {renderCommit(deploymentCommit, 'deployment', deploymentCommitUrl)}
        {codeCommit && renderCommit(codeCommit, 'code', codeCommitUrl || '')}
      </div>
    );
  }

  // Render with group structure
  return (
    <div className={`commit-group ${className}`}>
      <div className="commit-group-header">
        <StatusIcon phase={status} type="health" />
        <h4 className="commit-group-title">
          {title}
          {prUrl && prNumber && (
            <a 
              href={prUrl} 
              target="_blank" 
              rel="noopener noreferrer"
              className={`pr-indicator ${isActive ? 'pr-merged' : ''}`}
              title={`View PR #${prNumber}${isActive ? ' (Merged)' : ''}`}
            >
              <GoGitPullRequest className="pr-icon" />
              PR #{prNumber}
            </a>
          )}
        </h4>
      </div>
      <div className="commits-section">
        {renderCommit(deploymentCommit, 'deployment', deploymentCommitUrl)}
        {codeCommit && renderCommit(codeCommit, 'code', codeCommitUrl || '')}
      </div>
      
      {/* Display checks for this section */}
      {healthSummary?.shouldDisplay && checks && (
        <HealthSummary 
          checks={checks} 
          title={`${title || 'Section'} Checks`} 
          status={status} 
          healthSummary={healthSummary}
        />
      )}
    </div>
  );
};

export default CommitInfo;
