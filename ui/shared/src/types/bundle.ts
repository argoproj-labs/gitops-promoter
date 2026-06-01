import type { Commit, CommitStatus, History, PromotionStrategy, PullRequest } from './promotion';

/** One branch side (active/proposed) of a ChangeTransferPolicy status. */
export interface ChangeTransferPolicyBranchState {
  dry?: Commit;
  hydrated?: Commit;
  commitStatuses?: CommitStatus[];
}

/**
 * ChangeTransferPolicy is the (trimmed) shape the dashboard reads from the bundle's
 * `changeTransferPolicies`. The PromotionStrategy status is no longer included in the
 * bundle, so per-environment state is reconstructed from these CTPs (one per
 * environment, keyed by spec.activeBranch).
 */
export interface ChangeTransferPolicy {
  metadata?: { name?: string; namespace?: string };
  spec?: {
    activeBranch?: string;
    proposedBranch?: string;
  };
  status?: {
    active?: ChangeTransferPolicyBranchState;
    proposed?: ChangeTransferPolicyBranchState;
    pullRequest?: PullRequest;
    history?: History[];
  };
}

/**
 * PromotionStrategyBundle is the JSON shape of the aggregated
 * dashboard.promoter.argoproj.io/v1alpha1 PromotionStrategyDetails resource. It
 * joins a PromotionStrategy with everything related to it, pre-computed by the
 * server so the UI does not need to stitch resources together client-side.
 *
 * Secrets are never present in this payload. The embedded PromotionStrategy carries
 * only metadata + spec (its status is dropped); per-environment state lives on the
 * ChangeTransferPolicies.
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
  changeTransferPolicies?: ChangeTransferPolicy[];
  pullRequests?: unknown[];
  commitStatuses?: unknown[];
  argoCDCommitStatuses?: unknown[];
  gitCommitStatuses?: unknown[];
  timedCommitStatuses?: unknown[];
  webRequestCommitStatuses?: unknown[];
  gitRepository?: unknown;
  scmProvider?: unknown;
  clusterScmProvider?: unknown;
}
