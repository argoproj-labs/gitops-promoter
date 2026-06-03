import type { PromotionStrategy as PromotionStrategyCRD } from './crds';

/**
 * RFC 3339 timestamp from the API (Kubernetes `metav1.Time` JSON, or git `%aI` on reference commits).
 * @see https://datatracker.ietf.org/doc/html/rfc3339
 *
 * Must stay parseable by `new Date()` through enrichment and into `TimeAgo` / `formatDate`.
 * Do not pass through `formatDate` in `PSData` — format only at render time.
 */
export type Rfc3339DateTime = string;

/** Pre-computed relative time for display (e.g. `"3 hours ago"`). Not a parseable timestamp. */
export type RelativeTimeAgo = string;

/** Full PromotionStrategy CRD object (generated from OpenAPI). */
export type PromotionStrategy = PromotionStrategyCRD;

type PromotionStrategyStatus = NonNullable<PromotionStrategy['status']>;

/** Observed environment status (`status.environments[]`), not spec `Environment`. */
export type Environment = PromotionStrategyStatus['environments'][number];

export type History = NonNullable<Environment['history']>[number];

/**
 * Commit metadata on a branch (`active.dry`, `proposed.dry`, etc.).
 * `commitTime` — when the commit was made (dry/hydrated). RFC 3339 from CRD `commitTime` (`metav1.Time`).
 */
export type Commit = NonNullable<Environment['active']['dry']>;

/**
 * Reference commit embedded in `Commit.references`.
 * `date` — reference commit timestamp. RFC 3339 from CRD (`git show -s --format=%aI`).
 */
export type ReferenceCommit = NonNullable<
  NonNullable<NonNullable<Commit['references']>[number]>['commit']
> & {
  /** Set by enrichment (`PSData`); not present on the CRD. */
  url?: string;
};

/** Observed commit status on a branch (not the `CommitStatus` CRD resource). */
export type BranchCommitStatus = NonNullable<
  NonNullable<Environment['active']['commitStatuses']>[number]
>;

/** @deprecated Use {@link BranchCommitStatus}. */
export type CommitStatus = BranchCommitStatus;

/**
 * Pull request state embedded in environment status (not the `PullRequest` CRD).
 * `prMergeTime` — when the promotion PR was merged. RFC 3339 from CRD (`metav1.Time`).
 */
export type EnvironmentPullRequest = NonNullable<Environment['pullRequest']>;

/** @deprecated Use {@link EnvironmentPullRequest}. */
export type PullRequest = EnvironmentPullRequest;

export interface Check {
  name: string;
  status: string;
  description?: string;
  url?: string;
}

export interface EnrichedEnvDetails {
  // Environment info
  branch: string;
  promotionStatus: string;

  // Active commits
  activeSha: string;
  activeCommitAuthor: string;
  activeCommitSubject: string;
  activeCommitMessage: string;
  /** RFC 3339 for `TimeAgo`; empty string when absent. */
  activeCommitDate: Rfc3339DateTime | '';
  activeCommitUrl: string;
  activeChecks: Check[];
  activeChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };
  activeStatus: 'success' | 'failure' | 'pending' | 'unknown';
  activePrUrl: string | null;
  activePrNumber: number | null;

  activeReferenceCommit: ReferenceCommit | null;
  activeReferenceCommitUrl: string | null;

  // Proposed commits
  proposedSha: string;
  prNumber: number | null;
  prUrl: string | null;
  proposedDryCommitAuthor: string;
  proposedDryCommitSubject: string;
  proposedDryCommitBody: string;
  /** RFC 3339 for `TimeAgo`; empty string when absent. */
  proposedDryCommitDate: Rfc3339DateTime | '';
  proposedDryCommitUrl: string;
  proposedChecks: Check[];
  proposedChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };
  proposedStatus: 'success' | 'failure' | 'pending' | 'unknown';

  proposedReferenceCommit: ReferenceCommit | null;
  proposedReferenceCommitUrl: string | null;

  /** Relative display string; do not pass to `TimeAgo`. */
  historyMergeTimeAgo: RelativeTimeAgo | null;
  /** Relative display string; do not pass to `TimeAgo`. */
  activeMergeTimeAgo: RelativeTimeAgo | null;
}

export type PromotionPhase = 'promoted' | 'failure' | 'pending' | 'unknown';
