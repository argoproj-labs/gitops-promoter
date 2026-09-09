import { describe, it, expect } from 'vitest';
import { mergeCommitStatusManagers } from './PSData';
import type { CommitStatusManagerBundle } from './PSData';
import type { Environment, EnrichedBranchCommitStatus, PromotionStrategy } from '../types/promotion';

const BRANCH = 'environments/qal';
const OTHER_BRANCH = 'environments/prd';

function envWithKeys(branch: string, keys: string[]): Environment {
  return {
    branch,
    active: { dry: {}, hydrated: {}, commitStatuses: keys.map((key) => ({ key, phase: 'success' })) },
    proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
    lastHealthyDryShas: [],
  } as unknown as Environment;
}

function mergeActive(
  environment: Environment,
  managers: CommitStatusManagerBundle,
): EnrichedBranchCommitStatus[] {
  const ps = { status: { environments: [environment] } } as unknown as PromotionStrategy;
  const merged = mergeCommitStatusManagers(ps, managers);
  return (merged.status!.environments[0].active.commitStatuses ?? []) as EnrichedBranchCommitStatus[];
}

function timed(key: string, branches: string[]) {
  return {
    spec: { key, promotionStrategyRef: { name: 'my-strategy' }, environments: [] },
    status: { environments: branches.map((branch) => ({ branch, phase: 'pending' })) },
  } as unknown as NonNullable<CommitStatusManagerBundle['timedCommitStatuses']>[number];
}

function git(key: string, branches: string[]) {
  return {
    spec: { key, promotionStrategyRef: { name: 'my-strategy' } },
    status: { environments: branches.map((branch) => ({ branch, phase: 'pending' })) },
  } as unknown as NonNullable<CommitStatusManagerBundle['gitCommitStatuses']>[number];
}

function scheduled(key: string, branches: string[]) {
  return {
    spec: { key, promotionStrategyRef: { name: 'my-strategy' } },
    status: { environments: branches.map((branch) => ({ branch, phase: 'pending' })) },
  } as unknown as NonNullable<CommitStatusManagerBundle['scheduledCommitStatuses']>[number];
}

function webRequest(key: string, branches: string[]) {
  return {
    spec: { key, promotionStrategyRef: { name: 'my-strategy' } },
    status: { environments: branches.map((branch) => ({ branch, phase: 'pending' })) },
  } as unknown as NonNullable<CommitStatusManagerBundle['webRequestCommitStatuses']>[number];
}

function argoCD(key: string) {
  return {
    spec: { key },
    status: { applicationsSelected: [{ environment: BRANCH, name: 'my-app', phase: 'success' }] },
  } as unknown as NonNullable<CommitStatusManagerBundle['argoCDCommitStatuses']>[number];
}

describe('mergeCommitStatusManagers - branch-scoped manager matching', () => {
  it('returns the input unchanged when there are no environments', () => {
    const ps = { spec: {} } as unknown as PromotionStrategy;
    expect(mergeCommitStatusManagers(ps, {})).toBe(ps);
  });

  it('leaves kind/manager undefined when the bundle is empty', () => {
    const [check] = mergeActive(envWithKeys(BRANCH, ['timer']), {});

    expect(check.kind).toBeUndefined();
    expect(check.manager).toBeUndefined();
  });

  it('leaves kind/manager undefined when no manager key matches', () => {
    const [check] = mergeActive(envWithKeys(BRANCH, ['no-manager']), {
      timedCommitStatuses: [timed('timer', [BRANCH])],
    });

    expect(check.kind).toBeUndefined();
    expect(check.manager).toBeUndefined();
  });

  describe.each([
    ['TimedCommitStatus', 'timedCommitStatuses', timed],
    ['GitCommitStatus', 'gitCommitStatuses', git],
    ['ScheduledCommitStatus', 'scheduledCommitStatuses', scheduled],
    ['WebRequestCommitStatus', 'webRequestCommitStatuses', webRequest],
  ] as const)('%s', (kind, bundleKey, make) => {
    it('matches on spec.key plus a status.environments entry for the branch', () => {
      const manager = make('gate', [OTHER_BRANCH, BRANCH]);
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), { [bundleKey]: [manager] });

      expect(check.kind).toBe(kind);
      expect(check.manager).toBe(manager);
    });

    it('does not match when spec.key matches but the branch is absent from status.environments', () => {
      const manager = make('gate', [OTHER_BRANCH]);
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), { [bundleKey]: [manager] });

      expect(check.kind).toBeUndefined();
      expect(check.manager).toBeUndefined();
    });

    it('does not match when status.environments is missing entirely', () => {
      const manager = make('gate', []);
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), { [bundleKey]: [manager] });

      expect(check.kind).toBeUndefined();
      expect(check.manager).toBeUndefined();
    });
  });

  // ArgoCDCommitStatusStatus has no environments[] in the generated schema (only
  // applicationsSelected[].environment), so it deliberately matches on spec.key alone.
  describe('ArgoCDCommitStatus', () => {
    it('matches on spec.key alone, for any branch', () => {
      const manager = argoCD('argocd-health');
      const [check] = mergeActive(envWithKeys(OTHER_BRANCH, ['argocd-health']), {
        argoCDCommitStatuses: [manager],
      });

      expect(check.kind).toBe('ArgoCDCommitStatus');
      expect(check.manager).toBe(manager);
    });

    it('does not match when spec is absent', () => {
      const manager = { status: {} } as unknown as NonNullable<
        CommitStatusManagerBundle['argoCDCommitStatuses']
      >[number];
      const [check] = mergeActive(envWithKeys(BRANCH, ['argocd-health']), {
        argoCDCommitStatuses: [manager],
      });

      expect(check.kind).toBeUndefined();
    });
  });

  describe('key collisions across kinds', () => {
    it('resolves in bundle order: timed, git, scheduled, argoCD, webRequest', () => {
      const managers: CommitStatusManagerBundle = {
        timedCommitStatuses: [timed('gate', [BRANCH])],
        gitCommitStatuses: [git('gate', [BRANCH])],
        scheduledCommitStatuses: [scheduled('gate', [BRANCH])],
        argoCDCommitStatuses: [argoCD('gate')],
        webRequestCommitStatuses: [webRequest('gate', [BRANCH])],
      };
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), managers);

      expect(check.kind).toBe('TimedCommitStatus');
      expect(check.manager).toBe(managers.timedCommitStatuses![0]);
    });

    it('falls through to the next kind when the earlier kind fails the branch match', () => {
      const managers: CommitStatusManagerBundle = {
        timedCommitStatuses: [timed('gate', [OTHER_BRANCH])],
        gitCommitStatuses: [git('gate', [OTHER_BRANCH])],
        scheduledCommitStatuses: [scheduled('gate', [BRANCH])],
        webRequestCommitStatuses: [webRequest('gate', [BRANCH])],
      };
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), managers);

      expect(check.kind).toBe('ScheduledCommitStatus');
      expect(check.manager).toBe(managers.scheduledCommitStatuses![0]);
    });

    it('prefers argoCD over webRequest when both keys collide', () => {
      const managers: CommitStatusManagerBundle = {
        argoCDCommitStatuses: [argoCD('gate')],
        webRequestCommitStatuses: [webRequest('gate', [BRANCH])],
      };
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), managers);

      expect(check.kind).toBe('ArgoCDCommitStatus');
      expect(check.manager).toBe(managers.argoCDCommitStatuses![0]);
    });

    it('picks the first matching manager within a single kind', () => {
      const first = timed('gate', [BRANCH]);
      const second = timed('gate', [BRANCH]);
      const [check] = mergeActive(envWithKeys(BRANCH, ['gate']), {
        timedCommitStatuses: [first, second],
      });

      expect(check.manager).toBe(first);
    });
  });

  it('enriches proposed and history commit statuses with the environment branch', () => {
    const manager = timed('gate', [BRANCH]);
    const environment = {
      branch: BRANCH,
      active: { dry: {}, hydrated: {}, commitStatuses: [{ key: 'gate', phase: 'success' }] },
      proposed: { dry: {}, hydrated: {}, commitStatuses: [{ key: 'gate', phase: 'pending' }] },
      history: [
        {
          active: { dry: {}, hydrated: {}, commitStatuses: [{ key: 'gate', phase: 'success' }] },
          proposed: { dry: {}, hydrated: {}, commitStatuses: [{ key: 'gate', phase: 'pending' }] },
        },
      ],
      lastHealthyDryShas: [],
    } as unknown as Environment;

    const ps = { status: { environments: [environment] } } as unknown as PromotionStrategy;
    const merged = mergeCommitStatusManagers(ps, { timedCommitStatuses: [manager] });
    const env = merged.status!.environments[0];

    const enriched = [
      env.active.commitStatuses?.[0],
      env.proposed.commitStatuses?.[0],
      env.history?.[0].active?.commitStatuses?.[0],
      env.history?.[0].proposed?.commitStatuses?.[0],
    ] as EnrichedBranchCommitStatus[];

    for (const cs of enriched) {
      expect(cs.kind).toBe('TimedCommitStatus');
      expect(cs.manager).toBe(manager);
    }
  });

  it('does not mutate the input promotion strategy', () => {
    const environment = envWithKeys(BRANCH, ['gate']);
    const ps = { status: { environments: [environment] } } as unknown as PromotionStrategy;

    mergeCommitStatusManagers(ps, { timedCommitStatuses: [timed('gate', [BRANCH])] });

    const original = environment.active.commitStatuses?.[0] as EnrichedBranchCommitStatus;
    expect(original.kind).toBeUndefined();
    expect(original.manager).toBeUndefined();
  });
});
