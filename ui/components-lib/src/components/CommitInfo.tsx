import { GoArchive } from 'react-icons/go';
import { BsBraces } from 'react-icons/bs';
import { Tooltip } from './Tooltip';
import React, { useState, useRef, useCallback } from 'react';
import TimeAgo from './TimeAgo';
import './CommitInfo.scss';
import { DeploymentCommit, ReferenceCommit } from '@shared/types/promotion';

export interface CommitInfoProps {
  deploymentCommit: DeploymentCommit;
  codeCommit: ReferenceCommit | null;
  deploymentCommitUrl?: string;
  codeCommitUrl: string | null;
}

type CommitView = Partial<Pick<DeploymentCommit, 'sha' | 'subject' | 'body' | 'author' | 'date'>>;

const CommitInfo: React.FC<CommitInfoProps> = ({
  deploymentCommit,
  codeCommit,
  deploymentCommitUrl,
  codeCommitUrl,
}) => {
  const [showDeploymentTooltip, setShowDeploymentTooltip] = useState(false);
  const [showCodeTooltip, setShowCodeTooltip] = useState(false);
  const deploymentTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const codeTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const getIcon = (iconType: 'file' | 'code') => {
    if (iconType === 'code') return <BsBraces className="commit-icon" />;
    return <GoArchive className="commit-icon" />;
  };

  const getStatusClass = (type: 'deployment' | 'code') => {
    if (type === 'deployment') return 'commit-deployment';
    return 'commit-code';
  };

  const renderSha = (commit: CommitView, commitUrl?: string) => {
    const sha = commit.sha?.substring(0, 8) || 'N/A';
    if (commitUrl && commit.sha) {
      return (
        <Tooltip content={`Open commit ${sha} on GitHub`}>
          <a href={commitUrl} target="_blank" rel="noopener noreferrer" className="commit-sha-link">
            {sha}
          </a>
        </Tooltip>
      );
    }
    return <span className="commit-sha">{sha}</span>;
  };

  const getTooltipContent = (commit: CommitView) => {
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
    }, 100);
  }, []);

  const renderCommit = (commit: CommitView, type: 'deployment' | 'code', commitUrl?: string) => {
    const iconType = type === 'deployment' ? 'file' : 'code';
    const showTooltip = type === 'deployment' ? showDeploymentTooltip : showCodeTooltip;

    if (commit && (commit.sha || commit.subject || commit.author)) {
      return (
        <div className={`commit-info ${getStatusClass(type)}`}>
          <div className="commit-content">
            <div className="commit-header">
              <span className="commit-icon-wrapper">{getIcon(iconType)}</span>
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
                  authored <TimeAgo date={commit.date} />
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
        </div>
      );
    } else {
      return (
        <div className={`commit-info ${getStatusClass(type)}`}>
          <div className="commit-content">
            <div className="commit-header">
              <span className="commit-icon-wrapper">{getIcon(iconType)}</span>
              <span className="commit-sha">N/A</span>
              <span className="commit-subject"></span>
            </div>
            <div className="commit-meta">
              <span className="commit-author"></span>
            </div>
          </div>
        </div>
      );
    }
  };

  return (
    <div className="commits-section">
      {renderCommit(deploymentCommit, 'deployment', deploymentCommitUrl)}
      {codeCommit && renderCommit(codeCommit, 'code', codeCommitUrl || '')}
    </div>
  );
};

export default CommitInfo;
