import type { PromotionStrategy as PromotionStrategyResource } from './view';
import type { components } from './generated/view.gen';

/**
 * RFC 3339 timestamp from the API (Kubernetes `metav1.Time` JSON, or git `%aI` on reference commits).
 * @see https://datatracker.ietf.org/doc/html/rfc3339
 *
 * Must stay parseable by `new Date()` until `timeAgo` / `formatDate` format it at render time.
 */
export type Rfc3339DateTime = string;

/** Full PromotionStrategy CRD object (generated from view OpenAPI). */
export type PromotionStrategy = PromotionStrategyResource;

/**
 * Per-environment status assembled for PSData (from CTP status + spec branch).
 * `history` is not part of the EnvironmentStatus CRD schema; it is re-projected onto the
 * environment from the per-environment ChangeTransferPolicyHistory resource in the bundle.
 */
export type Environment = components['schemas']['EnvironmentStatus'] & {
  history?: History[];
  /** metadata.name of the ChangeTransferPolicy for this environment, when the bundle has one. */
  changeTransferPolicyName?: string;
  /**
   * The ChangeTransferPolicy's promoter.argoproj.io/instance-id label. Resources created for this
   * environment need the same label, or a non-default install's controller never sees them.
   */
  instanceId?: string;
  /** RestoreActiveCommit blocking this environment, from the bundle. */
  restoreActiveCommit?: EnvironmentRestoreActiveCommit;
};

/** The parts of a RestoreActiveCommit the UI needs to explain why a promotion is held. */
export interface EnvironmentRestoreActiveCommit {
  name: string;
  /**
   * Hydrated SHA of the restore commit this RestoreActiveCommit wrote (`status.activeSha`).
   * `blockedDrySha` applies only while this is still the environment's active hydrated SHA.
   */
  activeSha?: string;
  /** Dry SHA the RestoreActiveCommit moved off the active branch. */
  blockedDrySha?: string;
}

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
  /** Set by `buildMatrix`; not present on the CRD. */
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

export type CommitStatusManagerKind =
  | 'TimedCommitStatus'
  | 'GitCommitStatus'
  | 'ScheduledCommitStatus'
  | 'ArgoCDCommitStatus'
  | 'WebRequestCommitStatus'
  | 'DependentsSuccessfulCommitStatus';

export type CommitStatusManager =
  | components['schemas']['TimedCommitStatus']
  | components['schemas']['GitCommitStatus']
  | components['schemas']['ScheduledCommitStatus']
  | components['schemas']['ArgoCDCommitStatus']
  | components['schemas']['WebRequestCommitStatus']
  | components['schemas']['DependentsSuccessfulCommitStatus'];

/** {@link BranchCommitStatus} stamped with the manager join computed by `mergeCommitStatusManagers`. */
export interface EnrichedBranchCommitStatus extends BranchCommitStatus {
  kind?: CommitStatusManagerKind;
  manager?: CommitStatusManager;
}

export interface Check {
  name: string;
  status: string;
  description?: string;
  url?: string;
  branch: string;
  kind?: CommitStatusManagerKind;
  manager?: CommitStatusManager;
}

export type PromotionPhase = 'promoted' | 'failure' | 'pending' | 'unknown';
