import type { CommitStatus, Commit, PromotionStrategy, PullRequest } from './promotion';

/**
 * Server-computed summary of the commit-status gates for one branch state,
 * produced by the dashboard aggregation apiserver.
 */
export interface GateSummary {
  total: number;
  pending: number;
  success: number;
  failure: number;
}

/**
 * Server-computed per-environment rollup. Mirrors the Go
 * dashboard.EnvironmentRollup. The active/proposed branch states reuse the same
 * shape as PromotionStrategy environment statuses.
 */
export interface EnvironmentRollup {
  branch: string;
  changeTransferPolicyName?: string;
  active?: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  proposed?: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  activeGates?: GateSummary;
  proposedGates?: GateSummary;
  pullRequest?: PullRequest;
  promoted: boolean;
}

/**
 * PromotionStrategyBundle is the JSON shape of the aggregated
 * dashboard.promoter.argoproj.io/v1alpha1 PromotionStrategyDetails resource. It
 * joins a PromotionStrategy with everything related to it, pre-computed by the
 * server so the UI does not need to stitch resources together client-side.
 *
 * Secrets are never present in this payload.
 */
export interface PromotionStrategyBundle {
  kind: string;
  apiVersion: string;
  metadata: {
    name: string;
    namespace: string;
    uid?: string;
    resourceVersion?: string;
    creationTimestamp?: string;
    labels?: Record<string, string>;
  };
  promotionStrategy: PromotionStrategy;
  changeTransferPolicies?: unknown[];
  pullRequests?: unknown[];
  commitStatuses?: unknown[];
  argoCDCommitStatuses?: unknown[];
  gitCommitStatuses?: unknown[];
  timedCommitStatuses?: unknown[];
  webRequestCommitStatuses?: unknown[];
  gitRepository?: unknown;
  scmProvider?: unknown;
  clusterScmProvider?: unknown;
  environments?: EnvironmentRollup[];
}
