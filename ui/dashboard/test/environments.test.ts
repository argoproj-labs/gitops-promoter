import { describe, expect, it } from 'vitest';
import { environmentsFromBundle } from '@shared/utils/environments';
import type { PromotionStrategy } from '@shared/types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyHistory } from '@shared/types/view';

const spec = {
  environments: [{ branch: 'environment/dev' }, { branch: 'environment/prod' }],
} as unknown as PromotionStrategy['spec'];

const ctps = [
  {
    spec: { activeBranch: 'environment/prod' },
    status: {
      active: { dry: { sha: 'prod-active' }, hydrated: {} },
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
] as unknown as PromotionStrategyHistory[];

describe('environmentsFromBundle', () => {
  it('orders environments by the strategy spec and keys CTP status by activeBranch', () => {
    const envs = environmentsFromBundle(spec, ctps, histories);

    expect(envs.map((e) => e.branch)).toEqual(['environment/dev', 'environment/prod']);
    expect(envs[0].active.dry?.sha).toBe('dev-active');
    expect(envs[1].active.dry?.sha).toBe('prod-active');
    expect(envs[1].pullRequest?.id).toBe('42');
  });

  it('projects history from the PromotionStrategyHistory resources', () => {
    const envs = environmentsFromBundle(spec, ctps, histories);

    expect(envs[0].history).toHaveLength(1);
    expect(envs[0].history?.[0].pullRequest?.id).toBe('41');
    expect(envs[1].history).toBeUndefined();
  });

  it('renders empty branch states when an environment has no CTP yet', () => {
    const envs = environmentsFromBundle(spec, [], []);

    expect(envs).toHaveLength(2);
    expect(envs[0].active).toEqual({ dry: {}, hydrated: {} });
    expect(envs[0].history).toBeUndefined();
  });
});
