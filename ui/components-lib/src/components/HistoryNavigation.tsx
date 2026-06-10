import React, { useMemo, useState, useEffect, useCallback, useRef } from 'react';
import {
  FaCheckCircle,
  FaTimesCircle,
  FaSpinner,
  FaQuestionCircle,
  FaExpand,
  FaCompress,
} from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { timeAgo, formatDate, extractNameOnly, getCommitUrl } from '@shared/utils/util';
import { getHealthStatus } from '@shared/utils/getStatus';
import { enrichFromEnvironments } from '@shared/utils/PSData';
import type { History, CommitStatus, Environment } from '@shared/types/promotion';
import CommitInfo from './CommitInfo';
import { StatusType } from './StatusIcon';
import './HistoryNavigation.scss';

interface HistoryNavigationProps {
  history: History[];
  currentIndex: number;
  onHistorySelect: (index: number) => void;
  branch?: string;
  environment?: Environment;
  startInPanel?: boolean;
  onClose?: () => void;
}

type HealthKey = 'success' | 'failure' | 'pending' | 'unknown';

function healthFromStatuses(commitStatuses: CommitStatus[] | undefined): HealthKey {
  const checks = (commitStatuses || []).map((cs) => ({
    name: cs.key,
    status: cs.phase || 'unknown',
    description: cs.description,
    url: cs.url,
  }));
  return getHealthStatus(checks);
}

interface StopData {
  index: number;
  health: HealthKey;
  subject: string;
  author: string;
  shortSha: string;
  commitUrl: string;
  dateLabel: string;
  timeAgoLabel: string;
  prUrl?: string;
  prId?: string;
  checkCounts: { pass: number; fail: number; pending: number; total: number };
  transition?: 'regression' | 'recovery' | 'unchanged';
}

function buildStop(entry: History, index: number): StopData {
  const dry = entry.active?.dry;
  const sha = dry?.sha || '';
  const statuses = entry.active?.commitStatuses || [];

  let pass = 0,
    fail = 0,
    pending = 0;
  for (const cs of statuses) {
    if (cs.phase === 'success') pass++;
    else if (cs.phase === 'failure') fail++;
    else if (cs.phase === 'pending') pending++;
  }

  return {
    index,
    health: healthFromStatuses(statuses),
    subject: dry?.subject?.trim() || '—',
    author: dry?.author ? extractNameOnly(dry.author) : '—',
    shortSha: sha ? sha.slice(0, 7) : '—',
    commitUrl: getCommitUrl(dry?.repoURL || '', sha),
    dateLabel: dry?.commitTime ? formatDate(dry.commitTime) : '—',
    timeAgoLabel: dry?.commitTime ? timeAgo(dry.commitTime) : '',
    prUrl: entry.pullRequest?.url,
    prId: entry.pullRequest?.id,
    checkCounts: { pass, fail, pending, total: statuses.length },
  };
}

const healthIcon: Record<HealthKey, React.ReactNode> = {
  success: <FaCheckCircle />,
  failure: <FaTimesCircle />,
  pending: <FaSpinner className="fa-spin" />,
  unknown: <FaQuestionCircle />,
};

interface StopRowProps {
  stop: StopData;
  isSelected: boolean;
  isCompareTarget?: boolean;
  onSelect: () => void;
}

const StopRow: React.FC<StopRowProps> = ({ stop, isSelected, isCompareTarget, onSelect }) => (
  <button
    type="button"
    className={`history-subway__stop ${isSelected ? 'history-subway__stop--selected' : ''} ${isCompareTarget ? 'history-subway__stop--compare' : ''}`}
    onClick={onSelect}
    aria-label={`${stop.subject} by ${stop.author}, ${stop.dateLabel}`}
    aria-current={isSelected ? 'step' : undefined}
  >
    <div className={`history-subway__dot history-subway__dot--${stop.health}`}>
      {healthIcon[stop.health]}
    </div>
    <div className="history-subway__details">
      <span className="history-subway__subject">{stop.subject}</span>
      <span className="history-subway__meta">
        <span className="history-subway__author">{stop.author}</span>
        {stop.commitUrl ? (
          <a
            href={stop.commitUrl}
            className="history-subway__sha"
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
          >
            {stop.shortSha}
          </a>
        ) : (
          <span className="history-subway__sha history-subway__sha--muted">{stop.shortSha}</span>
        )}
        {stop.prUrl && stop.prId && (
          <a
            href={stop.prUrl}
            className="history-subway__pr"
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
          >
            <GoGitPullRequest />
            <span>#{stop.prId}</span>
          </a>
        )}
      </span>
      <span className="history-subway__footer">
        <span className="history-subway__checks">
          {stop.checkCounts.total > 0 && (
            <>
              {stop.checkCounts.pass > 0 && (
                <span className="history-subway__check history-subway__check--pass">
                  {stop.checkCounts.pass} passed
                </span>
              )}
              {stop.checkCounts.fail > 0 && (
                <span className="history-subway__check history-subway__check--fail">
                  {stop.checkCounts.fail} failed
                </span>
              )}
              {stop.checkCounts.pending > 0 && (
                <span className="history-subway__check history-subway__check--pending">
                  {stop.checkCounts.pending} pending
                </span>
              )}
            </>
          )}
        </span>
        <span className="history-subway__time">
          {stop.index === 0 ? 'Current' : stop.dateLabel}
          {stop.index !== 0 && stop.timeAgoLabel && (
            <span className="history-subway__time-ago"> · {stop.timeAgoLabel}</span>
          )}
        </span>
      </span>
    </div>
  </button>
);

/**
 * Renders the full CommitInfo detail panel for the selected history stop,
 * reusing the same component the main card uses.
 */
const StopDetailPanel: React.FC<{
  environment: Environment;
  historyIndex: number;
  view: 'deployment' | 'code';
}> = ({ environment, historyIndex, view }) => {
  const enriched = useMemo(
    () => enrichFromEnvironments([environment], historyIndex)[0],
    [environment, historyIndex],
  );
  if (!enriched) return null;

  const isHistoric = historyIndex > 0;

  /* ── Deployment tab: hydrated manifest commits + checks ── */
  if (view === 'deployment') {
    const activeDeploymentCommit = {
      sha: enriched.activeSha,
      author: enriched.activeCommitAuthor,
      subject: enriched.activeCommitSubject,
      body: enriched.activeCommitMessage,
      date: enriched.activeCommitDate,
    };

    const proposedDeploymentCommit = {
      sha: enriched.proposedSha,
      author: enriched.proposedDryCommitAuthor,
      subject: enriched.proposedDryCommitSubject,
      body: enriched.proposedDryCommitBody,
      date: enriched.proposedDryCommitDate,
    };

    return (
      <div className="history-subway__detail-panel">
        <CommitInfo
          title="Active"
          deploymentCommit={activeDeploymentCommit}
          codeCommit={null}
          isActive={true}
          status={enriched.activeStatus as StatusType}
          deploymentCommitUrl={enriched.activeCommitUrl}
          codeCommitUrl={null}
          checks={enriched.activeChecks}
          healthSummary={enriched.activeChecksSummary}
          prUrl={enriched.activePrUrl}
          prNumber={enriched.activePrNumber?.toString()}
          additionalChecks={isHistoric ? enriched.proposedChecks : undefined}
          additionalChecksTitle={isHistoric ? 'Proposed' : undefined}
          additionalChecksTitleTooltip={
            isHistoric
              ? 'State of proposed checks at the time the promotion PR was merged'
              : undefined
          }
          primaryChecksTitle={isHistoric ? 'Active' : undefined}
          primaryChecksTitleTooltip={
            isHistoric
              ? 'State of active checks when this promotion was superseded'
              : undefined
          }
          mergeTimeAgo={isHistoric ? (enriched.historyMergeTimeAgo ?? undefined) : undefined}
        />
        {!isHistoric &&
          enriched.promotionStatus !== 'promoted' &&
          enriched.promotionStatus !== 'success' && (
            <CommitInfo
              title="Proposed"
              deploymentCommit={proposedDeploymentCommit}
              codeCommit={null}
              isActive={false}
              status={enriched.proposedStatus as StatusType}
              className="proposed"
              deploymentCommitUrl={enriched.proposedDryCommitUrl}
              codeCommitUrl={null}
              checks={enriched.proposedChecks}
              healthSummary={enriched.proposedChecksSummary}
              prUrl={enriched.prUrl}
              prNumber={enriched.prNumber?.toString()}
            />
          )}
      </div>
    );
  }

  /* ── Code tab: source (reference) commits ── */
  const activeCodeCommit = enriched.activeReferenceCommit
    ? {
        sha: enriched.activeReferenceCommit.sha,
        author: enriched.activeReferenceCommit.author,
        subject: enriched.activeReferenceCommit.subject,
        body: enriched.activeReferenceCommit.body,
        date: enriched.activeReferenceCommit.date,
      }
    : null;

  const proposedCodeCommit = enriched.proposedReferenceCommit
    ? {
        sha: enriched.proposedReferenceCommit.sha,
        author: enriched.proposedReferenceCommit.author,
        subject: enriched.proposedReferenceCommit.subject,
        body: enriched.proposedReferenceCommit.body,
        date: enriched.proposedReferenceCommit.date,
      }
    : null;

  if (!activeCodeCommit && !proposedCodeCommit) {
    return (
      <div className="history-subway__detail-panel">
        <p className="history-panel__no-detail">
          No source code commit data available for this stop.
        </p>
      </div>
    );
  }

  return (
    <div className="history-subway__detail-panel">
      {activeCodeCommit && (
        <CommitInfo
          title="Active — Source Commit"
          deploymentCommit={activeCodeCommit}
          codeCommit={null}
          isActive={true}
          status={enriched.activeStatus as StatusType}
          deploymentCommitUrl={enriched.activeReferenceCommitUrl || undefined}
          codeCommitUrl={null}
          prUrl={enriched.activePrUrl}
          prNumber={enriched.activePrNumber?.toString()}
        />
      )}
      {proposedCodeCommit &&
        !isHistoric &&
        enriched.promotionStatus !== 'promoted' &&
        enriched.promotionStatus !== 'success' && (
          <CommitInfo
            title="Proposed — Source Commit"
            deploymentCommit={proposedCodeCommit}
            codeCommit={null}
            isActive={false}
            status={enriched.proposedStatus as StatusType}
            className="proposed"
            deploymentCommitUrl={enriched.proposedReferenceCommitUrl || undefined}
            codeCommitUrl={null}
            prUrl={enriched.prUrl}
            prNumber={enriched.prNumber?.toString()}
          />
        )}
    </div>
  );
};

const MIN_PANEL_WIDTH = 360;
const MAX_PANEL_WIDTH_RATIO = 0.85;
const DEFAULT_PANEL_WIDTH = 520;

const HistoryNavigation: React.FC<HistoryNavigationProps> = ({
  history,
  currentIndex,
  onHistorySelect,
  branch,
  environment,
  startInPanel = false,
  onClose,
}) => {
  const [fullPage, setFullPage] = useState(startInPanel);
  const [panelWidth, setPanelWidth] = useState(DEFAULT_PANEL_WIDTH);
  const [panelTab, setPanelTab] = useState<'deployment' | 'code'>('deployment');
  const [compareMode, setCompareMode] = useState(false);
  const [compareIndex, setCompareIndex] = useState<number | null>(null);
  const dragging = useRef(false);
  const stops = useMemo(() => history.map((entry, i) => buildStop(entry, i)), [history]);

  const healthRank: Record<HealthKey, number> = { failure: 0, unknown: 1, pending: 2, success: 3 };
  const railTransition = useCallback(
    (newerIdx: number, olderIdx: number): 'regression' | 'recovery' | null => {
      if (newerIdx >= stops.length || olderIdx >= stops.length) return null;
      const newer = healthRank[stops[newerIdx].health];
      const older = healthRank[stops[olderIdx].health];
      if (newer < older) return 'regression';
      if (newer > older) return 'recovery';
      return null;
    },
    [stops],
  );

  const closePanel = useCallback(() => {
    setFullPage(false);
    onClose?.();
  }, [onClose]);

  const handleEsc = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape' && fullPage) closePanel();
    },
    [fullPage, closePanel],
  );

  useEffect(() => {
    if (fullPage) {
      document.addEventListener('keydown', handleEsc);
      document.body.style.overflow = 'hidden';
    }
    return () => {
      document.removeEventListener('keydown', handleEsc);
      document.body.style.overflow = '';
    };
  }, [fullPage, handleEsc]);

  const onDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    dragging.current = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const onMove = (ev: MouseEvent) => {
      if (!dragging.current) return;
      const maxW = window.innerWidth * MAX_PANEL_WIDTH_RATIO;
      const newWidth = Math.min(maxW, Math.max(MIN_PANEL_WIDTH, window.innerWidth - ev.clientX));
      setPanelWidth(newWidth);
    };

    const onUp = () => {
      dragging.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };

    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }, []);

  const trackAndStops = (
    <div className="history-subway__track">
      {stops.map((stop, i) => {
        const isSelected = currentIndex === stop.index;
        const transition = i > 0 ? railTransition(i - 1, i) : null;
        return (
          <React.Fragment key={stop.index}>
            {i > 0 && (
              <div className="history-subway__rail-wrapper" aria-hidden>
                <div
                  className={`history-subway__rail ${currentIndex >= i ? 'history-subway__rail--lit' : ''}`}
                />
                {transition && (
                  <span className={`history-subway__transition history-subway__transition--${transition}`}>
                    {transition === 'regression' ? 'Regressed' : 'Recovered'}
                  </span>
                )}
              </div>
            )}
            <StopRow
              stop={stop}
              isSelected={isSelected}
              isCompareTarget={compareMode && compareIndex === stop.index}
              onSelect={() => {
                if (compareMode && currentIndex !== stop.index) {
                  setCompareIndex(stop.index);
                } else {
                  onHistorySelect(stop.index);
                }
              }}
            />
          </React.Fragment>
        );
      })}
    </div>
  );

  /* ── Inline mode (only when not launched as a panel) ── */
  if (!fullPage) {
    if (startInPanel) return null;
    return (
      <div className="history-subway" role="group" aria-label="Promotion history timeline">
        <div className="history-subway__header">
          <span className="history-subway__route-title">History</span>
          <button
            type="button"
            className="history-subway__maximize-btn"
            onClick={() => setFullPage(true)}
            title="Full page view"
          >
            <FaExpand />
          </button>
        </div>
        {trackAndStops}
      </div>
    );
  }

  /* ── Side panel mode ── */
  return (
    <>
      <div
        className="history-panel__backdrop"
        onClick={closePanel}
        aria-hidden
      />
      <aside
        className="history-panel"
        role="dialog"
        aria-label="Promotion history"
        style={{ width: panelWidth }}
      >
        <div
          className="history-panel__drag-handle"
          onMouseDown={onDragStart}
          title="Drag to resize"
        />
        <div className="history-panel__header">
          <span className="history-panel__title">
            {branch ? `${branch} — History` : 'Promotion History'}
          </span>
          <div className="history-panel__header-actions">
            <button
              type="button"
              className={`history-panel__compare-btn ${compareMode ? 'history-panel__compare-btn--active' : ''}`}
              onClick={() => {
                setCompareMode((p) => !p);
                setCompareIndex(null);
              }}
              title={compareMode ? 'Exit compare' : 'Compare two stops'}
            >
              Compare
            </button>
            <button
              type="button"
              className="history-panel__close-btn"
              onClick={closePanel}
              title="Close panel"
            >
              <FaCompress />
            </button>
          </div>
        </div>

        {/* Tabs */}
        <div className="history-panel__tabs">
          <button
            type="button"
            className={`history-panel__tab ${panelTab === 'deployment' ? 'history-panel__tab--active' : ''}`}
            onClick={() => setPanelTab('deployment')}
          >
            Deployment
          </button>
          <button
            type="button"
            className={`history-panel__tab ${panelTab === 'code' ? 'history-panel__tab--active' : ''}`}
            onClick={() => setPanelTab('code')}
          >
            Code
          </button>
        </div>

        <div className="history-panel__body">
          {/* Subway track */}
          <div className="history-panel__track-section">{trackAndStops}</div>

          {/* Detail panel */}
          {compareMode && compareIndex !== null && environment ? (
            <div className="history-panel__compare-section">
              <div className="history-panel__compare-col">
                <div className="history-panel__detail-label">
                  Stop {currentIndex + 1} — {stops[currentIndex]?.subject || 'Selected'}
                </div>
                <StopDetailPanel environment={environment} historyIndex={currentIndex} view={panelTab} />
              </div>
              <div className="history-panel__compare-divider" />
              <div className="history-panel__compare-col">
                <div className="history-panel__detail-label">
                  Stop {compareIndex + 1} — {stops[compareIndex]?.subject || 'Compare'}
                </div>
                <StopDetailPanel environment={environment} historyIndex={compareIndex} view={panelTab} />
              </div>
            </div>
          ) : (
            <div className="history-panel__detail-section">
              <div className="history-panel__detail-label">
                {compareMode
                  ? 'Click a second stop to compare'
                  : panelTab === 'deployment'
                    ? 'Deployment Details'
                    : 'Code Details'}
              </div>
              {environment ? (
                <StopDetailPanel environment={environment} historyIndex={currentIndex} view={panelTab} />
              ) : (
                <p className="history-panel__no-detail">
                  Select a stop to view details.
                </p>
              )}
            </div>
          )}
        </div>
      </aside>
    </>
  );
};

export default HistoryNavigation;
