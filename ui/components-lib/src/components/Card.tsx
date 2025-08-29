import React, { useState, useMemo, useCallback } from 'react';
import { FaServer, FaHistory } from 'react-icons/fa';
import { StatusType } from './StatusIcon';
import CommitInfo from './CommitInfo';
import HistoryNavigation from './HistoryNavigation';
import { enrichFromEnvironments } from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';
import './Card.scss';

export interface CardProps {
  environments: Environment[];
}

interface HistoryMode {
  [branch: string]: number;
}

interface CommitData {
  sha: string;
  author: string;
  subject: string;
  body: string;
  date: string;
}

const Card: React.FC<CardProps> = ({ environments }) => {
  const [historyMode, setHistoryMode] = useState<HistoryMode>({});
  const [hoveredIcon, setHoveredIcon] = useState<string | null>(null);

  const isHorizontalLayout = environments.length >= 4;

  const selectHistoryEntry = useCallback((branch: string, index: number) => {
    setHistoryMode(prev => ({ ...prev, [branch]: index }));
  }, []);

  const getHistoryIndex = useCallback((branch: string): number => {
    return historyMode[branch] || 0;
  }, [historyMode]);

  const isInHistoryMode = useCallback((branch: string): boolean => {
    return (historyMode[branch] || 0) > 0;
  }, [historyMode]);

  const handleIconMouseLeave = useCallback(() => {
    setHoveredIcon(null);
  }, []);

  const handleIconMouseEnter = useCallback((branch: string) => {
    setHoveredIcon(branch);
  }, []);

  // Get enriched environments with current history index
  const enrichedEnvs = useMemo(() => {
    return environments.map(env => {
      const historyIndex = getHistoryIndex(env.branch);
      return enrichFromEnvironments([env], historyIndex)[0];
    });
  }, [environments, historyMode, getHistoryIndex]);

  const createCommitData = useCallback((
    sha: string,
    author: string,
    subject: string,
    body: string,
    date: string
  ): CommitData => ({
    sha,
    author,
    subject,
    body,
    date
  }), []);

  return (
    <div className="env-cards-container">
      <div className={`env-cards-wrapper ${isHorizontalLayout ? 'horizontal-layout' : ''}`}>
        {enrichedEnvs.map((env: any) => {
          const branch = env.branch;
          const proposedStatus = env.promotionStatus;
          const inHistoryMode = isInHistoryMode(branch);
          const isNavigationVisible = hoveredIcon === branch;
          const environment = environments.find(e => e.branch === branch);
          const history = environment?.history || [];

          // Active commits
          const activeDeploymentCommit = createCommitData(
            env.activeSha,
            env.activeCommitAuthor,
            env.activeCommitSubject,
            env.activeCommitMessage,
            env.activeCommitDate
          );

          const activeCodeCommit = createCommitData(
            env.activeReferenceSha,
            env.activeReferenceCommitAuthor,
            env.activeReferenceCommitSubject,
            env.activeReferenceCommitBody || '',
            env.activeReferenceCommitDate
          );

          // Proposed commits
          const proposedDeploymentCommit = createCommitData(
            env.proposedSha,
            env.proposedDryCommitAuthor,
            env.proposedDryCommitSubject,
            env.proposedDryCommitBody,
            env.proposedDryCommitDate
          );

          const proposedCodeCommit = createCommitData(
            env.proposedReferenceSha,
            env.proposedReferenceCommitAuthor,
            env.proposedReferenceCommitSubject,
            env.proposedReferenceCommitBody || '',
            env.proposedReferenceCommitDate
          );

          const shouldShowProposed = !inHistoryMode &&
            proposedStatus !== 'promoted' &&
            proposedStatus !== 'success';

          return (
            <div key={branch} className="env-card-column">
              <div className={`env-card ${shouldShowProposed ? '' : 'single-commit-group'}`}>
                <div className="env-card__title" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', position: 'relative' }}>
                  <div style={{ display: 'flex', alignItems: 'center' }}>
                    <FaServer className="env-card__icon" />
                    <span className="env-card__env-name">
                      {branch}
                      {inHistoryMode && <span className="history-indicator"> (History)</span>}
                    </span>
                  </div>
                  
                  <div 
                    className="history-controls"
                    onMouseLeave={handleIconMouseLeave}
                  >
                    {isNavigationVisible && history.length > 0 ? (
                      <HistoryNavigation
                        history={history}
                        currentIndex={getHistoryIndex(branch)}
                        onHistorySelect={(index) => selectHistoryEntry(branch, index)}
                      />
                    ) : (
                      <button 
                        className={`history-toggle ${inHistoryMode ? 'active' : ''}`}
                        onMouseEnter={() => handleIconMouseEnter(branch)}
                        title="View history"
                        type="button"
                        aria-label={`View history for ${branch}`}
                      >
                        <FaHistory />
                      </button>
                    )}
                  </div>
                </div>

                {/* Active Commits Section */}
                <CommitInfo
                  title="Active"
                  deploymentCommit={activeDeploymentCommit}
                  codeCommit={activeCodeCommit}
                  isActive={true}
                  status={env.activeStatus as StatusType}
                  deploymentCommitUrl={env.activeCommitUrl}
                  codeCommitUrl={env.activeReferenceCommitUrl}
                  checks={env.activeChecks}
                  healthSummary={env.activeChecksSummary}
                  prUrl={env.activePrUrl}
                  prNumber={env.activePrNumber?.toString()}
                />

                {/* Proposed Commits Section */}
                {shouldShowProposed && (
                  <CommitInfo
                    title="Proposed"
                    deploymentCommit={proposedDeploymentCommit}
                    codeCommit={proposedCodeCommit}
                    isActive={false}
                    status={env.proposedStatus as StatusType}
                    className="proposed"
                    deploymentCommitUrl={env.proposedDryCommitUrl}
                    codeCommitUrl={env.proposedReferenceCommitUrl}
                    checks={env.proposedChecks}
                    healthSummary={env.proposedChecksSummary}
                    prUrl={env.prUrl}
                    prNumber={env.prNumber?.toString()}
                  />
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default Card;