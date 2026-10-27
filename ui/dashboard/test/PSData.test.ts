import { describe, it, expect } from 'vitest';
import { getChecks, mergeCommitStatusManagers } from '@shared/utils/PSData';
import type { CommitStatusManagerBundle } from '@shared/utils/PSData';
import type { Environment, PromotionStrategy } from '@shared/types/promotion';

// Minimal PromotionStrategy fixture wrapper used only to exercise mergeCommitStatusManagers,
// which operates on ps.status.environments; the rest of the CRD shape is irrelevant here.
function mergeEnvManagers(
  environment: Environment,
  managers: CommitStatusManagerBundle,
): Environment {
  const ps = {
    status: { environments: [environment] },
  } as unknown as PromotionStrategy;
  const merged = mergeCommitStatusManagers(ps, managers);
  return merged.status!.environments![0];
}

function checksFor(environment: Environment) {
  return {
    activeChecks: getChecks(environment.active.commitStatuses ?? [], environment.branch),
    proposedChecks: getChecks(environment.proposed.commitStatuses ?? [], environment.branch),
  };
}

const environmentWithChecks: Environment = {
  branch: 'environments/qal',
  active: {
    dry: {},
    hydrated: {},
    commitStatuses: [{ key: 'argocd-health', phase: 'success' }],
  },
  proposed: {
    dry: {},
    hydrated: {},
    commitStatuses: [
      { key: 'timer', phase: 'pending' },
      { key: 'dependents-successful', phase: 'pending' },
      { key: 'no-manager', phase: 'success' },
    ],
  },
  lastHealthyDryShas: [],
};

describe('getChecks - branch/kind/manager', () => {
  it('populates branch on every check regardless of manager match', () => {
    const env = checksFor(environmentWithChecks);

    expect(env.activeChecks[0].branch).toBe('environments/qal');
    expect(env.proposedChecks[0].branch).toBe('environments/qal');
    expect(env.proposedChecks[1].branch).toBe('environments/qal');
  });

  it('leaves kind and manager undefined when no manager bundle is provided', () => {
    const env = checksFor(environmentWithChecks);

    expect(env.activeChecks[0].kind).toBeUndefined();
    expect(env.activeChecks[0].manager).toBeUndefined();
  });

  it('matches a TimedCommitStatus by spec.key and status.environments[].branch', () => {
    const timedCommitStatus = {
      kind: 'TimedCommitStatus',
      spec: {
        key: 'timer',
        promotionStrategyRef: { name: 'my-strategy' },
        environments: [{ branch: 'environments/qal', duration: '5m' }],
      },
      status: {
        environments: [
          {
            branch: 'environments/qal',
            sha: 'a'.repeat(40),
            commitTime: '2026-05-22T14:40:04Z',
            requiredDuration: '5m',
            phase: 'pending',
            atMostDurationRemaining: '1m',
          },
        ],
      },
    };
    const managers: CommitStatusManagerBundle = { timedCommitStatuses: [timedCommitStatus] };

    const mergedEnv = mergeEnvManagers(environmentWithChecks, managers);
    const env = checksFor(mergedEnv);
    const check = env.proposedChecks.find((c) => c.name === 'timer');

    expect(check?.kind).toBe('TimedCommitStatus');
    expect(check?.manager).toBe(timedCommitStatus);
  });

  it('matches an ArgoCDCommitStatus by spec.key alone (status has no branch join key)', () => {
    const argoCDCommitStatus = {
      kind: 'ArgoCDCommitStatus',
      spec: { key: 'argocd-health' },
      status: {
        applicationsSelected: [
          {
            environment: 'environments/qal',
            clusterName: '',
            name: 'my-app',
            namespace: 'default',
            phase: 'success',
            sha: 'a'.repeat(40),
          },
        ],
      },
    };
    const managers: CommitStatusManagerBundle = { argoCDCommitStatuses: [argoCDCommitStatus] };

    const mergedEnv = mergeEnvManagers(environmentWithChecks, managers);
    const env = checksFor(mergedEnv);
    const check = env.activeChecks.find((c) => c.name === 'argocd-health');

    expect(check?.kind).toBe('ArgoCDCommitStatus');
    expect(check?.manager).toBe(argoCDCommitStatus);
  });

  it('matches a DependentsSuccessfulCommitStatus by spec.key and status.environments[].branch', () => {
    const dependentsCommitStatus = {
      kind: 'DependentsSuccessfulCommitStatus',
      spec: { key: 'dependents-successful', promotionStrategyRef: { name: 'my-strategy' } },
      status: {
        environments: [
          {
            branch: 'environments/qal',
            phase: 'pending',
            upstreams: [{ branch: 'environments/dev', satisfied: false, reason: 'not promoted' }],
          },
        ],
      },
    };
    const managers: CommitStatusManagerBundle = {
      dependentsSuccessfulCommitStatuses: [dependentsCommitStatus],
    };

    const mergedEnv = mergeEnvManagers(environmentWithChecks, managers);
    const env = checksFor(mergedEnv);
    const check = env.proposedChecks.find((c) => c.name === 'dependents-successful');

    expect(check?.kind).toBe('DependentsSuccessfulCommitStatus');
    expect(check?.manager).toBe(dependentsCommitStatus);
  });

  it('leaves kind/manager undefined when no manager matches the check key', () => {
    const managers: CommitStatusManagerBundle = {
      timedCommitStatuses: [
        {
          kind: 'TimedCommitStatus',
          spec: { key: 'timer', promotionStrategyRef: { name: 'my-strategy' }, environments: [] },
          status: { environments: [] },
        },
      ],
    };

    const mergedEnv = mergeEnvManagers(environmentWithChecks, managers);
    const env = checksFor(mergedEnv);
    const check = env.proposedChecks.find((c) => c.name === 'no-manager');

    expect(check?.kind).toBeUndefined();
    expect(check?.manager).toBeUndefined();
  });
});
