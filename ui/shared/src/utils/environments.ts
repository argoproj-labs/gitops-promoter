import type { Environment, EnvironmentRevertCommit, PromotionStrategy } from '../types/promotion';
import type {
  ChangeTransferPolicy,
  ChangeTransferPolicyHistory,
  RevertCommit,
} from '../types/view';

/** Label a GitOps Promoter install stamps on the resources it owns (api/v1alpha1 InstanceIDLabel). */
export const INSTANCE_ID_LABEL = 'promoter.argoproj.io/instance-id';

/**
 * Reconstruct the per-environment status the UI renders. The bundle does not carry the
 * PromotionStrategy status (it was a duplicate aggregation); instead the environment list is
 * built from the embedded ChangeTransferPolicies, one per environment keyed by
 * spec.activeBranch, in the order declared by the PromotionStrategy spec. Promotion history is
 * sourced from the per-environment ChangeTransferPolicyHistory resources, keyed the same way.
 * RevertCommits are matched to an environment by the ChangeTransferPolicy they name.
 */
export function environmentsFromBundle(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
  histories: ChangeTransferPolicyHistory[],
  revertCommits: RevertCommit[] = [],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  const historiesByBranch = new Map<string, ChangeTransferPolicyHistory>();
  for (const ctph of histories) {
    const branch = ctph.spec.activeBranch;
    if (branch) historiesByBranch.set(branch, ctph);
  }

  const revertsByPolicy = new Map<string, EnvironmentRevertCommit>();
  for (const rc of revertCommits) {
    const policy = rc.spec?.changeTransferPolicyRef.name;
    const name = rc.metadata?.name;
    if (!policy || !name || rc.metadata?.deletionTimestamp) continue;
    if (!revertsByPolicy.has(policy)) {
      revertsByPolicy.set(policy, { name, blockedDrySha: rc.status?.blockedDrySha });
    }
  }

  return spec.environments.map((env) => {
    const ctp = byBranch.get(env.branch);
    const status = ctp?.status ?? {};
    const ctpName = ctp?.metadata?.name;
    return {
      branch: env.branch,
      changeTransferPolicyName: ctpName,
      instanceId: ctp?.metadata?.labels?.[INSTANCE_ID_LABEL],
      active: status.active ?? { dry: {}, hydrated: {} },
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      revertCommit: ctpName ? revertsByPolicy.get(ctpName) : undefined,
      history: historiesByBranch.get(env.branch)?.status?.history,
      lastHealthyDryShas: [],
    };
  });
}

/**
 * Hover copy for a proposed commit while a RevertCommit holds its environment. `reverted` is
 * true when the proposed commit is the one the RevertCommit moved off the active branch.
 */
export function revertHoldTooltip(name: string, reverted: boolean): string {
  return reverted
    ? `RevertCommit ${name} reverted this commit off the active branch, so it will not be promoted. Push a newer commit and delete the RevertCommit to resume promotion.`
    : `RevertCommit ${name} is holding this environment in its reverted state, so this pull request will not auto-merge. Delete the RevertCommit to resume promotion.`;
}

/** Whether the environment's current proposed commit is the one its RevertCommit reverted. */
export function proposedIsReverted(env: Environment): boolean {
  const blocked = env.revertCommit?.blockedDrySha;
  return !!blocked && blocked === env.proposed.dry?.sha;
}
