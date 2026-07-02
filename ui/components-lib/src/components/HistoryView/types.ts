import type { Commit, CommitStatus, PullRequest } from '@shared/types/promotion';

/* ═════════════════════════════════════════════════════════════════
   Data model
   ═════════════════════════════════════════════════════════════════ */

export type HealthKey = 'success' | 'failure' | 'pending' | 'unknown';

export type CellKind = 'live' | 'in-flight' | 'was-here' | 'failed' | 'no-op' | 'not-reached';

export interface CellState {
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

export interface EnvColumn {
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
 * Env identity palette. Order matters: assigned positionally to envs as they
 * appear in the spec.
 */
// Every hue meets WCAG AA (>=4.5:1 on white) because
// these colors are applied to text (branch labels, SHA pills), not just
// accents. #d97706 (3.2:1) was darkened to #9a5a0f (5.5:1 on white).
export const LANE_COLORS = ['#1f4f5e', '#6f42c1', '#9a5a0f', '#0f766e', '#b91c1c', '#4338ca'];

export interface CommitRow {
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
}

export type FilterId = 'all' | 'live' | 'in-flight' | 'failed' | 'no-op';
export type SortId = 'newest' | 'oldest';
