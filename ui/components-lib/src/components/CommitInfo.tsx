import { FaRegFileAlt } from 'react-icons/fa';
import { BsBraces } from 'react-icons/bs';
import { GoGitPullRequest } from 'react-icons/go';
import { StatusIcon, StatusType } from './StatusIcon';
import React from 'react';
import TimeAgo from './TimeAgo';
import HealthSummary from './HealthSummary';

export interface CommitInfoProps {
  title?: string;
  deploymentCommit: any;
  codeCommit: any;
  isActive?: boolean;
  status?: StatusType;
  className?: string;
  deploymentCommitUrl?: string;
  codeCommitUrl?: string;
  activeChecks?: any[];
  proposedChecks?: any[];
  activeChecksSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
  proposedChecksSummary?: { successCount: number; totalCount: number; shouldDisplay: boolean };
  prUrl?: string;
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
  activeChecks, 
  proposedChecks, 
  activeChecksSummary,
  proposedChecksSummary,
  prUrl, 
  prNumber 
}) => {
  const getIcon = (iconType: 'file' | 'code') => {
    if (iconType === 'code') return <BsBraces className="commit-icon" />;
    return <FaRegFileAlt className="commit-icon" />;
  };

  const getStatusClass = (type: 'deployment' | 'code') => {
    if (type === 'deployment') return 'commit-deployment';
    return 'commit-code';
  };

  const renderSha = (commit: any, commitUrl?: string) => {
    const sha = commit.sha?.substring(0, 8) || 'N/A';
    if (commitUrl && commit.sha && commit.sha !== '-' && commitUrl !== '-') {
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

  // Create full commit message for tooltip
  const getFullCommitMessage = (commit: any) => {
    const subject = commit.subject || '';
    const body = commit.body || '';
    
    // If we have both subject and body, show them separately
    if (subject && body) {
      return `${subject}\n\n${body}`;
    }
    
    // If we only have body, show just the body
    if (body) {
      return body;
    }
    
    // If we only have subject, show just the subject
    if (subject) {
      return subject;
    }
    
    // Fallback
    return 'N/A';
  };

  const renderCommit = (commit: any, type: 'deployment' | 'code', commitUrl?: string) => {
    const iconType = type === 'deployment' ? 'file' : 'code';
    
    if (commit && (commit.sha || commit.subject || commit.author)) {
      return (
        <div className={`commit-info ${getStatusClass(type)}`}>
          <div className="commit-content">
            <div className="commit-header">
              {renderSha(commit, commitUrl)}
              <span 
                className="commit-subject" 
                title={getFullCommitMessage(commit)}
              >
                {commit.subject || 'N/A'}
              </span>
            </div>
            <div className="commit-meta">
              <span className="commit-author">by {commit.author || 'N/A'}</span>
              {commit.date && commit.date !== '-' && (
                <span className="commit-date">
                  authored <span title={commit.date}><TimeAgo date={commit.date} /></span>
                </span>
              )}
            </div>
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

  // If no title, render just the commits without group structure
  if (!title) {
    return (
      <div className="commits-section">
        {renderCommit(deploymentCommit, 'deployment', deploymentCommitUrl)}
        {renderCommit(codeCommit, 'code', codeCommitUrl)}
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
              className="pr-indicator"
              title={`View PR #${prNumber}`}
            >
              <GoGitPullRequest className="pr-icon" />
              PR #{prNumber}
            </a>
          )}
        </h4>
      </div>
      <div className="commits-section">
        {renderCommit(deploymentCommit, 'deployment', deploymentCommitUrl)}
        {renderCommit(codeCommit, 'code', codeCommitUrl)}
      </div>
      
      {/* Display checks */}
      {title === "Active" && activeChecksSummary?.shouldDisplay && activeChecks && (
        <HealthSummary 
          checks={activeChecks} 
          title="Active Checks" 
          status={status} 
          healthSummary={activeChecksSummary}
        />
      )}
      {title === "Proposed" && proposedChecksSummary?.shouldDisplay && proposedChecks && (
        <HealthSummary 
          checks={proposedChecks} 
          title="Proposed Checks" 
          status={status} 
          healthSummary={proposedChecksSummary}
        />
      )}
    </div>
  );
};

export default CommitInfo; 