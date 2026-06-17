import React, { useEffect, useMemo, useState, useCallback, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate, useParams } from 'react-router-dom';
import {
  FaCheckCircle,
  FaTimesCircle,
  FaSpinner,
  FaQuestionCircle,
  FaChevronLeft,
  FaTimes,
  FaBan,
  FaExclamationTriangle,
  FaArrowRight,
} from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import {
  timeAgo,
  formatDate,
  extractNameOnly,
  getCommitUrl,
  extractBodyPreTrailer,
} from '@shared/utils/util';
import type {
  Commit,
  CommitStatus,
  Environment,
  History,
  PromotionStrategy,
  PullRequest,
} from '@shared/types/promotion';
import './HistoryPage.scss';

/* ═════════════════════════════════════════════════════════════════
   Data model
   ═════════════════════════════════════════════════════════════════ */

type HealthKey = 'success' | 'failure' | 'pending' | 'unknown';

type CellKind =
  | 'live'
  | 'in-flight'
  | 'was-here'
  | 'failed'
  | 'no-op'
  | 'not-reached';

interface CellState {
  kind: CellKind;
  commit?: Commit;
  commitStatuses: CommitStatus[];
  health: HealthKey;
  pullRequest?: PullRequest;
  /** True when in-flight cell originates from the env's `proposed` block. */
  isProposed?: boolean;
  /** For no-op cells: human-readable explanation. */
  noopNote?: string;
  /** For was-here cells: the commit row id that replaced this one. */
  supersededById?: string;
  /** Timestamp this cell represents (for sort). */
  at?: string;
}

interface EnvColumn {
  branch: string;
  autoMerge: boolean;
  /** Env identity color. Assigned positionally from `LANE_COLORS`. */
  color: string;
  /** Current live commit (env.active.dry). */
  liveCommit?: Commit;
  liveStatuses: CommitStatus[];
  liveHealth: HealthKey;
  /** Current proposed (in-flight) commit, if distinct from live. */
  proposedCommit?: Commit;
  proposedStatuses: CommitStatus[];
  proposedHealth: HealthKey;
  proposedPR?: PullRequest;
}

/**
 * Env identity palette — same palette the previous subway design used, so
 * users carrying mental color associations from before don't have to relearn.
 * Order matters: assigned positionally to envs as they appear in the spec.
 */
// Env identity palette. Every hue meets WCAG AA (>=4.5:1 on white) because
// these colors are applied to text (branch labels, SHA pills), not just
// accents. #d97706 (3.2:1) was darkened to #9a5a0f (5.5:1 on white).
const LANE_COLORS = ['#1f4f5e', '#6f42c1', '#9a5a0f', '#0f766e', '#b91c1c', '#4338ca'];

interface CommitRow {
  id: string;
  dryShaFull: string;
  dryShaShort: string;
  subject: string;
  author: string;
  body?: string;
  prId?: string;
  prUrl?: string;
  repoUrl: string;
  /** ms; most-recent cell time across all envs — used for default sort. */
  freshestAt: number;
  /** ms; earliest cell time across all envs — used as "introduced" label. */
  earliestAt: number;
  cells: Record<string /* env.branch */, CellState>;
  hasLive: boolean;
  hasInFlight: boolean;
  hasFailed: boolean;
  hasNoop: boolean;
  /** Appears in first env only — never propagated downstream. */
  isStuckUpstream: boolean;
}

/* ─── small helpers ───────────────────────────────────────────────── */

function healthFromStatuses(statuses: CommitStatus[] | undefined): HealthKey {
  if (!statuses || statuses.length === 0) return 'unknown';
  if (statuses.some((s) => s.phase === 'failure')) return 'failure';
  if (statuses.some((s) => s.phase === 'pending')) return 'pending';
  if (statuses.every((s) => s.phase === 'success')) return 'success';
  return 'unknown';
}

function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : '';
}

function commitKey(c: Commit | undefined): string | null {
  const s = c?.sha;
  if (s && s.length >= 7) return s.slice(0, 7);
  // Fallback when sha is missing: subject+author so we still group sensibly.
  if (c?.subject || c?.author) return `nokey:${c?.subject ?? ''}|${c?.author ?? ''}`;
  return null;
}

function commitTimeMs(c: Commit | undefined): number {
  const t = c?.commitTime ? new Date(c.commitTime).getTime() : NaN;
  return Number.isFinite(t) ? t : 0;
}

/* ═════════════════════════════════════════════════════════════════
   Build the commit-first matrix
   ═════════════════════════════════════════════════════════════════ */

function buildMatrix(strategy: PromotionStrategy): {
  envs: EnvColumn[];
  rows: CommitRow[];
} {
  const envs = (strategy.status?.environments ?? []).filter(
    (e) => (e.history?.length ?? 0) > 0 || e.active?.dry,
  );

  const specByBranch = new Map<string, { autoMerge?: boolean }>();
  for (const e of strategy.spec.environments ?? []) specByBranch.set(e.branch, e);

  const envColumns: EnvColumn[] = envs.map((env, i) => {
    const liveStatuses = env.active?.commitStatuses ?? [];
    const proposedStatuses = env.proposed?.commitStatuses ?? [];
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedDistinct =
      env.proposed?.dry && proposedSha && proposedSha !== liveSha
        ? env.proposed.dry
        : undefined;

    return {
      branch: env.branch,
      autoMerge: specByBranch.get(env.branch)?.autoMerge ?? false,
      color: LANE_COLORS[i % LANE_COLORS.length]!,
      liveCommit: env.active?.dry,
      liveStatuses,
      liveHealth: healthFromStatuses(liveStatuses),
      proposedCommit: proposedDistinct,
      proposedStatuses: proposedDistinct ? proposedStatuses : [],
      proposedHealth: proposedDistinct
        ? healthFromStatuses(proposedStatuses)
        : 'unknown',
      proposedPR: proposedDistinct ? env.pullRequest : undefined,
    };
  });

  // rowsById holds the merged matrix; we'll fill cells as we walk envs.
  const rowsById = new Map<string, CommitRow>();

  /** Look up or insert a row for this commit. */
  const getRow = (
    c: Commit | undefined,
    repoUrlFallback: string,
    pr?: PullRequest,
  ): CommitRow | null => {
    const key = commitKey(c);
    if (!key || !c) return null;
    let row = rowsById.get(key);
    if (!row) {
      row = {
        id: key,
        dryShaFull: c.sha ?? '',
        dryShaShort: shortSha(c.sha),
        subject: (c.subject ?? '').trim() || '(no subject)',
        author: c.author ? extractNameOnly(c.author) : '—',
        body: c.body ? extractBodyPreTrailer(c.body) : undefined,
        prId: pr?.id,
        prUrl: pr?.url,
        repoUrl: c.repoURL ?? repoUrlFallback,
        freshestAt: 0,
        earliestAt: 0,
        cells: {},
        hasLive: false,
        hasInFlight: false,
        hasFailed: false,
        hasNoop: false,
        isStuckUpstream: false,
      };
      rowsById.set(key, row);
    }
    if (pr?.id && !row.prId) {
      row.prId = pr.id;
      row.prUrl = pr.url;
    }
    return row;
  };

  /** Insert / upgrade a cell. Later cells overwrite when they have stronger
   *  semantics (live > in-flight > failed > was-here > no-op). */
  const cellRank: Record<CellKind, number> = {
    live: 6,
    'in-flight': 5,
    failed: 4,
    'was-here': 3,
    'no-op': 2,
    'not-reached': 1,
  };
  const setCell = (row: CommitRow, branch: string, next: CellState) => {
    const prev = row.cells[branch];
    if (!prev || cellRank[next.kind] >= cellRank[prev.kind]) {
      row.cells[branch] = next;
    }
  };

  envs.forEach((env) => {
    const branch = env.branch;
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedIsDistinct = !!proposedSha && proposedSha !== liveSha;

    // 1) Live cell
    if (env.active?.dry) {
      const statuses = env.active.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(env.active.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'live';
        setCell(row, branch, {
          kind,
          commit: env.active.dry,
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          at: env.active.dry.commitTime ?? undefined,
        });
      }
    }

    // 2) In-flight cell (distinct proposed)
    if (proposedIsDistinct && env.proposed?.dry) {
      const statuses = env.proposed.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(env.proposed.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'in-flight';
        setCell(row, branch, {
          kind,
          commit: env.proposed.dry,
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          isProposed: true,
          at: env.proposed.dry.commitTime ?? undefined,
        });
      }
    }

    // 3) History cells — walk newest → oldest. Detect no-op by comparing each
    // entry's dry SHA against the next-older entry's dry SHA (same heuristic
    // used in the previous design).
    const history = env.history ?? [];
    history.forEach((entry, idx) => {
      const commit = entry.active?.dry;
      if (!commit) return;
      const statuses = entry.active?.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const olderSha = history[idx + 1]?.active?.dry?.sha;
      const isNoop =
        !!commit.sha && !!olderSha && commit.sha === olderSha;
      let kind: CellKind = isNoop
        ? 'no-op'
        : health === 'failure'
          ? 'failed'
          : 'was-here';

      const row = getRow(commit, '', entry.pullRequest);
      if (!row) return;

      const supersededById = idx > 0 ? commitKey(history[idx - 1]?.active?.dry) ?? undefined : undefined;
      setCell(row, branch, {
        kind,
        commit,
        commitStatuses: statuses,
        health,
        pullRequest: entry.pullRequest,
        noopNote: isNoop ? `Same dry SHA as the previous entry, so ${branch} didn't change.` : undefined,
        supersededById,
        at: commit.commitTime ?? entry.pullRequest?.prMergeTime ?? undefined,
      });
    });
  });

  // Finalize rollups + timestamps
  const rows = Array.from(rowsById.values()).map((row) => {
    const times: number[] = [];
    for (const branch of envs.map((e) => e.branch)) {
      const c = row.cells[branch];
      if (c?.at) {
        const t = new Date(c.at).getTime();
        if (Number.isFinite(t)) times.push(t);
      }
      if (c?.commit?.commitTime) {
        const t = new Date(c.commit.commitTime).getTime();
        if (Number.isFinite(t)) times.push(t);
      }
    }
    if (times.length === 0 && row.dryShaFull) {
      // last-ditch: hold sort stable by pushing missing-time rows to the bottom
      times.push(0);
    }
    row.freshestAt = times.length ? Math.max(...times) : 0;
    row.earliestAt = times.length ? Math.min(...times.filter((t) => t > 0), ...(times.includes(0) ? [Infinity] : [])) : 0;
    if (!Number.isFinite(row.earliestAt)) row.earliestAt = row.freshestAt;

    // Fill missing cells as 'not-reached'
    for (const e of envs) {
      if (!row.cells[e.branch]) {
        row.cells[e.branch] = {
          kind: 'not-reached',
          commitStatuses: [],
          health: 'unknown',
        };
      }
    }

    const branches = envs.map((e) => e.branch);
    const states = branches.map((b) => row.cells[b].kind);
    row.hasLive = states.includes('live');
    row.hasInFlight = states.includes('in-flight');
    row.hasFailed = states.includes('failed');
    row.hasNoop = states.includes('no-op');
    // "Stuck upstream" calls out a real problem: a change that landed in the
    // first env, isn't moving anymore, and never reached the rest of the
    // pipeline. We deliberately exclude:
    //  • `live` — current head in the first env; promotion may simply not
    //    have happened yet, so calling it stuck is alarmist.
    //  • `in-flight` — already on its way, just not arrived.
    //  • `no-op` — by definition didn't change anything.
    // Stuck only makes sense in a multi-env pipeline: there must be a
    // downstream env that the commit failed to reach.
    if (branches.length < 2) {
      row.isStuckUpstream = false;
    } else {
      const firstBranch = branches[0];
      const firstCellKind = row.cells[firstBranch].kind;
      const stuckEligibleKinds: CellKind[] = ['was-here', 'failed'];
      const appearsInFirstAsStuck = stuckEligibleKinds.includes(firstCellKind);
      const appearsLater = branches.slice(1).some(
        (b) => row.cells[b].kind !== 'not-reached',
      );
      row.isStuckUpstream = appearsInFirstAsStuck && !appearsLater;
    }

    return row;
  });

  // Default sort: newest first
  rows.sort((a, b) => b.freshestAt - a.freshestAt);

  return { envs: envColumns, rows };
}

/* ═════════════════════════════════════════════════════════════════
   Visual atoms
   ═════════════════════════════════════════════════════════════════ */

const HEALTH_LABELS: Record<HealthKey, string> = {
  success: 'Healthy',
  failure: 'Failed',
  pending: 'Pending',
  unknown: 'Unknown',
};

/**
 * Portal-based tooltip. Renders the bubble into document.body so it escapes the
 * matrix cells' `overflow: hidden` (a CSS-only tooltip would be clipped). Shows
 * instantly on hover/focus; positioned above the trigger, flipping below when
 * there isn't room. Accessible via keyboard focus.
 */
const Tooltip: React.FC<{
  label: React.ReactNode;
  children: React.ReactElement;
}> = ({ label, children }) => {
  const triggerRef = useRef<HTMLElement | null>(null);
  const [coords, setCoords] = useState<{ x: number; y: number; below: boolean } | null>(null);

  const show = useCallback(() => {
    const el = triggerRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    // Prefer above; flip below if the trigger is near the top of the viewport.
    const below = r.top < 64;
    setCoords({
      x: r.left + r.width / 2,
      y: below ? r.bottom + 8 : r.top - 8,
      below,
    });
  }, []);

  const hide = useCallback(() => setCoords(null), []);

  // Clone the child to attach ref + handlers directly — no wrapper element, so
  // the trigger keeps its exact place in the surrounding flex layout.
  const child = children as React.ReactElement<Record<string, unknown>>;
  const childProps = child.props;
  const trigger = React.cloneElement(child, {
    ref: (node: HTMLElement | null) => {
      triggerRef.current = node;
      const r = (child as unknown as { ref?: unknown }).ref;
      if (typeof r === 'function') (r as (n: HTMLElement | null) => void)(node);
      else if (r && typeof r === 'object') (r as { current: unknown }).current = node;
    },
    onMouseEnter: (e: React.MouseEvent) => {
      show();
      (childProps.onMouseEnter as ((e: React.MouseEvent) => void) | undefined)?.(e);
    },
    onMouseLeave: (e: React.MouseEvent) => {
      hide();
      (childProps.onMouseLeave as ((e: React.MouseEvent) => void) | undefined)?.(e);
    },
    onFocus: (e: React.FocusEvent) => {
      show();
      (childProps.onFocus as ((e: React.FocusEvent) => void) | undefined)?.(e);
    },
    onBlur: (e: React.FocusEvent) => {
      hide();
      (childProps.onBlur as ((e: React.FocusEvent) => void) | undefined)?.(e);
    },
  } as Record<string, unknown>);

  return (
    <>
      {trigger}
      {coords &&
        createPortal(
          <span
            role="tooltip"
            className={`hp-tip ${coords.below ? 'hp-tip--below' : 'hp-tip--above'}`}
            style={{ left: coords.x, top: coords.y }}
          >
            {label}
          </span>,
          document.body,
        )}
    </>
  );
};

/** Plain-language explanation of what a cell's status means in a given env,
 *  shown as the hover tooltip on the status pill. */
function cellPillTooltip(cell: CellState, branch: string): string {
  switch (cell.kind) {
    case 'live':
      return `Currently live in ${branch}`;
    case 'in-flight':
      return cell.isProposed
        ? `Proposed for ${branch} — promotion pending`
        : `Open promotion PR into ${branch}`;
    case 'was-here':
      return `Was live in ${branch}, since replaced by a newer commit`;
    case 'failed':
      return `Checks failed in ${branch}`;
    case 'no-op':
      return cell.noopNote || `No change in ${branch}`;
    case 'not-reached':
      return `Never reached ${branch}`;
    default:
      return '';
  }
}

/** One-line health status for the live strip: the health word plus the most
 *  relevant check count (failures > pending > passing), e.g. "Healthy · 2 passing"
 *  or "Failed · 1 failing". Falls back to just the health word when no checks. */
function healthSummary(health: HealthKey, statuses: CommitStatus[]): string {
  const label = HEALTH_LABELS[health];
  if (statuses.length === 0) return label;
  let pass = 0, fail = 0, pend = 0;
  for (const s of statuses) {
    if (s.phase === 'success') pass++;
    else if (s.phase === 'failure') fail++;
    else if (s.phase === 'pending') pend++;
  }
  if (fail > 0) return `${label} · ${fail} failing`;
  if (pend > 0) return `${label} · ${pend} running`;
  if (pass > 0) return `${label} · ${pass} passing`;
  return label;
}

// Human-readable status words for screen readers, mirroring the visible pills.
const CELL_KIND_LABELS: Record<CellKind, string> = {
  live: 'Live',
  'in-flight': 'In flight',
  'was-here': 'Was here',
  failed: 'Failed',
  'no-op': 'No-op',
  'not-reached': 'Not reached',
};

/* Detail drawer resize bounds (px) and the localStorage key for persistence. */
const DRAWER_MIN_WIDTH = 320;
const DRAWER_MAX_WIDTH = 760;
const DRAWER_DEFAULT_WIDTH = 420;
const DRAWER_WIDTH_KEY = 'hp-drawer-width';

const healthIcon: Record<HealthKey, React.ReactNode> = {
  success: <FaCheckCircle aria-hidden="true" />,
  failure: <FaTimesCircle aria-hidden="true" />,
  pending: <FaSpinner className="fa-spin" aria-hidden="true" />,
  unknown: <FaQuestionCircle aria-hidden="true" />,
};

function ChecksDots({ statuses }: { statuses: CommitStatus[] }) {
  if (statuses.length === 0) return null;
  let pass = 0, fail = 0, pend = 0;
  for (const s of statuses) {
    if (s.phase === 'success') pass++;
    else if (s.phase === 'failure') fail++;
    else if (s.phase === 'pending') pend++;
  }
  return (
    <span className="checks-dots">
      {pass > 0 && (
        <span className="checks-dots__item checks-dots__item--pass" title={`${pass} passing`}>
          <span className="checks-dots__dot" aria-hidden="true" /> {pass}
          <span className="hp-sr-only"> passing</span>
        </span>
      )}
      {fail > 0 && (
        <span className="checks-dots__item checks-dots__item--fail" title={`${fail} failing`}>
          <span className="checks-dots__dot" aria-hidden="true" /> {fail}
          <span className="hp-sr-only"> failing</span>
        </span>
      )}
      {pend > 0 && (
        <span className="checks-dots__item checks-dots__item--pend" title={`${pend} pending`}>
          <span className="checks-dots__dot" aria-hidden="true" /> {pend}
          <span className="hp-sr-only"> pending</span>
        </span>
      )}
    </span>
  );
}

/* ─── one matrix cell ─────────────────────────────────────────────── */

const FlowCell: React.FC<{
  cell: CellState;
  branch: string;
  isSelected: boolean;
  onSelect?: () => void;
  rowsById: Map<string, CommitRow>;
  onJumpToRow?: (rowId: string) => void;
}> = ({ cell, branch, isSelected, onSelect, rowsById, onJumpToRow }) => {
  if (cell.kind === 'not-reached') {
    return <div className="cell cell--not-reached" aria-label={`Not reached in ${branch}`}>Not reached</div>;
  }

  const failingChecks = cell.commitStatuses
    .filter((s) => s.phase === 'failure')
    .map((s) => s.description ? `${s.key}: ${s.description}` : s.key);

  const time = cell.at ? timeAgo(cell.at) : '';
  const exact = cell.at ? formatDate(cell.at) : '';

  // Each cell shows only what is specific to *this env*: the promotion PR.
  // The commit identity (subject, SHA, author) is identical for every reached
  // cell in the row — it's the same commit promoted across envs — so it lives
  // once in the row label rather than being repeated here.
  const rowForCell = cell.commit ? rowsById.get(commitKey(cell.commit) ?? '') : undefined;
  const prId = cell.pullRequest?.id ?? rowForCell?.prId;
  const prUrl = cell.pullRequest?.url ?? rowForCell?.prUrl;

  return (
    <button
      type="button"
      className={[
        'cell',
        `cell--${cell.kind}`,
        isSelected ? 'cell--selected' : '',
      ].filter(Boolean).join(' ')}
      onClick={onSelect}
      aria-label={`${
        cell.kind === 'in-flight'
          ? cell.isProposed
            ? 'Proposed'
            : 'PR open'
          : CELL_KIND_LABELS[cell.kind]
      } in ${branch}`}
      title={exact}
    >
      <div className="cell__top">
        <Tooltip label={cellPillTooltip(cell, branch)}>
          <span className={`cell__pill cell__pill--${cell.kind}`}>
            {cell.kind === 'live' && 'LIVE'}
            {cell.kind === 'in-flight' && (cell.isProposed ? 'PROPOSED' : 'PR OPEN')}
            {cell.kind === 'was-here' && 'WAS HERE'}
            {cell.kind === 'failed' && 'FAILED'}
            {cell.kind === 'no-op' && (
              <>
                <FaBan aria-hidden="true" /> NO-OP
              </>
            )}
          </span>
        </Tooltip>
        {time && <span className="cell__time">{time}</span>}
      </div>

      {prId && (
        <div className="cell__commit">
          <div className="cell__commit-meta">
            <Tooltip label={<>Promotion PR into {branch}: #{prId}<br />Open on remote</>}>
              <a
                className="cell__pr"
                href={prUrl}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => e.stopPropagation()}
                aria-label={`Promotion pull request #${prId} into ${branch}, opens in new tab`}
              >
                <GoGitPullRequest aria-hidden="true" /> #{prId}
              </a>
            </Tooltip>
          </div>
        </div>
      )}

      <div className="cell__bottom">
        {cell.kind === 'failed' && failingChecks.length > 0 && (
          <span className="cell__reason">{failingChecks[0]}</span>
        )}
        {cell.kind === 'no-op' && <span className="cell__reason cell__reason--muted">{cell.noopNote}</span>}
        {cell.kind === 'was-here' && cell.supersededById && onJumpToRow && rowsById.get(cell.supersededById) && (
          <div className="cell__bottom-row">
            <button
              type="button"
              className="cell__superseded"
              onClick={(e) => {
                e.stopPropagation();
                onJumpToRow(cell.supersededById!);
              }}
              title={`Replaced by ${rowsById.get(cell.supersededById)!.subject}`}
            >
              <FaArrowRight aria-hidden="true" /> Replaced
            </button>
          </div>
        )}
      </div>
    </button>
  );
};

/* ─── Live pipeline header strip ──────────────────────────────────── */

const LivePipelineStrip: React.FC<{
  envs: EnvColumn[];
}> = ({ envs }) => {
  return (
    <div className="live-strip" aria-label="Live pipeline status">
      {envs.map((env, i) => {
        const live = env.liveCommit;

        // Connector to next env
        const next = envs[i + 1];
        let connectorLabel = '';
        let connectorTone: 'idle' | 'promoting' | 'in-sync' = 'idle';
        let connectorPR: PullRequest | undefined;
        if (next) {
          if (
            next.proposedCommit?.sha &&
            next.proposedCommit.sha === env.liveCommit?.sha
          ) {
            connectorTone = 'promoting';
            connectorLabel = 'Promoting';
            connectorPR = next.proposedPR;
          } else if (
            next.liveCommit?.sha &&
            env.liveCommit?.sha &&
            next.liveCommit.sha === env.liveCommit.sha
          ) {
            connectorTone = 'in-sync';
            connectorLabel = 'In sync';
          } else {
            connectorTone = 'idle';
            connectorLabel = '';
          }
        }

        return (
          <React.Fragment key={env.branch}>
            <div
              className="live-station"
              style={{
                // Env-keyed accent: top stripe + faint tinted surface, applied
                // by CSS via the `--env-color` custom property.
                ['--env-color' as string]: env.color,
              }}
            >
              <div className="live-station__accent" />
              <div className="live-station__head">
                <span className="live-station__branch" style={{ color: env.color }}>
                  {env.branch}
                </span>
                <span
                  className={`live-station__pill live-station__pill--${env.liveHealth}`}
                  title={healthSummary(env.liveHealth, env.liveStatuses)}
                >
                  {healthIcon[env.liveHealth]} {HEALTH_LABELS[env.liveHealth]}
                </span>
              </div>

              {live ? (
                <div className="live-station__subject-row">
                  <span className="live-station__live-tag">LIVE</span>
                  <div className="live-station__subject" title={live.subject}>
                    {live.subject || '(no subject)'}
                  </div>
                </div>
              ) : (
                <div className="live-station__empty">No live commit</div>
              )}
            </div>

            {next && (
              <div className={`live-connector live-connector--${connectorTone}`}>
                <div className="live-connector__line" />
                <div className="live-connector__label">
                  {connectorTone === 'promoting' && (
                    <>
                      <FaArrowRight aria-hidden="true" /> {connectorLabel}
                      {connectorPR?.id && (
                        <a
                          href={connectorPR.url}
                          target="_blank"
                          rel="noreferrer"
                          className="live-connector__pr"
                          onClick={(e) => e.stopPropagation()}
                          aria-label={`Promotion pull request #${connectorPR.id}, opens in new tab`}
                        >
                          <GoGitPullRequest aria-hidden="true" /> #{connectorPR.id}
                        </a>
                      )}
                    </>
                  )}
                  {connectorTone === 'in-sync' && (
                    <>
                      <FaCheckCircle aria-hidden="true" /> {connectorLabel}
                    </>
                  )}
                </div>
              </div>
            )}
          </React.Fragment>
        );
      })}
    </div>
  );
};

/* ═════════════════════════════════════════════════════════════════
   Main page
   ═════════════════════════════════════════════════════════════════ */

type FilterId = 'all' | 'live' | 'in-flight' | 'failed' | 'stuck' | 'no-op';
type SortId = 'newest' | 'oldest' | 'stuck-first';

const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigate();
  const { items, fetchItems } = PromotionStrategyStore();

  useEffect(() => {
    if (namespace) fetchItems(namespace);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  const strategy = items.find((ps) => ps.metadata?.name === name);
  const { envs, rows } = useMemo(
    () => (strategy ? buildMatrix(strategy) : { envs: [], rows: [] }),
    [strategy],
  );
  const rowsById = useMemo(() => {
    const m = new Map<string, CommitRow>();
    for (const r of rows) m.set(r.id, r);
    return m;
  }, [rows]);

  const [filter, setFilter] = useState<FilterId>('all');
  const [sort, setSort] = useState<SortId>('newest');
  const [envFilter, setEnvFilter] = useState<string | null>(null);
  const [selected, setSelected] = useState<{ rowId: string; branch: string } | null>(null);

  /** User-resizable detail drawer. Width persists across sessions and is
   *  clamped so the drawer can't swallow or vanish from the viewport. */
  const [drawerWidth, setDrawerWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(DRAWER_WIDTH_KEY));
    return saved >= DRAWER_MIN_WIDTH && saved <= DRAWER_MAX_WIDTH ? saved : DRAWER_DEFAULT_WIDTH;
  });
  const [isResizingDrawer, setIsResizingDrawer] = useState(false);

  useEffect(() => {
    localStorage.setItem(DRAWER_WIDTH_KEY, String(drawerWidth));
  }, [drawerWidth]);

  /** Begin dragging the drawer's left edge. We track from the pointer's start
   *  X and the width at grab time, then resize live until pointerup. */
  const handleDrawerResizeStart = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startWidth = drawerWidth;
      setIsResizingDrawer(true);

      const onMove = (ev: PointerEvent) => {
        // Drawer is anchored right, so dragging left (smaller clientX) widens it.
        const delta = startX - ev.clientX;
        const next = Math.min(
          DRAWER_MAX_WIDTH,
          Math.max(DRAWER_MIN_WIDTH, startWidth + delta),
        );
        setDrawerWidth(next);
      };
      const onUp = () => {
        setIsResizingDrawer(false);
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        document.body.style.userSelect = '';
        document.body.style.cursor = '';
      };
      // Suppress text selection / cursor flicker while dragging.
      document.body.style.userSelect = 'none';
      document.body.style.cursor = 'col-resize';
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
    },
    [drawerWidth],
  );

  /** Reset the drawer to its default width (double-click the resize handle). */
  const handleDrawerResizeReset = useCallback(() => {
    setDrawerWidth(DRAWER_DEFAULT_WIDTH);
  }, []);

  /** Set an absolute, clamped drawer width (used by keyboard resize). */
  const handleDrawerResizeTo = useCallback((next: number) => {
    setDrawerWidth(Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, next)));
  }, []);

  /** Toggle the env-scope filter from the Environment chips. Selecting the
   *  active env clears it; selecting another replaces it. */
  const handleToggleEnvFilter = useCallback((branch: string) => {
    setEnvFilter((prev) => (prev === branch ? null : branch));
  }, []);

  /** Env-scope filter applied first; status-scope filter applied second.
   *  Counts below respect the env filter so the chips stay truthful. */
  const envScopedRows = useMemo(
    () =>
      envFilter
        ? rows.filter((r) => r.cells[envFilter]?.kind !== 'not-reached')
        : rows,
    [rows, envFilter],
  );

  const filteredRows = useMemo(() => {
    const apply = (r: CommitRow): boolean => {
      switch (filter) {
        case 'all': return true;
        case 'live': return r.hasLive;
        case 'in-flight': return r.hasInFlight;
        case 'failed': return r.hasFailed;
        case 'no-op': return r.hasNoop;
        case 'stuck': return r.isStuckUpstream;
      }
    };
    const list = envScopedRows.filter(apply);
    if (sort === 'newest') return [...list].sort((a, b) => b.freshestAt - a.freshestAt);
    if (sort === 'oldest') return [...list].sort((a, b) => a.freshestAt - b.freshestAt);
    return [...list].sort((a, b) => {
      const sa = a.isStuckUpstream ? 0 : 1;
      const sb = b.isStuckUpstream ? 0 : 1;
      if (sa !== sb) return sa - sb;
      return b.freshestAt - a.freshestAt;
    });
  }, [envScopedRows, filter, sort]);

  const counts = useMemo(() => {
    return {
      all: envScopedRows.length,
      live: envScopedRows.filter((r) => r.hasLive).length,
      'in-flight': envScopedRows.filter((r) => r.hasInFlight).length,
      failed: envScopedRows.filter((r) => r.hasFailed).length,
      stuck: envScopedRows.filter((r) => r.isStuckUpstream).length,
      'no-op': envScopedRows.filter((r) => r.hasNoop).length,
    } satisfies Record<FilterId, number>;
  }, [envScopedRows]);

  const summary = useMemo(() => {
    return {
      live: rows.filter((r) => r.hasLive).length,
      inFlight: rows.filter((r) => r.hasInFlight).length,
      failed: rows.filter((r) => r.hasFailed).length,
      stuck: rows.filter((r) => r.isStuckUpstream).length,
    };
  }, [rows]);

  const handleBack = () => navigate(`/promotion-strategies/${namespace}/${name}`);

  const handleJumpToRow = useCallback((rowId: string) => {
    const row = rowsById.get(rowId);
    if (!row) return;
    // Prefer a "live" cell, otherwise the first non-not-reached cell.
    const branches = envs.map((e) => e.branch);
    const liveBranch = branches.find((b) => row.cells[b].kind === 'live');
    const branch = liveBranch ?? branches.find((b) => row.cells[b].kind !== 'not-reached');
    if (branch) setSelected({ rowId, branch });
    // scroll into view
    requestAnimationFrame(() => {
      document.getElementById(`row-${rowId}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    });
  }, [envs, rowsById]);

  if (!strategy) return <div className="hp-loading">Loading promotion history…</div>;

  if (envs.length === 0) {
    return (
      <div className="hp-empty">
        <h2 className="hp-empty__title">No promotion history yet</h2>
        <p className="hp-empty__body">
          This promotion strategy doesn't have any environments with history to show.
        </p>
        <button onClick={handleBack} className="hp-empty__back">Back to {name}</button>
      </div>
    );
  }

  const selectedRow = selected ? rowsById.get(selected.rowId) ?? null : null;
  const selectedCell = selectedRow && selected ? selectedRow.cells[selected.branch] : null;
  const hasMultipleEnvs = envs.length > 1;
  const stuckLabel = envs.length > 0 ? `Stuck in ${envs[0].branch}` : 'Stuck';

  const FILTERS: { id: FilterId; label: string }[] = [
    { id: 'all', label: 'All commits' },
    { id: 'live', label: 'Live' },
    { id: 'in-flight', label: 'In flight' },
    { id: 'failed', label: 'Failed' },
    ...(hasMultipleEnvs ? [{ id: 'stuck' as FilterId, label: stuckLabel }] : []),
    { id: 'no-op', label: 'No-op' },
  ];

  // CSS grid template: commit col + one col per env
  const gridTemplate = `minmax(280px, 1.4fr) repeat(${envs.length}, minmax(180px, 1fr))`;

  return (
    <div className="hp">
      <header className="hp-header">
        <button className="hp-header__back" onClick={handleBack} type="button">
          <FaChevronLeft aria-hidden="true" />
          <span>Back to {name}</span>
        </button>
        <div className="hp-header__title">
          <h1>{name}</h1>
          <span className="hp-header__subtitle">Promotion flow · {namespace}</span>
        </div>
        <div className="hp-header__spacer" />
        <div className="hp-header__stats">
          <span className="hp-stat hp-stat--success" title="Commits currently live in at least one env">
            <strong>{summary.live}</strong> live
          </span>
          <span className="hp-stat hp-stat--warning" title="Commits with an open promotion PR">
            <strong>{summary.inFlight}</strong> in flight
          </span>
          <span className="hp-stat hp-stat--danger" title="Commits that failed checks in some env">
            <strong>{summary.failed}</strong> failed
          </span>
          {envs.length > 1 && (
            <span className="hp-stat hp-stat--info" title={`Commits in ${envs[0]?.branch ?? 'first env'} that never reached later envs`}>
              <strong>{summary.stuck}</strong> stuck
            </span>
          )}
        </div>
      </header>

      <div className="hp-body">
        <div className="hp-main">
          <LivePipelineStrip envs={envs} />

          <div className="hp-controls">
            <div className="hp-controls__group">
              <span className="hp-controls__label">Filter</span>
              {FILTERS.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className={`hp-chip ${filter === f.id ? 'hp-chip--active' : ''}`}
                  onClick={() => setFilter(f.id)}
                  aria-pressed={filter === f.id}
                >
                  {f.label} <span className="hp-chip__count">{counts[f.id]}</span>
                </button>
              ))}
            </div>
            {hasMultipleEnvs && (
              <div className="hp-controls__group">
                <span className="hp-controls__label">Environment</span>
                {envs.map((env) => {
                  const isActive = envFilter === env.branch;
                  const envCount = rows.filter(
                    (r) => r.cells[env.branch]?.kind !== 'not-reached',
                  ).length;
                  return (
                    <button
                      key={env.branch}
                      type="button"
                      className={`hp-chip hp-chip--env ${isActive ? 'hp-chip--active' : ''}`}
                      onClick={() => handleToggleEnvFilter(env.branch)}
                      aria-pressed={isActive}
                    >
                      <span
                        className="hp-chip__dot"
                        style={isActive ? undefined : { background: env.color }}
                        aria-hidden="true"
                      />
                      {env.branch} <span className="hp-chip__count">{envCount}</span>
                    </button>
                  );
                })}
              </div>
            )}
            <div className="hp-controls__group">
              <span className="hp-controls__label">Sort</span>
              <button
                type="button"
                className={`hp-chip ${sort === 'newest' ? 'hp-chip--active' : ''}`}
                onClick={() => setSort('newest')}
                aria-pressed={sort === 'newest'}
              >
                Newest first
              </button>
              <button
                type="button"
                className={`hp-chip ${sort === 'oldest' ? 'hp-chip--active' : ''}`}
                onClick={() => setSort('oldest')}
                aria-pressed={sort === 'oldest'}
              >
                Oldest first
              </button>
              <button
                type="button"
                className={`hp-chip ${sort === 'stuck-first' ? 'hp-chip--active' : ''}`}
                onClick={() => setSort('stuck-first')}
                aria-pressed={sort === 'stuck-first'}
              >
                Stuck first
              </button>
            </div>
          </div>

          <div className="hp-sr-only" role="status" aria-live="polite">
            {`${filteredRows.length} ${filteredRows.length === 1 ? 'commit' : 'commits'} shown`}
          </div>

          <div className="hp-matrix">
            {envFilter && (() => {
              const env = envs.find((e) => e.branch === envFilter);
              if (!env) return null;
              return (
                <div
                  className="hp-env-banner"
                  style={{
                    background: `${env.color}10`,
                    borderColor: `${env.color}55`,
                    color: env.color,
                  }}
                >
                  <span className="hp-env-banner__dot" style={{ background: env.color }} />
                  <span className="hp-env-banner__text">
                    Showing only commits that touched <strong>{env.branch}</strong>
                    <span className="hp-env-banner__count">
                      {envScopedRows.length} {envScopedRows.length === 1 ? 'commit' : 'commits'}
                    </span>
                  </span>
                  <button
                    type="button"
                    className="hp-env-banner__clear"
                    onClick={() => setEnvFilter(null)}
                  >
                    Clear filter
                  </button>
                </div>
              );
            })()}
            <div
              className="hp-matrix__head"
              style={{ gridTemplateColumns: gridTemplate }}
            >
              <span className="hp-matrix__head-label">Commit</span>
              {envs.map((env) => (
                <span key={env.branch} className="hp-matrix__head-env">
                  <span className="hp-matrix__head-env-swatch" style={{ background: env.color }} />
                  <span className="hp-matrix__head-env-branch" style={{ color: env.color }}>
                    {env.branch}
                  </span>
                </span>
              ))}
            </div>

            {filteredRows.length === 0 ? (
              <div className="hp-matrix__empty">No commits match these filters. Try adjusting them.</div>
            ) : (
              filteredRows.map((row) => (
                <div
                  key={row.id}
                  id={`row-${row.id}`}
                  className={`hp-row ${selected?.rowId === row.id ? 'hp-row--selected' : ''}`}
                  style={{ gridTemplateColumns: gridTemplate }}
                >
                  <div className="hp-row__commit">
                    <div className="hp-row__subject" title={row.subject}>{row.subject}</div>
                    <div className="hp-row__meta">
                      {row.repoUrl && row.dryShaFull ? (
                        <Tooltip label={<>Commit <code>{row.dryShaFull}</code><br />Open on remote</>}>
                          <a
                            className="hp-row__sha"
                            href={getCommitUrl(row.repoUrl, row.dryShaFull)}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            aria-label={`Commit ${row.dryShaShort}, opens in new tab`}
                          >
                            {row.dryShaShort}
                          </a>
                        </Tooltip>
                      ) : (
                        <Tooltip label={<>Commit <code>{row.dryShaFull || row.dryShaShort}</code></>}>
                          <span className="hp-row__sha">{row.dryShaShort}</span>
                        </Tooltip>
                      )}
                      {row.prId && (
                        <Tooltip label={<>Pull request #{row.prId}<br />Open on remote</>}>
                          <a
                            className="hp-row__pr"
                            href={row.prUrl}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            aria-label={`Pull request #${row.prId}, opens in new tab`}
                          >
                            <GoGitPullRequest aria-hidden="true" /> #{row.prId}
                          </a>
                        </Tooltip>
                      )}
                      <span className="hp-row__sep" aria-hidden="true">·</span>
                      <Tooltip label={`Authored by ${row.author}`}>
                        <span className="hp-row__author-inline">{row.author}</span>
                      </Tooltip>
                      {row.freshestAt > 0 && (
                        <>
                          <span className="hp-row__sep">·</span>
                          <Tooltip
                            label={`Introduced ${formatDate(new Date(row.earliestAt || row.freshestAt).toISOString())}`}
                          >
                            <span className="hp-row__time-inline">
                              {timeAgo(new Date(row.earliestAt || row.freshestAt).toISOString())}
                            </span>
                          </Tooltip>
                        </>
                      )}
                      {row.isStuckUpstream && (
                        <Tooltip label="This commit never reached the downstream environments">
                          <span className="hp-row__flag hp-row__flag--stuck">
                            <FaExclamationTriangle aria-hidden="true" /> stuck
                          </span>
                        </Tooltip>
                      )}
                    </div>
                  </div>
                  {envs.map((env) => {
                    const isFocusedEnv = envFilter === env.branch;
                    const isDimmedEnv = envFilter !== null && envFilter !== env.branch;
                    return (
                      <div
                        key={env.branch}
                        className={[
                          'hp-row__env-slot',
                          isFocusedEnv ? 'hp-row__env-slot--focus' : '',
                          isDimmedEnv ? 'hp-row__env-slot--dim' : '',
                        ].filter(Boolean).join(' ')}
                      >
                        <FlowCell
                          cell={row.cells[env.branch]}
                          branch={env.branch}
                          isSelected={selected?.rowId === row.id && selected?.branch === env.branch}
                          onSelect={() => setSelected({ rowId: row.id, branch: env.branch })}
                          rowsById={rowsById}
                          onJumpToRow={handleJumpToRow}
                        />
                      </div>
                    );
                  })}
                </div>
              ))
            )}
          </div>
        </div>

        <DetailDrawer
          row={selectedRow}
          cell={selectedCell}
          branch={selected?.branch ?? null}
          envs={envs}
          rowsById={rowsById}
          width={drawerWidth}
          isResizing={isResizingDrawer}
          onResizeStart={handleDrawerResizeStart}
          onResizeReset={handleDrawerResizeReset}
          onResizeTo={handleDrawerResizeTo}
          onClose={() => setSelected(null)}
          onJumpToRow={handleJumpToRow}
        />
      </div>
    </div>
  );
};

/* ═════════════════════════════════════════════════════════════════
   Detail drawer
   ═════════════════════════════════════════════════════════════════ */

const DetailDrawer: React.FC<{
  row: CommitRow | null;
  cell: CellState | null;
  branch: string | null;
  envs: EnvColumn[];
  rowsById: Map<string, CommitRow>;
  width: number;
  isResizing: boolean;
  onResizeStart: (e: React.PointerEvent) => void;
  onResizeReset: () => void;
  onResizeTo: (width: number) => void;
  onClose: () => void;
  onJumpToRow: (id: string) => void;
}> = ({
  row,
  cell,
  branch,
  envs,
  rowsById,
  width,
  isResizing,
  onResizeStart,
  onResizeReset,
  onResizeTo,
  onClose,
  onJumpToRow,
}) => {
  if (!row || !cell || !branch) {
    return (
      <aside className="hp-drawer">
        <div className="hp-drawer__prompt">
          <p>Select a cell to see what happened to a commit in that environment.</p>
        </div>
      </aside>
    );
  }

  const exact = cell.at ? formatDate(cell.at) : '';
  const ago = cell.at ? timeAgo(cell.at) : '';
  const failingChecks = cell.commitStatuses.filter((s) => s.phase === 'failure');
  const passingChecks = cell.commitStatuses.filter((s) => s.phase === 'success');
  const pendingChecks = cell.commitStatuses.filter((s) => s.phase === 'pending');

  // Cross-env story: which other envs did this same commit reach?
  const presence = envs
    .map((e) => ({ env: e, cell: row.cells[e.branch] }))
    .filter((x) => x.cell.kind !== 'not-reached');

  // Find where it came from (closest upstream env where this commit was live/was-here)
  const envIdx = envs.findIndex((e) => e.branch === branch);
  const upstream = envs
    .slice(0, envIdx)
    .reverse()
    .find((e) => {
      const k = row.cells[e.branch].kind;
      return k === 'live' || k === 'was-here';
    });
  const downstreamNotReached = envs
    .slice(envIdx + 1)
    .filter((e) => row.cells[e.branch].kind === 'not-reached');

  return (
    <aside
      className={`hp-drawer hp-drawer--open ${isResizing ? 'hp-drawer--resizing' : ''}`}
      style={{ width, minWidth: width }}
    >
      <div
        className="hp-drawer__resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize details panel"
        aria-valuenow={Math.round(width)}
        aria-valuemin={DRAWER_MIN_WIDTH}
        aria-valuemax={DRAWER_MAX_WIDTH}
        tabIndex={0}
        onPointerDown={onResizeStart}
        onDoubleClick={onResizeReset}
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
            e.preventDefault();
            // Left arrow widens (panel is right-anchored), right arrow narrows.
            const step = e.shiftKey ? 48 : 16;
            const dir = e.key === 'ArrowLeft' ? 1 : -1;
            onResizeTo(
              Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, width + dir * step)),
            );
          } else if (e.key === 'Home') {
            e.preventDefault();
            onResizeReset();
          }
        }}
      >
        <span className="hp-drawer__resizer-grip" aria-hidden="true" />
      </div>
      <button type="button" className="hp-drawer__close" onClick={onClose} aria-label="Close details">
        <FaTimes aria-hidden="true" />
      </button>

      <div className="hp-drawer__scroll">
      <div className="hp-drawer__header">
        <div className="hp-drawer__badges">
          <span className={`hp-drawer__kind hp-drawer__kind--${cell.kind}`}>
            {cell.kind === 'live' && 'LIVE'}
            {cell.kind === 'in-flight' && (cell.isProposed ? 'PROPOSED' : 'PR OPEN')}
            {cell.kind === 'was-here' && 'WAS HERE'}
            {cell.kind === 'failed' && 'FAILED'}
            {cell.kind === 'no-op' && 'NO-OP'}
            {cell.kind === 'not-reached' && 'NOT REACHED'}
          </span>
          <span className="hp-drawer__branch">{branch}</span>
        </div>
        <h2 className="hp-drawer__subject">{row.subject}</h2>
        <div className="hp-drawer__meta">
          <span className="hp-drawer__author">{row.author}</span>
          <span className="hp-drawer__sep">·</span>
          {row.repoUrl && row.dryShaFull ? (
            <a
              href={getCommitUrl(row.repoUrl, row.dryShaFull)}
              target="_blank"
              rel="noreferrer"
              className="hp-drawer__sha"
              aria-label={`Commit ${row.dryShaShort}, opens in new tab`}
            >
              {row.dryShaShort}
            </a>
          ) : (
            <span className="hp-drawer__sha">{row.dryShaShort}</span>
          )}
          {row.prId && (
            <>
              <span className="hp-drawer__sep" aria-hidden="true">·</span>
              <a
                href={row.prUrl}
                target="_blank"
                rel="noreferrer"
                className="hp-drawer__pr"
                aria-label={`Pull request #${row.prId}, opens in new tab`}
              >
                <GoGitPullRequest aria-hidden="true" /> #{row.prId}
              </a>
            </>
          )}
          {ago && (
            <>
              <span className="hp-drawer__sep">·</span>
              <span title={exact}>{ago}</span>
            </>
          )}
        </div>
      </div>

      {cell.kind === 'failed' && failingChecks.length > 0 && (
        <div className="hp-drawer__section hp-drawer__section--danger">
          <h3>Why it failed</h3>
          <ul>
            {failingChecks.map((c) => (
              <li key={c.key}>
                <strong>{c.key}</strong>
                {c.description ? <> — {c.description}</> : null}
                {c.url && (
                  <>
                    {' '}
                    <a
                      href={c.url}
                      target="_blank"
                      rel="noreferrer"
                      aria-label={`View details for ${c.key}, opens in new tab`}
                    >
                      View details
                    </a>
                  </>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {cell.kind === 'no-op' && cell.noopNote && (
        <div className="hp-drawer__section hp-drawer__section--muted">
          <h3>Why it's a no-op</h3>
          <p>{cell.noopNote}</p>
        </div>
      )}

      {cell.commitStatuses.length > 0 && cell.kind !== 'failed' && (
        <div className="hp-drawer__section">
          <h3>Checks</h3>
          <ul className="hp-drawer__checks">
            {[...passingChecks, ...pendingChecks, ...failingChecks].map((c) => (
              <li key={c.key} className={`hp-drawer__check hp-drawer__check--${c.phase}`}>
                <span className="hp-drawer__check-icon" aria-hidden="true">{healthIcon[c.phase as HealthKey] ?? healthIcon.unknown}</span>
                <span className="hp-sr-only">
                  {HEALTH_LABELS[(c.phase as HealthKey)] ?? HEALTH_LABELS.unknown}:{' '}
                </span>
                <span className="hp-drawer__check-key">{c.key}</span>
                {c.description && <span className="hp-drawer__check-desc">{c.description}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}

      {row.body && (
        <div className="hp-drawer__section">
          <h3>Commit message</h3>
          <pre className="hp-drawer__body">{row.body}</pre>
        </div>
      )}

      <div className="hp-drawer__section">
        <h3>This commit across environments</h3>
        <ul className="hp-drawer__presence">
          {envs.map((e) => {
            const c = row.cells[e.branch];
            const isHere = e.branch === branch;
            return (
              <li
                key={e.branch}
                className={[
                  'hp-drawer__presence-item',
                  `hp-drawer__presence-item--${c.kind}`,
                  isHere ? 'hp-drawer__presence-item--current' : '',
                ].join(' ')}
              >
                <span className="hp-drawer__presence-branch">{e.branch}</span>
                <span className={`cell__pill cell__pill--${c.kind}`}>
                  {c.kind === 'live' && 'LIVE'}
                  {c.kind === 'in-flight' && (c.isProposed ? 'PROPOSED' : 'PR OPEN')}
                  {c.kind === 'was-here' && 'WAS HERE'}
                  {c.kind === 'failed' && (<><FaTimesCircle aria-hidden="true" /> FAILED</>)}
                  {c.kind === 'no-op' && (<><FaBan aria-hidden="true" /> NO-OP</>)}
                  {c.kind === 'not-reached' && '—'}
                </span>
                {c.at && (
                  <span className="hp-drawer__presence-time">{timeAgo(c.at)}</span>
                )}
              </li>
            );
          })}
        </ul>
        {presence.length === 1 && downstreamNotReached.length > 0 && (
          <p className="hp-drawer__hint">
            This commit only reached <strong>{branch}</strong>. It hasn't moved on to{' '}
            {downstreamNotReached.map((e) => e.branch).join(', ')}.
          </p>
        )}
        {upstream && (
          <p className="hp-drawer__hint">
            Promoted here from <strong>{upstream.branch}</strong>.
          </p>
        )}
      </div>

      {cell.kind === 'was-here' && cell.supersededById && rowsById.get(cell.supersededById) && (
        <div className="hp-drawer__section">
          <h3>Replaced by</h3>
          <button
            type="button"
            className="hp-drawer__replaced"
            onClick={() => onJumpToRow(cell.supersededById!)}
          >
            <FaArrowRight aria-hidden="true" />
            <span>{rowsById.get(cell.supersededById)!.subject}</span>
          </button>
        </div>
      )}
      </div>
    </aside>
  );
};

export default HistoryPage;
