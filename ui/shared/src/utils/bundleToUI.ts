import { mergeCommitStatusManagers } from './PSData';
import { sortStrategyCommitStatuses } from './util';
import { environmentsFromBundle } from './environments';
import type { CommitStatusManagerBundle } from './PSData';
import type { PromotionStrategy } from '../types/promotion';
import type { PromotionStrategyDetails } from '../types/view';

export function managersFromBundle(bundle: PromotionStrategyDetails): CommitStatusManagerBundle {
  return {
    timedCommitStatuses: bundle.timedCommitStatuses,
    gitCommitStatuses: bundle.gitCommitStatuses,
    scheduledCommitStatuses: bundle.scheduledCommitStatuses,
    argoCDCommitStatuses: bundle.argoCDCommitStatuses,
    webRequestCommitStatuses: bundle.webRequestCommitStatuses,
    dependentsSuccessfulCommitStatuses: bundle.dependentsSuccessfulCommitStatuses,
  };
}

export function mergePromotionStrategyFromBundle(
  bundle: PromotionStrategyDetails,
): PromotionStrategy {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromBundle(
    ps.spec,
    bundle.changeTransferPolicies ?? [],
    bundle.changeTransferPolicyHistories ?? [],
  );
  const psWithEnvironments = {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
  sortStrategyCommitStatuses(psWithEnvironments);
  return mergeCommitStatusManagers(psWithEnvironments, managersFromBundle(bundle));
}
