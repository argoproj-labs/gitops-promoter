import { describe, expect, it } from 'vitest';
import { environmentsFromBundle, proposedIsReverted } from '@shared/utils/environments';
import type { PromotionStrategy } from '@shared/types/promotion';
import type {
  ChangeTransferPolicy,
  ChangeTransferPolicyHistory,
  RevertActiveCommit,
} from '@shared/types/view';

const spec = {
  environments: [{ branch: 'environment/dev' }, { branch: 'environment/prod' }],
} as unknown as PromotionStrategy['spec'];

const ctps = [
  {
    metadata: {
      name: 'strategy-environment-prod-abcd',
      labels: { 'promoter.argoproj.io/instance-id': 'team-a' },
    },
    spec: { activeBranch: 'environment/prod' },
    status: {
      active: { dry: { sha: 'prod-active' }, hydrated: { sha: 'prod-hydrated' } },
      proposed: { dry: { sha: 'prod-proposed' }, hydrated: {} },
      pullRequest: { id: '42' },
    },
  },
  {
    spec: { activeBranch: 'environment/dev' },
    status: {
      active: { dry: { sha: 'dev-active' }, hydrated: {} },
      proposed: { dry: { sha: 'dev-proposed' }, hydrated: {} },
    },
  },
] as unknown as ChangeTransferPolicy[];

const histories = [
  {
    spec: { activeBranch: 'environment/dev' },
    status: {
      history: [{ pullRequest: { id: '41' } }],
    },
  },
] as unknown as ChangeTransferPolicyHistory[];

const revertActiveCommits = [
  {
    metadata: { name: 'revert-prod' },
    spec: { promotionStrategyRef: { name: 'my-strategy' }, branch: 'environment/prod' },
    status: { activeSha: 'prod-hydrated', blockedDrySha: 'prod-proposed' },
  },
  {
    metadata: { name: 'revert-going-away', deletionTimestamp: '2026-09-25T00:00:00Z' },
    spec: { promotionStrategyRef: { name: 'my-strategy' }, branch: 'environment/prod' },
  },
] as unknown as RevertActiveCommit[];

describe('environmentsFromBundle', () => {
  it('orders environments by the strategy spec and keys CTP status by activeBranch', () => {
    const envs = environmentsFromBundle(spec, ctps, histories, revertActiveCommits);

    expect(envs.map((e) => e.branch)).toEqual(['environment/dev', 'environment/prod']);
    expect(envs[0].active.dry?.sha).toBe('dev-active');
    expect(envs[1].active.dry?.sha).toBe('prod-active');
    expect(envs[1].pullRequest?.id).toBe('42');
    expect(envs[1].revertActiveCommit).toEqual({
      name: 'revert-prod',
      activeSha: 'prod-hydrated',
      blockedDrySha: 'prod-proposed',
    });
    expect(envs[0].revertActiveCommit).toBeUndefined();
    expect(proposedIsReverted(envs[1])).toBe(true);
    expect(envs[1].changeTransferPolicyName).toBe('strategy-environment-prod-abcd');
    expect(envs[0].changeTransferPolicyName).toBeUndefined();
    expect(envs[1].instanceId).toBe('team-a');
    expect(envs[0].instanceId).toBeUndefined();
  });

  it('projects history from the ChangeTransferPolicyHistory resources', () => {
    const envs = environmentsFromBundle(spec, ctps, histories);

    expect(envs[0].history).toHaveLength(1);
    expect(envs[0].history?.[0].pullRequest?.id).toBe('41');
    expect(envs[1].history).toBeUndefined();
  });

  it('uses the RevertActiveCommit whose restore is still the active tip', () => {
    const envs = environmentsFromBundle(spec, ctps, histories, [
      {
        metadata: { name: 'revert-a' },
        spec: { branch: 'environment/prod' },
        status: { activeSha: 'r1', blockedDrySha: 'd3' },
      },
      {
        metadata: { name: 'revert-b' },
        spec: { branch: 'environment/prod' },
        status: { activeSha: 'prod-hydrated', blockedDrySha: 'd2' },
      },
    ] as unknown as RevertActiveCommit[]);

    expect(envs[1].revertActiveCommit).toEqual({
      name: 'revert-b',
      activeSha: 'prod-hydrated',
      blockedDrySha: 'd2',
    });
    expect(proposedIsReverted({ ...envs[1], proposed: { dry: { sha: 'd2' }, hydrated: {} } })).toBe(
      true,
    );
    expect(proposedIsReverted({ ...envs[1], proposed: { dry: { sha: 'd3' }, hydrated: {} } })).toBe(
      false,
    );
  });

  it('keeps the hold and falls back to history when no RevertActiveCommit matches the tip', () => {
    const envs = environmentsFromBundle(
      spec,
      ctps,
      [
        {
          spec: { activeBranch: 'environment/prod' },
          status: {
            history: [
              {
                restoredFrom: 'h1',
                active: { dry: { sha: 'd-target' }, hydrated: { sha: 'prod-hydrated' } },
              },
              {
                active: { dry: { sha: 'd2' }, hydrated: { sha: 'r1' } },
              },
            ],
          },
        },
      ] as unknown as ChangeTransferPolicyHistory[],
      [
        {
          metadata: { name: 'revert-a' },
          spec: { branch: 'environment/prod' },
          status: { activeSha: 'r1', blockedDrySha: 'd3' },
        },
        {
          metadata: { name: 'revert-pending' },
          spec: { branch: 'environment/prod' },
          status: {},
        },
      ] as unknown as RevertActiveCommit[],
    );

    expect(envs[1].revertActiveCommit).toEqual({
      name: 'revert-a',
      activeSha: 'r1',
    });
    expect(proposedIsReverted({ ...envs[1], proposed: { dry: { sha: 'd2' }, hydrated: {} } })).toBe(
      true,
    );
    expect(proposedIsReverted({ ...envs[1], proposed: { dry: { sha: 'd3' }, hydrated: {} } })).toBe(
      false,
    );
  });

  it('renders empty branch states when an environment has no CTP yet', () => {
    const envs = environmentsFromBundle(spec, [], []);

    expect(envs).toHaveLength(2);
    expect(envs[0].active).toEqual({ dry: {}, hydrated: {} });
    expect(envs[0].history).toBeUndefined();
  });
});

describe('proposedIsReverted', () => {
  it('matches RevertActiveCommit.status.blockedDrySha while the CR exists', () => {
    const env = {
      active: { dry: { sha: 'abc' }, hydrated: { sha: 'h1' } },
      proposed: { dry: { sha: 'def' }, hydrated: {} },
      revertActiveCommit: { name: 'revert-prod', activeSha: 'h1', blockedDrySha: 'def' },
    };
    expect(proposedIsReverted(env as never)).toBe(true);
  });

  it('ignores blockedDrySha when activeSha is not the live tip and uses history', () => {
    const env = {
      active: { dry: { sha: 'abc' }, hydrated: { sha: 'r2' } },
      proposed: { dry: { sha: 'd3' }, hydrated: {} },
      revertActiveCommit: { name: 'revert-a', activeSha: 'r1', blockedDrySha: 'd3' },
      history: [
        {
          restoredFrom: 'h1',
          active: { dry: { sha: 'd-target' }, hydrated: { sha: 'r2' } },
        },
        {
          active: { dry: { sha: 'd2' }, hydrated: { sha: 'r1' } },
        },
      ],
    };
    expect(proposedIsReverted(env as never)).toBe(false);
    expect(
      proposedIsReverted({ ...env, proposed: { dry: { sha: 'd2' }, hydrated: {} } } as never),
    ).toBe(true);
  });

  it('recovers the blocked dry SHA from history after the RevertActiveCommit is deleted', () => {
    const env = {
      active: { dry: { sha: 'abc' }, hydrated: { sha: 'restore-h' } },
      proposed: { dry: { sha: 'def' }, hydrated: {} },
      history: [
        {
          restoredFrom: 'abc-h',
          active: { dry: { sha: 'abc' }, hydrated: { sha: 'restore-h' } },
        },
        {
          active: { dry: { sha: 'def' }, hydrated: { sha: 'old-h' } },
        },
      ],
    };
    expect(proposedIsReverted(env as never)).toBe(true);
  });

  it('does not treat a newer proposed dry SHA as reverted after the CR is gone', () => {
    const env = {
      active: { dry: { sha: 'abc' }, hydrated: { sha: 'restore-h' } },
      proposed: { dry: { sha: 'ghi' }, hydrated: {} },
      history: [
        {
          restoredFrom: 'abc-h',
          active: { dry: { sha: 'abc' }, hydrated: { sha: 'restore-h' } },
        },
        {
          active: { dry: { sha: 'def' }, hydrated: { sha: 'old-h' } },
        },
      ],
    };
    expect(proposedIsReverted(env as never)).toBe(false);
  });
});
