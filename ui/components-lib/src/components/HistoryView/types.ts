import type { Commit, CommitStatus, PullRequest, ReferenceCommit } from '@shared/types/promotion';

export type HealthKey = 'success' | 'failure' | 'pending' | 'unknown';

export type CellKind = 'live' | 'in-flight' | 'was-here' | 'failed' | 'no-op' | 'no-changes';

export interface CellState {
  kind: CellKind;
  /** Dry commit (config source-of-truth) this cell represents. */
  commit?: Commit;
  /** Rendered-manifests commit (deploy repo, distinct sha) produced from `commit`. */
  hydrated?: Commit;
  /** Upstream code commits registered on the dry commit (`dry.references[].commit`). */
  references?: ReferenceCommit[];
  commitStatuses: CommitStatus[];
  health: HealthKey;
  pullRequest?: PullRequest;
  isProposed?: boolean;
  noopNote?: string;
  supersededById?: string;
  at?: string;
  liveDurationMs?: number;
  replacedAt?: string;
}

export interface EnvColumn {
  branch: string;
  autoMerge: boolean;
  color: string;
  liveCommit?: Commit;
  liveStatuses: CommitStatus[];
  liveHealth: HealthKey;
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
  refShaShort?: string;
  refUrl?: string;
  repoUrl: string;
  freshestAt: number;
  earliestAt: number;
  cells: Record<string, CellState>;
  hasLive: boolean;
  hasInFlight: boolean;
  hasFailed: boolean;
  hasNoop: boolean;
}

export type FilterId = 'all' | 'live' | 'in-flight' | 'failed' | 'no-op';
export type SortId = 'newest' | 'oldest';
