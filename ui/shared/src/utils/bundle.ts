import type { Environment } from '../types/promotion';
import type {
  ChangeTransferPolicy,
  PromotionStrategy,
  PromotionStrategyDetails,
} from '../types/view';

/**
 * Reconstruct the per-environment status the UI renders from a bundle's embedded
 * ChangeTransferPolicies. The bundle no longer carries the PromotionStrategy status
 * (it was a duplicate aggregation); instead we build the environment list from the
 * CTPs, one per environment keyed by spec.activeBranch, in the order declared by the
 * PromotionStrategy spec.
 */
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

/**
 * Collapse a PromotionStrategyDetails bundle into a PromotionStrategy whose
 * status.environments is populated from the embedded CTPs. The metadata name/namespace
 * come from the bundle envelope, matching what the dashboard and extension render.
 */
export function bundleToPromotionStrategy(bundle: PromotionStrategyDetails): PromotionStrategy {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromCTPs(ps.spec, bundle.changeTransferPolicies ?? []);
  return {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
}
