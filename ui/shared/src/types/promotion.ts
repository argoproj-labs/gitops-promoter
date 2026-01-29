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
  commitTime?: string | null;
  repoURL?: string;
  references?: Array<{
    commit: ReferenceCommit;
  }>;
}

export interface ReferenceCommit  {
  sha?: string;
  author?: string;
  subject?: string;
  body?: string;
  date?: string;
  url?: string;
  repoURL?: string;
}

export interface PullRequest {
  id: string;
  url?: string;
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
    creationTimestamp: string;
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
  activeCommitDate: string;
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
  proposedDryCommitDate: string;
  proposedDryCommitUrl: string;
  proposedChecks: Check[];
  proposedChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };
  proposedStatus: 'success' | 'failure' | 'pending' | 'unknown';

  proposedReferenceCommit: ReferenceCommit | null;
  proposedReferenceCommitUrl: string | null;
}

export type PromotionPhase = 'promoted' | 'failure' | 'pending' | 'unknown'; 