import type {
  Environment,
  EnvironmentRevertActiveCommit,
  PromotionStrategy,
} from '../types/promotion';
import type {
  ChangeTransferPolicy,
  ChangeTransferPolicyHistory,
  RevertActiveCommit,
} from '../types/view';

/** Label a GitOps Promoter install stamps on the resources it owns (api/v1alpha1 InstanceIDLabel). */
export const INSTANCE_ID_LABEL = 'promoter.argoproj.io/instance-id';

/**
 * Reconstruct the per-environment status the UI renders. The bundle does not carry the
 * PromotionStrategy status (it was a duplicate aggregation); instead the environment list is
 * built from the embedded ChangeTransferPolicies, one per environment keyed by
 * spec.activeBranch, in the order declared by the PromotionStrategy spec. Promotion history is
 * sourced from the per-environment ChangeTransferPolicyHistory resources, keyed the same way.
 * RevertActiveCommits are matched to an environment by spec.branch. The bundle is already one
 * PromotionStrategy, so the branch is unique within it. Several RevertActiveCommits can target
 * that branch; only the one whose restore commit is still the active tip supplies blockedDrySha.
 */
export function environmentsFromBundle(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
  histories: ChangeTransferPolicyHistory[],
  revertActiveCommits: RevertActiveCommit[] = [],
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

  const revertsByBranch = new Map<string, RevertActiveCommit[]>();
  for (const rc of revertActiveCommits) {
    const branch = rc.spec?.branch;
    const name = rc.metadata?.name;
    if (!branch || !name || rc.metadata?.deletionTimestamp) continue;
    const existing = revertsByBranch.get(branch);
    if (existing) existing.push(rc);
    else revertsByBranch.set(branch, [rc]);
  }

  return spec.environments.map((env) => {
    const ctp = byBranch.get(env.branch);
    const status = ctp?.status ?? {};
    const ctpName = ctp?.metadata?.name;
    const active = status.active ?? { dry: {}, hydrated: {} };
    return {
      branch: env.branch,
      changeTransferPolicyName: ctpName,
      instanceId: ctp?.metadata?.labels?.[INSTANCE_ID_LABEL],
      active,
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      revertActiveCommit: revertForEnvironment(
        revertsByBranch.get(env.branch),
        active.hydrated?.sha,
      ),
      history: historiesByBranch.get(env.branch)?.status?.history,
      lastHealthyDryShas: [],
    };
  });
}

/**
 * The RevertActiveCommit the UI attributes this environment's hold to.
 *
 * blockedDrySha comes only from the object whose status.activeSha is the live active tip. An
 * older RevertActiveCommit for the same branch still names the dry SHA its own restore moved off,
 * which is not the one the current tip moved off. When none match, the first live object is still
 * returned so the hold stays visible, and revertedDrySha recovers the dry SHA from history.
 */
function revertForEnvironment(
  rcs: RevertActiveCommit[] | undefined,
  activeHydratedSha: string | undefined,
): EnvironmentRevertActiveCommit | undefined {
  if (!rcs?.length) return undefined;

  const current = rcs.find(
    (rc) => rc.status?.activeSha !== undefined && rc.status.activeSha === activeHydratedSha,
  );
  const chosen = current ?? rcs[0];
  const name = chosen.metadata?.name;
  if (!name) return undefined;

  const projected: EnvironmentRevertActiveCommit = { name };
  if (chosen.status?.activeSha) projected.activeSha = chosen.status.activeSha;
  if (current?.status?.blockedDrySha) projected.blockedDrySha = current.status.blockedDrySha;
  return projected;
}

/**
 * Hover copy for a proposed commit while a RevertActiveCommit holds its environment. `reverted` is
 * true when the proposed commit is the one the RevertActiveCommit moved off the active branch.
 */
export function revertHoldTooltip(name: string, reverted: boolean): string {
  return reverted
    ? `RevertActiveCommit ${name} reverted this commit off the active branch, so it will not be promoted. Push a newer commit and delete the RevertActiveCommit to resume promotion.`
    : `RevertActiveCommit ${name} is holding this environment in its reverted state, so this pull request will not auto-merge. Delete the RevertActiveCommit to resume promotion.`;
}

/**
 * Dry SHA the active branch was restored off of, when the tip is still that restore commit.
 * Prefer RevertActiveCommit.status.blockedDrySha when that object's activeSha is the live tip
 * (or when activeSha was not projected). Otherwise recover the same value from promotion history:
 * the entry immediately older than the live restore.
 */
export function revertedDrySha(env: Environment): string | undefined {
  const revert = env.revertActiveCommit;
  const activeHydrated = env.active.hydrated?.sha;
  const tipMatches = !revert?.activeSha || revert.activeSha === activeHydrated;
  if (revert?.blockedDrySha && tipMatches) return revert.blockedDrySha;

  const history = env.history;
  if (!history?.length || !activeHydrated) return undefined;

  for (let i = 0; i < history.length; i++) {
    const entry = history[i];
    if (!entry.restoredFrom) continue;
    if (entry.active?.hydrated?.sha !== activeHydrated) continue;
    return history[i + 1]?.active?.dry?.sha;
  }
  return undefined;
}

/** Whether the environment's current proposed commit is the one a restore moved off the active branch. */
export function proposedIsReverted(env: Environment): boolean {
  const blocked = revertedDrySha(env);
  return !!blocked && blocked === env.proposed.dry?.sha;
}
