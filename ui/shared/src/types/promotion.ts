export interface CommitStatus {
  key: string;
  phase: string;
  url?: string;
  details?: string;
}

export interface Commit {
  sha?: string;
  author?: string;
  subject?: string;
  body?: string;
  commitTime?: string | null;
  repoURL?: string;
  references?: Array<{
    commit: {
      author?: string;
      sha?: string;
      subject?: string;
      date?: string;
      body?: string;
      repoURL?: string;
    };
  }>;
}

export interface PullRequest {
  id: string;
  url?: string;
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
  details?: string;
  url?: string;
}

export interface EnrichedEnvDetails {
  // Environment info
  branch: string;
  phase: string;
  promotionStatus: string;
  
  // Active commits
  drySha: string;
  dryCommitAuthor: string;
  dryCommitSubject: string;
  dryCommitMessage: string;
  dryCommitDate: string;
  dryCommitUrl: string;
  activeChecks: Check[];
  activeChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };
  
  referenceSha: string;
  referenceCommitAuthor: string;
  referenceCommitSubject: string;
  referenceCommitDate: string;
  referenceCommitUrl: string;
  referenceCommitBody: string;
  
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

  proposedReferenceSha: string;
  proposedReferenceCommitAuthor: string;
  proposedReferenceCommitSubject: string;
  proposedReferenceCommitDate: string;
  proposedReferenceCommitUrl: string;
  proposedReferenceCommitBody: string;
}

export type PromotionPhase = 'success' | 'failure' | 'pending' | 'default'; 