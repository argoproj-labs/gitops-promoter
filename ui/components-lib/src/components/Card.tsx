import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState, useMemo, useRef, useEffect } from 'react';
import ActiveCard from './ActiveCard';
import ProposedChangesCard from './ProposedChangesCard';
import {
  EnrichedEnvDetails,
  enrichFromEnvironments,
  getProcessingEnvs,
} from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';
import './Card.scss';

export interface CardProps {
  environments: Environment[];
}

const Card: React.FC<CardProps> = ({ environments }) => {
  const [isVerticalLayout, setIsVerticalLayout] = useState<boolean>(true);
  const wrapperRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;

    const detectFlexDirection = () => {
      const flexDirection = window.getComputedStyle(wrapper).flexDirection;
      setIsVerticalLayout(flexDirection === 'row');
    };

    detectFlexDirection();
    const observer = new ResizeObserver(detectFlexDirection);
    observer.observe(wrapper);
    return () => observer.disconnect();
  }, []);

  const processingEnvs = useMemo(() => getProcessingEnvs(environments), [environments]);

  const enrichedEnvs = useMemo(() => {
    return environments.map((env) => enrichFromEnvironments([env], 0)[0]);
  }, [environments]);

  return (
    <div className="env-cards-container">
      <div className="env-cards-wrapper" ref={wrapperRef}>
        {enrichedEnvs.map((env: EnrichedEnvDetails, index) => {
          const branch = env.branch;
          const proposedStatus = env.promotionStatus;
          const isProcessing = processingEnvs.has(branch);

          const activeDeploymentCommit = {
            sha: env.activeSha,
            author: env.activeCommitAuthor,
            subject: env.activeCommitSubject,
            body: env.activeCommitMessage,
            date: env.activeCommitDate,
          };

          const proposedDeploymentCommit = {
            sha: env.proposedSha,
            author: env.proposedDryCommitAuthor,
            subject: env.proposedDryCommitSubject,
            body: env.proposedDryCommitBody,
            date: env.proposedDryCommitDate,
          };

          const mergeTimeAgo = env.activeMergeTimeAgo ?? undefined;

          const hasPendingProposal =
            proposedStatus !== undefined && ['pending', 'failure'].includes(proposedStatus);
          const cardClassName = ['env-card', hasPendingProposal ? '' : 'single-commit-group']
            .filter(Boolean)
            .join(' ');

          return (
            <React.Fragment key={env.branch}>
              <div className="env-card-column">
                <div className={cardClassName}>
                  <ActiveCard
                    branch={branch}
                    activeStatus={env.activeStatus as StatusType}
                    deploymentCommit={activeDeploymentCommit}
                    codeCommit={env.activeReferenceCommit}
                    deploymentCommitUrl={env.activeCommitUrl}
                    codeCommitUrl={env.activeReferenceCommitUrl}
                    prUrl={env.activePrUrl}
                    prNumber={env.activePrNumber?.toString()}
                    mergeTimeAgo={mergeTimeAgo}
                    prTooltip={env.activePrTooltip}
                    checks={env.activeChecks}
                    healthSummary={env.activeChecksSummary}
                  />

                  {isProcessing ? (
                    <div className="commit-group env-card__proposed-loading">
                      <div className="commit-group-header">
                        <StatusIcon phase="pending" type="health" />
                        <h4 className="commit-group-title">Proposed</h4>
                      </div>
                      <div
                        className="env-card__proposed-loading-bar"
                        role="progressbar"
                        aria-label="Preparing newer commit"
                      />
                      <div className="env-card__proposed-loading-text">
                        preparing newer commit&hellip;
                      </div>
                    </div>
                  ) : proposedStatus !== 'promoted' && proposedStatus !== 'success' ? (
                    <ProposedChangesCard
                      deploymentCommit={proposedDeploymentCommit}
                      codeCommit={env.proposedReferenceCommit}
                      status={env.proposedStatus as StatusType}
                      deploymentCommitUrl={env.proposedDryCommitUrl}
                      codeCommitUrl={env.proposedReferenceCommitUrl}
                      checks={env.proposedChecks}
                      healthSummary={env.proposedChecksSummary}
                      prUrl={env.prUrl}
                      prNumber={env.prNumber?.toString()}
                      prTooltip={env.prTooltip}
                    />
                  ) : null}
                </div>
              </div>
              {index < enrichedEnvs.length - 1 && (
                <div className="arrow-separator">
                  <svg
                    width="24"
                    height="24"
                    viewBox="0 0 24 24"
                    fill="none"
                    xmlns="http://www.w3.org/2000/svg"
                    style={{ transform: isVerticalLayout ? 'rotate(0deg)' : 'rotate(90deg)' }}
                  >
                    <path
                      d="M5 12h14m-6-6l6 6-6 6"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                </div>
              )}
            </React.Fragment>
          );
        })}
      </div>
    </div>
  );
};

export default Card;
