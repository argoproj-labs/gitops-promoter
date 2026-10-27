import type { PromotionStrategy } from '../types/promotion';
import type { PromotionStrategyDetails } from '../types/view';
import { environmentsFromBundle } from './environments';
import { mergeCommitStatusManagers } from './PSData';
import { sortStrategyCommitStatuses } from './util';

/**
 * The PromotionStrategy the dashboard and extension render from a PromotionStrategyDetails
 * bundle: status.environments rebuilt from the bundle's ChangeTransferPolicies, commit statuses
 * sorted by key, and each commit status joined to the manager resource that reports it.
 */
export function strategyFromBundle(bundle: PromotionStrategyDetails): PromotionStrategy {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromBundle(
    ps.spec,
    bundle.changeTransferPolicies ?? [],
    bundle.changeTransferPolicyHistories ?? [],
    bundle.restoreActiveCommits ?? [],
  );
  const promotionStrategy = {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
  sortStrategyCommitStatuses(promotionStrategy);
  return mergeCommitStatusManagers(promotionStrategy, bundle);
}
