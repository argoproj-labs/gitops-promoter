import type { PromotionStrategy as PromotionStrategyResource } from './view';
import type { components } from './generated/view.gen';

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

/** Full PromotionStrategy CRD object (generated from view OpenAPI). */
export type PromotionStrategy = PromotionStrategyResource;

/** Per-environment status assembled for Card/PSData (from CTP status + spec branch). */
export type Environment = components['schemas']['EnvironmentStatus'];

export type History = components['schemas']['History'];

/**
 * Commit metadata on a branch (`active.dry`, `proposed.dry`, etc.).
 * `commitTime` — when the commit was made (dry/hydrated). RFC 3339 from CRD `commitTime` (`metav1.Time`).
 */
export type Commit = components['schemas']['CommitShaState'];

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
export type BranchCommitStatus = components['schemas']['ChangeRequestPolicyCommitStatusPhase'];

/** @deprecated Use {@link BranchCommitStatus}. */
export type CommitStatus = BranchCommitStatus;

/**
 * Pull request state embedded in environment status (not the `PullRequest` CRD).
 * `prMergeTime` — when the promotion PR was merged. RFC 3339 from CRD (`metav1.Time`).
 */
export type EnvironmentPullRequest = components['schemas']['PullRequestCommonStatus'];

/** @deprecated Use {@link EnvironmentPullRequest}. */
export type PullRequest = EnvironmentPullRequest;

export interface Check {
  name: string;
  status: string;
  description?: string;
  url?: string;
}

/**
 * Result of {@link calculateHealthSummary}: how many checks passed out of the
 * total, plus whether the summary is worth rendering at all (`totalCount > 0`).
 * Shared by the card components and their `HealthSummary` so the shape stays in
 * one place.
 */
export interface HealthSummaryResult {
  successCount: number;
  totalCount: number;
  shouldDisplay: boolean;
}

/**
 * Flattened commit shape built by enrichment ({@link EnrichedEnvDetails}) and
 * passed to the card components / `CommitInfo`. All fields are display strings
 * already formatted for render (sha truncated at the source is not assumed).
 */
export interface DeploymentCommit {
  sha: string;
  author: string;
  subject: string;
  body: string;
  date: string;
}

/**
 * State-aware tooltip descriptor for the active PR indicator, derived in
 * enrichment ({@link EnrichedEnvDetails.activePrTooltip}). `label` is the
 * verb the tooltip renders (`merged` or `opened`); `time` is the matching
 * RFC 3339 timestamp (merge time when merged, creation time when open),
 * formatted at render time via `formatDate`.
 */
export interface PrTooltip {
  label: 'merged' | 'opened';
  time: Rfc3339DateTime;
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
  activeChecksSummary: HealthSummaryResult;
  activeStatus: 'success' | 'failure' | 'pending' | 'unknown';
  activePrUrl: string | null;
  activePrNumber: number | null;
  /** Active PR creation time. RFC 3339 for `formatDate`; null when absent. */
  activePrCreationTime: Rfc3339DateTime | null;
  /** Active PR merge time. RFC 3339 for `formatDate`; null when absent. */
  activePrMergeTime: Rfc3339DateTime | null;
  /** Raw active PR `state` from the CRD (e.g. `merged`, `open`, or `""`). */
  activePrState: string | null;
  /**
   * Derived merged-vs-open descriptor for the active PR tooltip, or null when
   * there is no active PR. Merged when `state === 'merged'` or (`state` is
   * empty and a merge time is present); otherwise open.
   */
  activePrTooltip: PrTooltip | null;

  activeReferenceCommit: ReferenceCommit | null;
  activeReferenceCommitUrl: string | null;

  // Proposed commits
  proposedSha: string;
  prNumber: number | null;
  prUrl: string | null;
  /** Derived merged-vs-open tooltip for the proposed-card PR link; null when no PR. */
  prTooltip: PrTooltip | null;
  proposedDryCommitAuthor: string;
  proposedDryCommitSubject: string;
  proposedDryCommitBody: string;
  /** RFC 3339 for `TimeAgo`; empty string when absent. */
  proposedDryCommitDate: Rfc3339DateTime | '';
  proposedDryCommitUrl: string;
  proposedChecks: Check[];
  proposedChecksSummary: HealthSummaryResult;
  proposedStatus: 'success' | 'failure' | 'pending' | 'unknown';

  proposedReferenceCommit: ReferenceCommit | null;
  proposedReferenceCommitUrl: string | null;

  /** Relative display string; do not pass to `TimeAgo`. */
  historyMergeTimeAgo: RelativeTimeAgo | null;
}

export type PromotionPhase = 'promoted' | 'failure' | 'pending' | 'unknown';
