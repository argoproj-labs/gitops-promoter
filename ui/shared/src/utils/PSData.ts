import type { components } from '../types/generated/view.gen';
import type {
  BranchCommitStatus,
  CommitStatusManager,
  CommitStatusManagerKind,
  EnrichedBranchCommitStatus,
  Environment,
  History,
  PromotionStrategy,
  Check,
} from '../types/promotion';

export interface CommitStatusManagerBundle {
  timedCommitStatuses?: components['schemas']['TimedCommitStatus'][];
  gitCommitStatuses?: components['schemas']['GitCommitStatus'][];
  scheduledCommitStatuses?: components['schemas']['ScheduledCommitStatus'][];
  argoCDCommitStatuses?: components['schemas']['ArgoCDCommitStatus'][];
  webRequestCommitStatuses?: components['schemas']['WebRequestCommitStatus'][];
  dependentsSuccessfulCommitStatuses?: components['schemas']['DependentsSuccessfulCommitStatus'][];
}

function findManager(
  key: string,
  branch: string,
  managers: CommitStatusManagerBundle,
): { kind: CommitStatusManagerKind; manager: CommitStatusManager } | undefined {
  for (const tcs of managers.timedCommitStatuses ?? []) {
    if (tcs.spec.key === key && tcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'TimedCommitStatus', manager: tcs };
    }
  }
  for (const gcs of managers.gitCommitStatuses ?? []) {
    if (gcs.spec.key === key && gcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'GitCommitStatus', manager: gcs };
    }
  }
  for (const scs of managers.scheduledCommitStatuses ?? []) {
    if (scs.spec.key === key && scs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'ScheduledCommitStatus', manager: scs };
    }
  }
  for (const acs of managers.argoCDCommitStatuses ?? []) {
    if (acs.spec?.key === key) {
      return { kind: 'ArgoCDCommitStatus', manager: acs };
    }
  }
  for (const wrcs of managers.webRequestCommitStatuses ?? []) {
    if (wrcs.spec.key === key && wrcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'WebRequestCommitStatus', manager: wrcs };
    }
  }
  for (const dcs of managers.dependentsSuccessfulCommitStatuses ?? []) {
    if (dcs.spec.key === key && dcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'DependentsSuccessfulCommitStatus', manager: dcs };
    }
  }
  return undefined;
}

export function getChecks(commitStatuses: EnrichedBranchCommitStatus[], branch: string): Check[] {
  return commitStatuses.map((cs: EnrichedBranchCommitStatus) => ({
    name: cs.key,
    status: cs.phase,
    description: cs.description,
    url: cs.url,
    branch,
    kind: cs.kind,
    manager: cs.manager,
  }));
}

/**
 * Stamps `kind`/`manager` onto every commit-status entry nested in `ps.status.environments`
 * (active, proposed, and each history entry's active/proposed) using the same join semantics
 * as `findManager`. Returns a new `PromotionStrategy`-shaped value; does not mutate `ps`.
 */
export function mergeCommitStatusManagers(
  ps: PromotionStrategy,
  managers: CommitStatusManagerBundle,
): PromotionStrategy {
  if (!ps.status?.environments) {
    return ps;
  }

  const enrichStatuses = (
    commitStatuses: BranchCommitStatus[] | undefined,
    branch: string,
  ): EnrichedBranchCommitStatus[] | undefined =>
    commitStatuses?.map((cs) => {
      const match = findManager(cs.key, branch, managers);
      return { ...cs, kind: match?.kind, manager: match?.manager };
    });

  const environments: Environment[] = ps.status.environments.map((environment: Environment) => {
    const branch = environment.branch || '';

    const active = {
      ...environment.active,
      commitStatuses: enrichStatuses(environment.active.commitStatuses, branch),
    };
    const proposed = {
      ...environment.proposed,
      commitStatuses: enrichStatuses(environment.proposed.commitStatuses, branch),
    };

    const history: History[] | undefined = environment.history?.map((entry: History) => ({
      ...entry,
      active: entry.active
        ? { ...entry.active, commitStatuses: enrichStatuses(entry.active.commitStatuses, branch) }
        : entry.active,
      proposed: entry.proposed
        ? {
            ...entry.proposed,
            commitStatuses: enrichStatuses(entry.proposed.commitStatuses, branch),
          }
        : entry.proposed,
    }));

    return { ...environment, active, proposed, history };
  });

  return { ...ps, status: { ...ps.status, environments } };
}

export type { PromotionStrategy } from '../types/promotion';
