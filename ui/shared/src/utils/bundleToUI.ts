import { mergeCommitStatusManagers } from './PSData';
import { sortStrategyCommitStatuses } from './util';
import type { CommitStatusManagerBundle } from './PSData';
import type { Environment, PromotionStrategy } from '../types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyDetails } from '../types/view';

// Reconstruct the per-environment status the UI renders. The bundle no longer carries
// the PromotionStrategy status (it was a duplicate aggregation); instead we build the
// environment list from the embedded ChangeTransferPolicies, one per environment keyed
// by spec.activeBranch, in the order declared by the PromotionStrategy spec.
export function environmentsFromCTPs(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  return spec.environments.map((env) => {
    const status = byBranch.get(env.branch)?.status ?? {};
    return {
      branch: env.branch,
      active: status.active ?? { dry: {}, hydrated: {} },
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      history: status.history,
      lastHealthyDryShas: [],
    };
  });
}

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
  const environments = environmentsFromCTPs(ps.spec, bundle.changeTransferPolicies ?? []);
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
