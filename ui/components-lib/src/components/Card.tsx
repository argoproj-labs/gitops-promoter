import { FaServer } from 'react-icons/fa';
import { StatusType } from './StatusIcon';
import React from 'react';
import CommitInfo from './CommitInfo';
import './Card.scss';

export interface CardProps {
  environments: any[];
}

const Card: React.FC<CardProps> = ({ environments }) => {
  // Add CSS class for horizontal layout when 4+ environments
  const isHorizontalLayout = environments.length >= 4;
  
  return (
    <div className="env-cards-container">
      <div className={`env-cards-wrapper ${isHorizontalLayout ? 'horizontal-layout' : ''}`}>
        {environments.map((env: any, envIdx: number) => {
          const branch = env.branch;
          const proposedStatus = env.promotionStatus;

          // Active/Proposed deployment and code commits
          const activeDeploymentCommit = {
            sha: env.activeSha,
            author: env.activeCommitAuthor,
            subject: env.activeCommitSubject,
            body: env.activeCommitMessage,
            date: env.activeCommitDate
          };

          const activeCodeCommit = {
            sha: env.activeReferenceSha,
            author: env.activeReferenceCommitAuthor,
            subject: env.activeReferenceCommitSubject,
            body: env.activeReferenceCommitBody || '',
            date: env.activeReferenceCommitDate
          };

          const proposedDeploymentCommit = {
            sha: env.proposedSha,
            author: env.proposedDryCommitAuthor,
            subject: env.proposedDryCommitSubject,
            body: env.proposedDryCommitBody,
            date: env.proposedDryCommitDate
          };

          const proposedCodeCommit = {
            sha: env.proposedReferenceSha,
            author: env.proposedReferenceCommitAuthor,
            subject: env.proposedReferenceCommitSubject,
            body: env.proposedReferenceCommitBody || '',
            date: env.proposedReferenceCommitDate
          };

          return (
            <div key={env.branch} className="env-card-column">
              <div className={`env-card ${(proposedStatus === 'promoted' || proposedStatus === 'success') ? 'single-commit-group' : ''}`}>
                <div className="env-card__title" style={{ display: 'flex', alignItems: 'center' }}>
                  <FaServer className="env-card__icon" />
                  <span className="env-card__env-name">{branch}</span>
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


                {/* Proposed Commits Section*/}
                {proposedStatus !== 'promoted' && proposedStatus !== 'success' && (
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