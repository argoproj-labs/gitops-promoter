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
          const phase = env.phase;
          const proposedStatus = env.promotionStatus;

          // Active commits (currently deployed)
          const activeDeploymentCommit = {
            sha: env.drySha,
            author: env.dryCommitAuthor,
            subject: env.dryCommitSubject,
            body: env.dryCommitMessage,
            date: env.dryCommitDate
          };

          const activeCodeCommit = {
            sha: env.referenceSha,
            author: env.referenceCommitAuthor,
            subject: env.referenceCommitSubject,
            body: env.referenceCommitBody || '',
            date: env.referenceCommitDate
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
                  status={phase as StatusType}
                  deploymentCommitUrl={env.dryCommitUrl}
                  codeCommitUrl={env.referenceCommitUrl}
                  activeChecks={env.activeChecks}
                  proposedChecks={env.proposedChecks}
                  activeChecksSummary={env.activeChecksSummary}
                  proposedChecksSummary={env.proposedChecksSummary}
                />


                {/* Proposed Commits Section - Only show if not promoted/success */}
                {proposedStatus !== 'promoted' && proposedStatus !== 'success' && (
                  <CommitInfo
                    title="Proposed"
                    deploymentCommit={proposedDeploymentCommit}
                    codeCommit={proposedCodeCommit}
                    isActive={false}
                    status={proposedStatus as StatusType}
                    className="proposed"
                    deploymentCommitUrl={env.proposedDryCommitUrl}
                    codeCommitUrl={env.proposedReferenceCommitUrl}
                    activeChecks={env.activeChecks}
                    proposedChecks={env.proposedChecks}
                    activeChecksSummary={env.activeChecksSummary}
                    proposedChecksSummary={env.proposedChecksSummary}
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