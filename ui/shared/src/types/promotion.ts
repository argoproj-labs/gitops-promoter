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

export interface CommitStatus {
  key: string;
  phase: string;
  url?: string;
  description?: string;
}

export interface Commit {
  sha?: string;
  author?: string;
  subject?: string;
  body?: string;
  /** When the commit was made (dry/hydrated). RFC 3339 from CRD `commitTime` (`metav1.Time`). */
  commitTime?: Rfc3339DateTime | null;
  repoURL?: string;
  references?: Array<{
    commit: ReferenceCommit;
  }>;
}

export interface ReferenceCommit {
  sha?: string;
  author?: string;
  subject?: string;
  body?: string;
  /** Reference commit timestamp. RFC 3339 from CRD (`git show -s --format=%aI`). */
  date?: Rfc3339DateTime;
  url?: string;
  repoURL?: string;
}

export interface PullRequest {
  id: string;
  url?: string;
  /** When the promotion PR was merged. RFC 3339 from CRD (`metav1.Time`). */
  prMergeTime?: Rfc3339DateTime;
  state?: string;
  externallyMergedOrClosed?: boolean;
}

export interface History {
  active: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  proposed: {
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  pullRequest?: PullRequest;
}

export interface Environment {
  branch: string;
  active: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  proposed: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
    note?: { drySha?: string };
  };
  pullRequest?: PullRequest;
  history?: History[];
}

export interface PromotionStrategy {
  kind: string;
  apiVersion: string;
  metadata: {
    name: string;
    namespace: string;
    uid: string;
    resourceVersion: string;
    generation: number;
    creationTimestamp: Rfc3339DateTime;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
  spec: {
    gitRepositoryRef: {
      name: string;
      namespace?: string;
    };
    activeCommitStatuses?: { key: string }[] | null;
    proposedCommitStatuses?: { key: string }[] | null;
    environments: {
      branch: string;
      autoMerge?: boolean;
      activeCommitStatuses?: { key: string }[] | null;
      proposedCommitStatuses?: { key: string }[] | null;
    }[];
  };
  status?: { environments?: Environment[] };
}

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
