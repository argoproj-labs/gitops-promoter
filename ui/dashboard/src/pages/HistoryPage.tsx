import React, { useMemo, useState, useCallback, useRef, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  FaCheckCircle,
  FaTimesCircle,
  FaSpinner,
  FaQuestionCircle,
  FaChevronLeft,
  FaUndoAlt,
  FaBan,
  FaExclamationTriangle,
  FaTimes,
} from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import { timeAgo, formatDate, extractNameOnly, getCommitUrl } from '@shared/utils/util';
import { getHealthStatus } from '@shared/utils/getStatus';
import { enrichFromEnvironments } from '@shared/utils/PSData';
import CommitInfo from '@lib/components/CommitInfo';
import { StatusType } from '@lib/components/StatusIcon';
import type { History, CommitStatus, Environment, PromotionStrategy } from '@shared/types/promotion';
import './HistoryPage.scss';

type HealthKey = 'success' | 'failure' | 'pending' | 'unknown';
type EntryType = 'normal' | 'noop' | 'interrupted';

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
  entryType: EntryType;
  subject: string;
  author: string;
  shortSha: string;
  fullSha: string;
  codeSha?: string;
  commitUrl: string;
  dateLabel: string;
  timeAgoLabel: string;
  prUrl?: string;
  prId?: string;
  checkCounts: { pass: number; fail: number; pending: number; total: number };
}

function buildStops(history: History[]): StopData[] {
  return history.map((entry, index) => {
    const dry = entry.active?.dry;
    const sha = dry?.sha || '';
    const statuses = entry.active?.commitStatuses || [];
    const refSha = dry?.references?.[0]?.commit?.sha;

    let pass = 0, fail = 0, pending = 0;
    for (const cs of statuses) {
      if (cs.phase === 'success') pass++;
      else if (cs.phase === 'failure') fail++;
      else if (cs.phase === 'pending') pending++;
    }

    let entryType: EntryType = 'normal';
    // No-op: this entry's active dry SHA is identical to the older (next-index) entry
    if (index + 1 < history.length) {
      const olderSha = history[index + 1]?.active?.dry?.sha || '';
      if (sha && olderSha && sha === olderSha) entryType = 'noop';
    }
    // Interrupted: the entry had a pending proposed commit whose SHA never
    // became the active SHA in the newer (previous-index) entry AND the
    // proposed SHA is different from the entry's own active SHA (meaning
    // a promotion was genuinely in flight but got overridden).
    // Only flag this when the proposed commit has pending/unknown checks,
    // indicating the promotion hadn't finished.
    if (index > 0 && entryType === 'normal') {
      const proposedStatuses = entry.proposed?.commitStatuses || [];
      const hasPendingProposed = proposedStatuses.length > 0 &&
        proposedStatuses.some((cs: CommitStatus) => cs.phase === 'pending');
      if (hasPendingProposed) {
        const proposedSha = entry.proposed?.hydrated?.sha || '';
        const newerActiveSha = history[index - 1]?.active?.dry?.sha || '';
        if (proposedSha && newerActiveSha && proposedSha !== newerActiveSha && proposedSha !== sha) {
          entryType = 'interrupted';
        }
      }
    }

    return {
      index,
      health: healthFromStatuses(statuses),
      entryType,
      subject: dry?.subject?.trim() || '—',
      author: dry?.author ? extractNameOnly(dry.author) : '—',
      shortSha: sha ? sha.slice(0, 7) : '—',
      fullSha: sha,
      codeSha: refSha ? refSha.slice(0, 7) : undefined,
      commitUrl: getCommitUrl(dry?.repoURL || '', sha),
      dateLabel: dry?.commitTime ? formatDate(dry.commitTime) : '—',
      timeAgoLabel: dry?.commitTime ? timeAgo(dry.commitTime) : '',
      prUrl: entry.pullRequest?.url,
      prId: entry.pullRequest?.id,
      checkCounts: { pass, fail, pending, total: statuses.length },
    };
  });
}

const healthIcon: Record<HealthKey, React.ReactNode> = {
  success: <FaCheckCircle />,
  failure: <FaTimesCircle />,
  pending: <FaSpinner className="fa-spin" />,
  unknown: <FaQuestionCircle />,
};

const LANE_COLORS = ['#1f4f5e', '#6f42c1', '#d97706', '#0f766e', '#b91c1c', '#4338ca'];

interface LaneData {
  branch: string;
  environment: Environment;
  stops: StopData[];
  color: string;
}

const HEALTH_LABELS: Record<HealthKey, string> = {
  success: 'Healthy',
  failure: 'Failed',
  pending: 'Pending',
  unknown: 'Unknown',
};

interface SeenInHit {
  laneIdx: number;
  stopIdx: number;
  branch: string;
  color: string;
  health: HealthKey;
  timeLabel: string;
}

function findSeenIn(sha: string, subject: string, lanes: LaneData[]): SeenInHit[] {
  const hits: SeenInHit[] = [];
  for (let li = 0; li < lanes.length; li++) {
    const lane = lanes[li];
    for (let si = 0; si < lane.stops.length; si++) {
      const s = lane.stops[si];
      const shaMatch = sha && s.fullSha && s.fullSha.slice(0, 7) === sha.slice(0, 7);
      const subjectMatch = subject && s.subject === subject;
      if (shaMatch || subjectMatch) {
        hits.push({
          laneIdx: li,
          stopIdx: si,
          branch: lane.branch,
          color: lane.color,
          health: s.health,
          timeLabel: s.index === 0 ? 'Current' : s.timeAgoLabel || s.dateLabel,
        });
        break;
      }
    }
  }
  return hits;
}

/* ═══════════════════════════════════════
   Integrated Detail Panel (deployment + code)
   ═══════════════════════════════════════ */
const DetailPanel: React.FC<{
  environment: Environment;
  historyIndex: number;
}> = ({ environment, historyIndex }) => {
  const enriched = useMemo(
    () => enrichFromEnvironments([environment], historyIndex)[0],
    [environment, historyIndex],
  );
  if (!enriched) return null;
  const isHistoric = historyIndex > 0;

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
  const activeCodeCommit = enriched.activeReferenceCommit
    ? { sha: enriched.activeReferenceCommit.sha, author: enriched.activeReferenceCommit.author, subject: enriched.activeReferenceCommit.subject, body: enriched.activeReferenceCommit.body, date: enriched.activeReferenceCommit.date }
    : null;
  const proposedCodeCommit = enriched.proposedReferenceCommit
    ? { sha: enriched.proposedReferenceCommit.sha, author: enriched.proposedReferenceCommit.author, subject: enriched.proposedReferenceCommit.subject, body: enriched.proposedReferenceCommit.body, date: enriched.proposedReferenceCommit.date }
    : null;

  const showProposed = !isHistoric &&
    enriched.promotionStatus !== 'promoted' &&
    enriched.promotionStatus !== 'success';

  return (
    <div className="hp-detail__integrated">
      {/* Deployment section */}
      <div className="hp-detail__section">
        <span className="hp-detail__section-label">Deployment Commits</span>
        <div className="hp-detail__commits">
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
            additionalChecksTitleTooltip={isHistoric ? 'State of proposed checks at the time the promotion PR was merged' : undefined}
            primaryChecksTitle={isHistoric ? 'Active' : undefined}
            primaryChecksTitleTooltip={isHistoric ? 'State of active checks when this promotion was superseded' : undefined}
            mergeTimeAgo={isHistoric ? (enriched.historyMergeTimeAgo ?? undefined) : undefined}
          />
          {showProposed && (
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
      </div>

      {/* Source code section */}
      {(activeCodeCommit || proposedCodeCommit) && (
        <div className="hp-detail__section">
          <span className="hp-detail__section-label">Source Code Commits</span>
          <div className="hp-detail__commits">
            {activeCodeCommit && (
              <CommitInfo
                title="Active — Source"
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
            {proposedCodeCommit && showProposed && (
              <CommitInfo
                title="Proposed — Source"
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
        </div>
      )}
    </div>
  );
};

/* ═══════════════════════════════════════
   Subway Dot — enhanced with SHA, PR, entryType
   ═══════════════════════════════════════ */
const SubwayDot: React.FC<{
  stop: StopData;
  color: string;
  isSelected: boolean;
  isRelated: boolean;
  isCompact: boolean;
  onClick: () => void;
}> = ({ stop, color, isSelected, isRelated, isCompact, onClick }) => {
  // Health always takes visual priority over entryType for failure/pending
  const healthPriority = stop.health === 'failure' || stop.health === 'pending';
  const showEntryType = !healthPriority && stop.entryType !== 'normal';

  const dotClasses = [
    'subway-dot',
    isSelected ? 'subway-dot--selected' : '',
    !isSelected && isRelated ? 'subway-dot--related' : '',
    stop.index === 0 ? 'subway-dot--current' : 'subway-dot--historic',
    showEntryType && stop.entryType === 'noop' ? 'subway-dot--noop' : '',
    showEntryType && stop.entryType === 'interrupted' ? 'subway-dot--interrupted' : '',
    isCompact ? 'subway-dot--compact' : '',
  ].filter(Boolean).join(' ');

  const circleVariant = showEntryType
    ? (stop.entryType === 'interrupted' ? 'pending' : stop.entryType)
    : stop.health;

  return (
    <button
      type="button"
      className={dotClasses}
      onClick={onClick}
      title={`${stop.subject} — ${stop.author} (${stop.shortSha})`}
      aria-label={`${stop.subject} by ${stop.author}, ${stop.dateLabel}`}
    >
      <div
        className={`subway-dot__circle subway-dot__circle--${circleVariant}`}
        style={isSelected ? { boxShadow: `0 0 0 3px ${color}44, 0 0 0 6px ${color}22` } : undefined}
      >
        {stop.entryType === 'interrupted' ? healthIcon['pending'] : healthIcon[stop.health]}
      </div>
      {stop.index === 0 && <span className="subway-dot__live-badge">LIVE</span>}
      <div className="subway-dot__label">
        <span className="subway-dot__subject">{stop.subject}</span>
        <span className="subway-dot__sha-row">
          <span className="subway-dot__sha-pill">{stop.shortSha}</span>
          {stop.codeSha && <span className="subway-dot__sha-pill subway-dot__sha-pill--code">{stop.codeSha}</span>}
          {stop.prId && <span className="subway-dot__pr-pill">#{stop.prId}</span>}
        </span>
        <span className="subway-dot__time">
          {stop.index === 0 ? 'Current' : stop.timeAgoLabel || stop.dateLabel}
        </span>
        {stop.entryType !== 'normal' && (
          <span className={`subway-dot__entry-tag subway-dot__entry-tag--${stop.entryType}`}>
            {stop.entryType === 'noop' ? 'No change' : 'Interrupted'}
          </span>
        )}
      </div>
    </button>
  );
};

/* ═══════════════════════════════════════
   Main Page
   ═══════════════════════════════════════ */
const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigate();

  const { items, fetchItems } = PromotionStrategyStore();

  useEffect(() => {
    if (namespace) fetchItems(namespace);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  const strategy = items.find((ps: PromotionStrategy) => ps.metadata?.name === name);

  // Inline `strategy?.status?.environments` inside the memo so its identity
  // doesn't change on every render (the `|| []` fallback would create a fresh
  // array each pass and bust memoization).
  const lanes: LaneData[] = useMemo(() => {
    const envs = strategy?.status?.environments || [];
    return envs
      .filter((env: Environment) => (env.history?.length || 0) > 0 || env.active?.dry)
      .map((env: Environment, i: number) => {
        const currentEntry = {
          active: env.active,
          proposed: { hydrated: env.proposed?.hydrated },
          pullRequest: env.pullRequest,
        } as History;
        const allEntries = [currentEntry, ...(env.history || [])];
        return {
          branch: env.branch,
          environment: env,
          stops: buildStops(allEntries),
          color: LANE_COLORS[i % LANE_COLORS.length],
        };
      });
  }, [strategy]);

  const isCompact = lanes.length > 4;
  const maxStops = useMemo(() => Math.max(...lanes.map((l) => l.stops.length), 0), [lanes]);

  const [selected, setSelected] = useState<{ lane: number; stop: number } | null>(null);
  const [showRollbackInfo, setShowRollbackInfo] = useState(false);

  // Outermost ref kept on the page container for future use (selection sync,
  // overlay positioning). The drag-to-resize divider that was sketched in the
  // design has not been wired up; reintroduce that machinery only when the
  // divider markup lands.
  const containerRef = useRef<HTMLDivElement | null>(null);

  const selectedLane = selected !== null ? lanes[selected.lane] : null;
  const selectedStop = selected !== null && selectedLane ? selectedLane.stops[selected.stop] : null;

  const seenIn = useMemo(() => {
    if (!selectedStop) return [];
    return findSeenIn(selectedStop.fullSha, selectedStop.subject, lanes);
  }, [selectedStop, lanes]);

  const relatedMatch = useMemo(() => {
    if (!selectedStop) return { sha: '', subject: '' };
    return {
      sha: selectedStop.fullSha ? selectedStop.fullSha.slice(0, 7) : '',
      subject: selectedStop.subject,
    };
  }, [selectedStop]);

  const stateTransition = useMemo<{ from: HealthKey; to: HealthKey } | null>(() => {
    if (!selected || !selectedLane) return null;
    const idx = selected.stop;
    const stops = selectedLane.stops;
    if (idx + 1 >= stops.length) return null;
    const older = stops[idx + 1].health;
    const newer = stops[idx].health;
    if (older === newer) return null;
    return { from: older, to: newer };
  }, [selected, selectedLane]);

  const handleBack = () => navigate(`/promotion-strategies/${namespace}/${name}`);

  const handleSeenInClick = useCallback((laneIdx: number, stopIdx: number) => {
    setSelected({ lane: laneIdx, stop: stopIdx });
  }, []);

  if (!strategy) return <div className="hp-loading">Loading...</div>;

  if (lanes.length === 0) {
    return (
      <div className="hp-empty">
        <p>No history available for this promotion strategy.</p>
        <button onClick={handleBack} className="hp-empty__back">Go Back</button>
      </div>
    );
  }

  const isHistoricEntry = selectedStop ? selectedStop.index > 0 : false;

  return (
    <div className="hp" ref={containerRef}>
      <header className="hp-header">
        <button className="hp-header__back" onClick={handleBack} type="button">
          <FaChevronLeft />
          <span>Back to {name}</span>
        </button>
        <div className="hp-header__title">
          <h1>{name}</h1>
          <span className="hp-header__subtitle">Promotion History</span>
        </div>
      </header>

      <div className="hp-body">
        {/* ═══ Subway Map ═══ */}
        <div
          className={`subway-map ${isCompact ? 'subway-map--compact' : ''}`}
        >
          <div className="subway-map__scroll">
            {lanes.map((lane, laneIdx) => (
              <div className="subway-lane" key={lane.branch}>
                <div className="subway-lane__label" style={{ borderLeftColor: lane.color }}>
                  <span className="subway-lane__branch">{lane.branch}</span>
                  <span className="subway-lane__count">{lane.stops.length} entries</span>
                </div>
                <div className="subway-lane__track">
                  <div className="subway-lane__rail" style={{ background: lane.color }} />
                  <div className="subway-lane__stops">
                    {lane.stops.map((stop, stopIdx) => (
                      <React.Fragment key={stop.index}>
                        <SubwayDot
                          stop={stop}
                          color={lane.color}
                          isSelected={selected?.lane === laneIdx && selected?.stop === stopIdx}
                          isRelated={Boolean(
                            (relatedMatch.sha &&
                              stop.fullSha.slice(0, 7) === relatedMatch.sha) ||
                              (relatedMatch.subject && stop.subject === relatedMatch.subject),
                          )}
                          isCompact={isCompact}
                          onClick={() => setSelected({ lane: laneIdx, stop: stopIdx })}
                        />
                        {stopIdx < lane.stops.length - 1 && (
                          <div className="subway-lane__segment" style={{ background: lane.color }} />
                        )}
                      </React.Fragment>
                    ))}
                    {lane.stops.length < maxStops &&
                      Array.from({ length: maxStops - lane.stops.length }).map((_, i) => (
                        <div className="subway-lane__spacer" key={`pad-${i}`} />
                      ))}
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* ═══ Detail Drawer (right side) ═══ */}
        <div className={`hp-detail ${selectedStop ? 'hp-detail--open' : ''}`}>
        {selectedStop && selectedLane ? (
          <>
            <button type="button" className="hp-detail__close" onClick={() => setSelected(null)} aria-label="Close panel">
              <FaTimes />
            </button>
            <div className="hp-detail__header">
              <div className="hp-detail__lane-indicator" style={{ background: selectedLane.color }} />
              <div className="hp-detail__header-content">
                <div className="hp-detail__badges-row">
                  <span className={`hp-detail__epoch-badge ${isHistoricEntry ? 'hp-detail__epoch-badge--historic' : 'hp-detail__epoch-badge--live'}`}>
                    {isHistoricEntry ? 'HISTORIC SNAPSHOT' : 'CURRENT STATE'}
                  </span>
                  <div className={`hp-detail__status-badge hp-detail__status-badge--${selectedStop.health}`}>
                    {healthIcon[selectedStop.health]}
                    <span>{HEALTH_LABELS[selectedStop.health]}</span>
                  </div>
                  {selectedStop.entryType !== 'normal' && (
                    <span className={`hp-detail__entry-type-badge hp-detail__entry-type-badge--${selectedStop.entryType}`}>
                      {selectedStop.entryType === 'noop' ? <><FaBan /> No-op</> : <><FaExclamationTriangle /> Interrupted</>}
                    </span>
                  )}
                </div>
                <h2 className="hp-detail__title">{selectedStop.subject}</h2>
                <div className="hp-detail__meta">
                  <span className="hp-detail__branch-tag" style={{ background: `${selectedLane.color}18`, color: selectedLane.color, border: `1px solid ${selectedLane.color}44` }}>
                    {selectedLane.branch}
                  </span>
                  <span className="hp-detail__author">{selectedStop.author}</span>
                  <span className="hp-detail__separator">&middot;</span>
                  {selectedStop.commitUrl ? (
                    <a href={selectedStop.commitUrl} className="hp-detail__sha" target="_blank" rel="noreferrer">{selectedStop.shortSha}</a>
                  ) : (
                    <span className="hp-detail__sha">{selectedStop.shortSha}</span>
                  )}
                  {selectedStop.codeSha && (
                    <>
                      <span className="hp-detail__separator">/</span>
                      <span className="hp-detail__sha hp-detail__sha--code">{selectedStop.codeSha}</span>
                    </>
                  )}
                  {selectedStop.prUrl && selectedStop.prId && (
                    <>
                      <span className="hp-detail__separator">&middot;</span>
                      <a href={selectedStop.prUrl} className="hp-detail__pr-link" target="_blank" rel="noreferrer">
                        <GoGitPullRequest /> PR #{selectedStop.prId}
                      </a>
                    </>
                  )}
                  <span className="hp-detail__separator">&middot;</span>
                  <span className="hp-detail__date">
                    {selectedStop.index === 0 ? 'Current' : selectedStop.dateLabel}
                    {selectedStop.index !== 0 && selectedStop.timeAgoLabel && (
                      <span className="hp-detail__time-ago"> ({selectedStop.timeAgoLabel})</span>
                    )}
                  </span>
                </div>
                {isHistoricEntry && (
                  <button
                    type="button"
                    className="hp-rollback-btn"
                    onClick={() => setShowRollbackInfo(true)}
                    title="Rollback flow coming soon"
                  >
                    <FaUndoAlt />
                    Want to rollback?
                  </button>
                )}
              </div>
            </div>

            {/* Seen In + State Change */}
            {(seenIn.length > 1 || stateTransition) && (
              <div className="hp-journey">
                {seenIn.length > 1 && (
                  <div className="hp-journey__flow">
                    <span className="hp-journey__label">Seen In</span>
                    <div className="hp-journey__hops">
                      {seenIn.map((hit, i) => (
                        <React.Fragment key={hit.branch}>
                          {i > 0 && (
                            <div className="hp-journey__arrow">
                              <svg width="20" height="12" viewBox="0 0 20 12" fill="none">
                                <path d="M1 6h16m-4-4l4 4-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                              </svg>
                            </div>
                          )}
                          <button
                            type="button"
                            className={`hp-journey__hop ${hit.laneIdx === selected!.lane && hit.stopIdx === selected!.stop ? 'hp-journey__hop--current' : ''}`}
                            style={{ borderColor: hit.color }}
                            onClick={() => handleSeenInClick(hit.laneIdx, hit.stopIdx)}
                          >
                            <div className={`hp-journey__hop-dot hp-journey__hop-dot--${hit.health}`}>
                              {healthIcon[hit.health]}
                            </div>
                            <div className="hp-journey__hop-info">
                              <span className="hp-journey__hop-branch">{hit.branch}</span>
                              <span className="hp-journey__hop-time">{hit.timeLabel}</span>
                            </div>
                          </button>
                        </React.Fragment>
                      ))}
                    </div>
                  </div>
                )}

                {stateTransition && (
                  <div className="hp-journey__transition">
                    <span className="hp-journey__label">State Change</span>
                    <div className="hp-journey__state-change">
                      <span className={`hp-journey__state hp-journey__state--${stateTransition.from}`}>
                        {healthIcon[stateTransition.from]}
                        {HEALTH_LABELS[stateTransition.from]}
                      </span>
                      <svg className="hp-journey__state-arrow" width="24" height="12" viewBox="0 0 24 12" fill="none">
                        <path d="M1 6h20m-4-4l4 4-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                      </svg>
                      <span className={`hp-journey__state hp-journey__state--${stateTransition.to}`}>
                        {healthIcon[stateTransition.to]}
                        {HEALTH_LABELS[stateTransition.to]}
                      </span>
                    </div>
                  </div>
                )}
              </div>
            )}

            {/* Rollback info dialog */}
            {showRollbackInfo && selectedStop && (
              <>
                <div className="hp-rollback-backdrop" onClick={() => setShowRollbackInfo(false)} />
                <div className="hp-rollback-dialog">
                  <h3>Rollback Information</h3>
                  <p>Rolling back <strong>{selectedLane.branch}</strong> to this version would involve:</p>
                  <ul>
                    <li>Reverting to deployment SHA <code>{selectedStop.shortSha}</code></li>
                    {selectedStop.codeSha && <li>Source code SHA <code>{selectedStop.codeSha}</code></li>}
                    <li>Creating a revert PR against the active branch</li>
                    <li>Re-running commit status checks</li>
                  </ul>
                  <p className="hp-rollback-dialog__note">This feature is not yet available. Rollback flow coming soon.</p>
                  <button type="button" className="hp-rollback-dialog__close" onClick={() => setShowRollbackInfo(false)}>Close</button>
                </div>
              </>
            )}

            <DetailPanel
              environment={selectedLane.environment}
              historyIndex={selected!.stop}
            />
          </>
        ) : (
          <div className="hp-detail__prompt">
            <p>Select a stop on any timeline to view details.</p>
          </div>
        )}
      </div>
      </div>{/* end hp-body */}
    </div>
  );
};

export default HistoryPage;
