import { describe, it, expect } from 'vitest';
import { enrichFromEnvironments } from '@shared/utils/PSData';
import { timeAgo } from '@shared/utils/util';
import type { Environment } from '@shared/types/promotion';

const environmentWithReferenceCommit: Environment = {
  branch: 'environments/qal',
  active: {
    dry: {
      sha: 'af24f4de24ecf1a15bc3348f738ae3fd6fbbc73b',
      commitTime: '2026-05-22T14:40:04Z',
      author: 'deployment-bot <bot@example.com>',
      repoURL: 'https://github.example.com/deployment',
      subject: '[Changed] - infrastructure deployment',
    },
    hydrated: {},
  },
  proposed: {
    dry: {
      sha: 'c589fc882c4327688eb068673f9bcca07ea5b5bd',
      commitTime: '2026-05-22T15:05:15Z',
      author: 'deployment-bot <bot@example.com>',
      repoURL: 'https://github.example.com/deployment',
      subject: '[Changed] - infrastructure deployment: [version=master-d22207e]',
      references: [
        {
          commit: {
            author: '"alice" <alice@example.com>',
            date: '2026-05-22T15:00:36Z',
            sha: 'd22207e1fe7baad7e91400d80c42a599d25c1022',
            subject: 'Merge pull request #2 from dev-integration/alice-patch-1',
            repoURL: 'https://github.example.com/example-repo',
          },
        },
      ],
    },
    hydrated: {},
    commitStatuses: [],
  },
  lastHealthyDryShas: [],
};

describe('enrichFromEnvironments', () => {
  it('preserves RFC 3339 commit timestamps so TimeAgo does not render "NaN days ago"', () => {
    const [env] = enrichFromEnvironments([environmentWithReferenceCommit], 0);

    expect(env.proposedReferenceCommit?.date).toBe('2026-05-22T15:00:36Z');
    expect(env.proposedDryCommitDate).toBe('2026-05-22T15:05:15Z');
    expect(env.activeCommitDate).toBe('2026-05-22T14:40:04Z');

    for (const date of [
      env.proposedReferenceCommit?.date,
      env.proposedDryCommitDate,
      env.activeCommitDate,
    ]) {
      expect(timeAgo(date!)).not.toMatch(/NaN/);
    }
  });
});
