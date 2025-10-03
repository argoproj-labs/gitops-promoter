import { FaServer, FaHistory } from 'react-icons/fa';
import { StatusType } from './StatusIcon';
import React, { useState, useMemo } from 'react';
import CommitInfo from './CommitInfo';
import HistoryNavigation from './HistoryNavigation';
import {EnrichedEnvDetails, enrichFromEnvironments} from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';
import './Card.scss';

export interface CardProps {
  environments: Environment[];
}

const Card: React.FC<CardProps> = ({ environments }) => {
  const isHorizontalLayout = environments.length >= 4;
  
  const [historyMode, setHistoryMode] = useState<{ [branch: string]: number }>({});
  const [hoveredIcon, setHoveredIcon] = useState<string | null>(null);
  
  const selectHistoryEntry = (branch: string, index: number) => {
    setHistoryMode(prev => ({ ...prev, [branch]: index }));
  };
  
  
  const getHistoryIndex = (branch: string) => {
    return historyMode[branch] || 0;
  };
  

  const isInHistoryMode = (branch: string) => {
    return (historyMode[branch] || 0) > 0;
  };
  
  // Get enriched environments with current history index
  const enrichedEnvs = useMemo(() => {
    return environments.map(env => {
      const historyIndex = getHistoryIndex(env.branch);
      return enrichFromEnvironments([env], historyIndex)[0];
    });
  }, [environments, historyMode]);
  
  
  return (
    <div className="env-cards-container">
      <div className={`env-cards-wrapper ${isHorizontalLayout ? 'horizontal-layout' : ''}`}>
        {enrichedEnvs.map((env: EnrichedEnvDetails) => {
          const branch = env.branch;
          const proposedStatus = env.promotionStatus;
          const inHistoryMode = isInHistoryMode(branch);
          const isNavigationVisible = hoveredIcon === branch;
          const environment = environments.find(e => e.branch === branch);
          const history = environment?.history || [];

          // Active/Proposed deployment and code commits
          const activeDeploymentCommit = {
            sha: env.activeSha,
            author: env.activeCommitAuthor,
            subject: env.activeCommitSubject,
            body: env.activeCommitMessage,
            date: env.activeCommitDate
          };

          const proposedDeploymentCommit = {
            sha: env.proposedSha,
            author: env.proposedDryCommitAuthor,
            subject: env.proposedDryCommitSubject,
            body: env.proposedDryCommitBody,
            date: env.proposedDryCommitDate
          };

          return (
            <div key={env.branch} className="env-card-column">
              <div className={`env-card ${!inHistoryMode && proposedStatus && ['pending', 'failure'].includes(proposedStatus) ? '' : 'single-commit-group'}`}>
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
                    onMouseLeave={() => setHoveredIcon(null)}
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
                        onMouseEnter={() => setHoveredIcon(branch)}
                        title={inHistoryMode ? 'View history' : 'View history'}
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
                  codeCommit={env.activeReferenceCommit}
                  isActive={true}
                  status={env.activeStatus as StatusType}
                  deploymentCommitUrl={env.activeCommitUrl}
                  codeCommitUrl={env.activeReferenceCommitUrl}
                  checks={env.activeChecks}
                  healthSummary={env.activeChecksSummary}
                  prUrl={env.activePrUrl}
                  prNumber={env.activePrNumber?.toString()}
                />


                {/* Proposed Commits Section - Only show in current view */}
                {!inHistoryMode && proposedStatus !== 'promoted' && proposedStatus !== 'success' && (
                  <CommitInfo
                    title="Proposed"
                    deploymentCommit={proposedDeploymentCommit}
                    codeCommit={env.proposedReferenceCommit}
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