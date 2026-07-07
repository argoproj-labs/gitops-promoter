import { describe, it, expect } from 'vitest';
import { repoURLFromBundle } from '@shared/utils/bundle';
import type { PromotionStrategyBundle } from '@shared/types/bundle';

describe('repoURLFromBundle', () => {
  it('derives GitHub URL from GitRepository and ScmProvider', () => {
    const bundle = {
      kind: 'PromotionStrategyDetails',
      apiVersion: 'view.promoter.argoproj.io/v1alpha1',
      metadata: { name: 'ps', namespace: 'default' },
      promotionStrategy: { metadata: { name: 'ps', namespace: 'default' }, spec: {} },
      gitRepository: {
        spec: { github: { owner: 'my-org', name: 'my-repo' } },
      },
      scmProvider: { spec: { github: { domain: 'github.example.com' } } },
    } as PromotionStrategyBundle;

    expect(repoURLFromBundle(bundle)).toBe('https://github.example.com/my-org/my-repo');
  });

  it('derives GitLab URL from GitRepository and ScmProvider', () => {
    const bundle = {
      kind: 'PromotionStrategyDetails',
      apiVersion: 'view.promoter.argoproj.io/v1alpha1',
      metadata: { name: 'ps', namespace: 'default' },
      promotionStrategy: { metadata: { name: 'ps', namespace: 'default' }, spec: {} },
      gitRepository: {
        spec: { gitlab: { namespace: 'my-group', name: 'my-repo' } },
      },
      scmProvider: { spec: { gitlab: { domain: 'gitlab.example.com' } } },
    } as PromotionStrategyBundle;

    expect(repoURLFromBundle(bundle)).toBe('https://gitlab.example.com/my-group/my-repo');
  });

  it('ignores CTP status dry.repoURL (hydrated links use SCM, dry links use per-commit status)', () => {
    const bundle = {
      kind: 'PromotionStrategyDetails',
      apiVersion: 'view.promoter.argoproj.io/v1alpha1',
      metadata: { name: 'ps', namespace: 'default' },
      promotionStrategy: { metadata: { name: 'ps', namespace: 'default' }, spec: {} },
      changeTransferPolicies: [
        {
          spec: { activeBranch: 'environment/dev' },
          status: {
            proposed: { dry: { repoURL: 'https://github.com/org/from-hydrator.git' } },
          },
        },
      ],
      gitRepository: {
        spec: { github: { owner: 'my-org', name: 'my-repo' } },
      },
      scmProvider: { spec: { github: {} } },
    } as PromotionStrategyBundle;

    expect(repoURLFromBundle(bundle)).toBe('https://github.com/my-org/my-repo');
  });
});
