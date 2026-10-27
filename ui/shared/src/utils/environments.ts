import type {
  Environment,
  EnvironmentRestoreActiveCommit,
  PromotionStrategy,
} from '../types/promotion';
import type {
  ChangeTransferPolicy,
  ChangeTransferPolicyHistory,
  RestoreActiveCommit,
} from '../types/view';

/** Label a GitOps Promoter install stamps on the resources it owns (api/v1alpha1 InstanceIDLabel). */
export const INSTANCE_ID_LABEL = 'promoter.argoproj.io/instance-id';

/**
 * Reconstruct the per-environment status the UI renders. The bundle does not carry the
 * PromotionStrategy status (it was a duplicate aggregation); instead the environment list is
 * built from the embedded ChangeTransferPolicies, one per environment keyed by
 * spec.activeBranch, in the order declared by the PromotionStrategy spec. Promotion history is
 * sourced from the per-environment ChangeTransferPolicyHistory resources, keyed the same way.
 * RestoreActiveCommits are matched to an environment by spec.branch. The bundle is already one
 * PromotionStrategy, so the branch is unique within it. Several RestoreActiveCommits can target
 * that branch; only the one whose restore commit is still the active tip supplies blockedDrySha.
 */
export function environmentsFromBundle(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
  histories: ChangeTransferPolicyHistory[],
  restoreActiveCommits: RestoreActiveCommit[] = [],
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

  const restoresByBranch = new Map<string, RestoreActiveCommit[]>();
  for (const rc of restoreActiveCommits) {
    const branch = rc.spec?.branch;
    const name = rc.metadata?.name;
    if (!branch || !name || rc.metadata?.deletionTimestamp) continue;
    const existing = restoresByBranch.get(branch);
    if (existing) existing.push(rc);
    else restoresByBranch.set(branch, [rc]);
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
      restoreActiveCommit: restoreForEnvironment(
        restoresByBranch.get(env.branch),
        active.hydrated?.sha,
      ),
      history: historiesByBranch.get(env.branch)?.status?.history,
      lastHealthyDryShas: [],
    };
  });
}

/**
 * The RestoreActiveCommit the UI attributes this environment's block to.
 *
 * blockedDrySha comes only from the object whose status.activeSha is the live active tip. An
 * older RestoreActiveCommit for the same branch still names the dry SHA its own restore moved off,
 * which is not the one the current tip moved off. When none match, the first live object is still
 * returned so the block stays visible, and blockedDryShaFromHistory recovers the dry SHA from history.
 */
function restoreForEnvironment(
  rcs: RestoreActiveCommit[] | undefined,
  activeHydratedSha: string | undefined,
): EnvironmentRestoreActiveCommit | undefined {
  if (!rcs?.length) return undefined;

  const current = rcs.find(
    (rc) => rc.status?.activeSha !== undefined && rc.status.activeSha === activeHydratedSha,
  );
  const chosen = current ?? rcs[0];
  const name = chosen.metadata?.name;
  if (!name) return undefined;

  const projected: EnvironmentRestoreActiveCommit = { name };
  if (chosen.status?.activeSha) projected.activeSha = chosen.status.activeSha;
  if (current?.status?.blockedDrySha) projected.blockedDrySha = current.status.blockedDrySha;
  return projected;
}

/**
 * Hover copy for a proposed commit while a RestoreActiveCommit blocks its environment. `blocked`
 * is true when the proposed commit is the dry SHA the restore moved off the active branch.
 */
export function restoreBlockTooltip(name: string, blocked: boolean): string {
  return blocked
    ? `RestoreActiveCommit ${name} blocked this commit off the active branch, so it will not be promoted. Push a newer commit and set spec.blockEnvironment to false to resume promotion.`
    : `RestoreActiveCommit ${name} is blocking this environment, so this pull request will not auto-merge. Set spec.blockEnvironment to false to resume promotion.`;
}

/**
 * Dry SHA the active branch was restored off of, when the tip is still that restore commit.
 * Prefer RestoreActiveCommit.status.blockedDrySha when that object's activeSha is the live tip
 * (or when activeSha was not projected). Otherwise recover the same value from promotion history:
 * the entry immediately older than the live restore.
 */
export function blockedDryShaFromHistory(env: Environment): string | undefined {
  const restore = env.restoreActiveCommit;
  const activeHydrated = env.active.hydrated?.sha;
  const tipMatches = !restore?.activeSha || restore.activeSha === activeHydrated;
  if (restore?.blockedDrySha && tipMatches) return restore.blockedDrySha;

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
export function proposedIsBlocked(env: Environment): boolean {
  const blocked = blockedDryShaFromHistory(env);
  return !!blocked && blocked === env.proposed.dry?.sha;
}
