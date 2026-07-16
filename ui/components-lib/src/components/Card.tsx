import { FaServer, FaHistory } from 'react-icons/fa';
import { StatusIcon, StatusType } from './StatusIcon';
import React, { useState, useMemo, useRef, useEffect } from 'react';
import CommitInfo from './CommitInfo';
import HistoryNavigation from './HistoryNavigation';
import {
  EnrichedEnvDetails,
  enrichFromEnvironments,
  getProcessingEnvs,
} from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';
import './Card.scss';

export interface CardProps {
  environments: Environment[];
  highlightBranch?: string;
  onFocusChange?: (_branch: string | null) => void;
}

const Card: React.FC<CardProps> = ({ environments, highlightBranch, onFocusChange }) => {
  const [historyMode, setHistoryMode] = useState<{ [branch: string]: number }>({});
  const [hoveredIcon, setHoveredIcon] = useState<string | null>(null);
  const [isVerticalLayout, setIsVerticalLayout] = useState<boolean>(true);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const highlightRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const detectFlexDirection = () => {
      if (wrapperRef.current) {
        const styles = window.getComputedStyle(wrapperRef.current);
        const flexDirection = styles.flexDirection;
        setIsVerticalLayout(flexDirection === 'row');
      }
    };

    detectFlexDirection();
    window.addEventListener('resize', detectFlexDirection);
    return () => window.removeEventListener('resize', detectFlexDirection);
  }, []);

  const selectHistoryEntry = (branch: string, index: number) => {
    setHistoryMode((prev) => ({ ...prev, [branch]: index }));
  };

  const getHistoryIndex = (branch: string) => {
    return historyMode[branch] || 0;
  };

  const isInHistoryMode = (branch: string) => {
    return (historyMode[branch] || 0) > 0;
  };

  const processingEnvs = useMemo(() => getProcessingEnvs(environments), [environments]);

  // Get enriched environments with current history index
  const enrichedEnvs = useMemo(() => {
    return environments.map((env) => {
      const historyIndex = getHistoryIndex(env.branch);
      return enrichFromEnvironments([env], historyIndex)[0];
    });
  }, [environments, historyMode]);

  useEffect(() => {
    if (!highlightBranch) return;
    const node = highlightRef.current;
    if (!node) return;
    node.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
  }, [highlightBranch, enrichedEnvs]);

  const toggleFocus = (branch: string) => {
    onFocusChange?.(highlightBranch === branch ? null : branch);
  };

  return (
    <div className="env-cards-container">
      <div className="env-cards-wrapper" ref={wrapperRef}>
        {enrichedEnvs.map((env: EnrichedEnvDetails, index) => {
          const branch = env.branch;
          const proposedStatus = env.promotionStatus;
          const isProcessing = processingEnvs.has(branch);
          const inHistoryMode = isInHistoryMode(branch);
          const isNavigationVisible = hoveredIcon === branch;
          const environment = environments.find((e) => e.branch === branch);
          const history = environment?.history || [];

          // Active/Proposed deployment and code commits
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

          const mergeTimeAgo = inHistoryMode
            ? (env.historyMergeTimeAgo ?? undefined)
            : (env.activeMergeTimeAgo ?? undefined);

          const hasPendingProposal =
            !inHistoryMode &&
            proposedStatus !== undefined &&
            ['pending', 'failure'].includes(proposedStatus);
          // The highlighted card is both the focus target and the scroll target.
          const isFocused = highlightBranch === branch;
          const isScrollTarget = isFocused;
          const cardClassName = [
            'env-card',
            inHistoryMode ? 'history-mode' : '',
            hasPendingProposal ? '' : 'single-commit-group',
            isFocused ? 'highlighted' : '',
          ]
            .filter(Boolean)
            .join(' ');

          // Focus the card on click, but let clicks on interactive children
          // (history controls, commit/PR links) behave normally.
          const handleCardClick = (event: React.MouseEvent<HTMLDivElement>) => {
            if ((event.target as HTMLElement).closest('a, button')) return;
            toggleFocus(branch);
          };
          const handleCardKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
            if (event.target !== event.currentTarget) return;
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              toggleFocus(branch);
            }
          };

          return (
            <React.Fragment key={env.branch}>
              <div className="env-card-column">
                <div
                  className={cardClassName}
                  ref={isScrollTarget ? highlightRef : undefined}
                  role="button"
                  tabIndex={0}
                  aria-pressed={isFocused}
                  onClick={handleCardClick}
                  onKeyDown={handleCardKeyDown}
                >
                  <div
                    className="env-card__title"
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      position: 'relative',
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center' }}>
                      <FaServer className="env-card__icon" />
                      <div>
                        <span className="env-card__env-name">{branch}</span>
                      </div>
                    </div>

                    <div className="history-controls" onMouseLeave={() => setHoveredIcon(null)}>
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
                    additionalChecks={inHistoryMode ? env.proposedChecks : undefined}
                    additionalChecksTitle={inHistoryMode ? 'Proposed' : undefined}
                    additionalChecksTitleTooltip={
                      inHistoryMode
                        ? 'State of proposed checks at the time the promotion PR was merged'
                        : undefined
                    }
                    primaryChecksTitle={inHistoryMode ? 'Active' : undefined}
                    primaryChecksTitleTooltip={
                      inHistoryMode
                        ? 'State of active checks when this promotion was superseded'
                        : undefined
                    }
                    mergeTimeAgo={mergeTimeAgo}
                  />

                  {/* Proposed Commits Section (normal mode only) */}
                  {!inHistoryMode &&
                    (isProcessing ? (
                      // Processing: stand in for the Proposed section with a labeled
                      // loading placeholder while the newer commit is hydrated.
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
                    ) : null)}
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
