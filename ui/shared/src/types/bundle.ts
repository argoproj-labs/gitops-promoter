import type { PromotionStrategy } from './promotion';

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
}
