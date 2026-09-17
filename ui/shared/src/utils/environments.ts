import type { Environment, PromotionStrategy } from '../types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyHistory } from '../types/view';

/**
 * Reconstruct the per-environment status the UI renders. The bundle does not carry the
 * PromotionStrategy status (it was a duplicate aggregation); instead the environment list is
 * built from the embedded ChangeTransferPolicies, one per environment keyed by
 * spec.activeBranch, in the order declared by the PromotionStrategy spec. Promotion history is
 * sourced from the per-environment PromotionStrategyHistory resources, keyed the same way.
 */
export function environmentsFromBundle(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
  histories: PromotionStrategyHistory[],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  const historiesByBranch = new Map<string, PromotionStrategyHistory>();
  for (const psh of histories) {
    const branch = psh.spec.activeBranch;
    if (branch) historiesByBranch.set(branch, psh);
  }

  return spec.environments.map((env) => {
    const status = byBranch.get(env.branch)?.status ?? {};
    return {
      branch: env.branch,
      active: status.active ?? { dry: {}, hydrated: {} },
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      history: historiesByBranch.get(env.branch)?.status?.history,
      lastHealthyDryShas: [],
    };
  });
}
