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

// Active env carrying a merged PR (state === 'merged') on the live history entry.
const environmentWithMergedPr: Environment = {
  branch: 'environments/prd',
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
  proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
  history: [
    {
      pullRequest: {
        id: '42',
        url: 'https://github.example.com/deployment/pull/42',
        state: 'merged',
        prCreationTime: '2026-05-22T14:00:00Z',
        prMergeTime: '2026-05-22T14:52:00Z',
      },
      active: { dry: {}, hydrated: {}, commitStatuses: [] },
    },
  ],
  lastHealthyDryShas: [],
};

// Active env carrying an open PR (state === 'open') on the live history entry.
const environmentWithOpenPr: Environment = {
  branch: 'environments/dev',
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
  proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
  history: [
    {
      pullRequest: {
        id: '43',
        url: 'https://github.example.com/deployment/pull/43',
        state: 'open',
        prCreationTime: '2026-05-22T14:00:00Z',
      },
      active: { dry: {}, hydrated: {}, commitStatuses: [] },
    },
  ],
  lastHealthyDryShas: [],
};

// Active env carrying a closed-but-unmerged PR (state === 'closed') on the live history entry.
const environmentWithClosedPr: Environment = {
  branch: 'environments/uat',
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
  proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
  history: [
    {
      pullRequest: {
        id: '45',
        url: 'https://github.example.com/deployment/pull/45',
        state: 'closed',
        prCreationTime: '2026-05-22T14:00:00Z',
      },
      active: { dry: {}, hydrated: {}, commitStatuses: [] },
    },
  ],
  lastHealthyDryShas: [],
};

// Externally merged/closed PR: state is empty ("") but a merge time is present.
// Lives on environment.pullRequest and is picked up via the mergedEnvPr fallback.
const environmentWithExternallyMergedPr: Environment = {
  branch: 'environments/stg',
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
  proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
  pullRequest: {
    id: '44',
    url: 'https://github.example.com/deployment/pull/44',
    state: '',
    externallyMergedOrClosed: true,
    prCreationTime: '2026-05-22T14:00:00Z',
    prMergeTime: '2026-05-22T14:52:00Z',
  },
  lastHealthyDryShas: [],
};

// Externally closed (not merged) PR: state is empty ("") and no merge time is present.
// Lives on environment.pullRequest and is picked up via the mergedEnvPr fallback.
const environmentWithExternallyClosedPr: Environment = {
  branch: 'environments/int',
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
  proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
  pullRequest: {
    id: '46',
    url: 'https://github.example.com/deployment/pull/46',
    state: '',
    externallyMergedOrClosed: true,
    prCreationTime: '2026-05-22T14:00:00Z',
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

  it('surfaces active PR creation time, merge time, and state (RFC 3339 passthrough)', () => {
    const [env] = enrichFromEnvironments([environmentWithMergedPr], 0);

    expect(env.activePrCreationTime).toBe('2026-05-22T14:00:00Z');
    expect(env.activePrMergeTime).toBe('2026-05-22T14:52:00Z');
    expect(env.activePrState).toBe('merged');
  });

  it('derives a merged tooltip from state === "merged" using the merge time', () => {
    const [env] = enrichFromEnvironments([environmentWithMergedPr], 0);

    expect(env.activePrTooltip).toEqual({
      status: 'merged',
      label: 'merged',
      time: '2026-05-22T14:52:00Z',
    });
  });

  it('derives an opened tooltip from an open PR using the creation time', () => {
    const [env] = enrichFromEnvironments([environmentWithOpenPr], 0);

    expect(env.activePrState).toBe('open');
    expect(env.activePrTooltip).toEqual({
      status: 'opened',
      label: 'opened',
      time: '2026-05-22T14:00:00Z',
    });
  });

  it('derives a closed tooltip (not "opened") for a closed-but-unmerged PR', () => {
    const [env] = enrichFromEnvironments([environmentWithClosedPr], 0);

    expect(env.activePrState).toBe('closed');
    expect(env.activePrTooltip).toEqual({
      status: 'closed',
      label: 'closed',
      time: null,
    });
  });

  it('labels an externally merged PR (empty state + merge time) as merged externally', () => {
    const [env] = enrichFromEnvironments([environmentWithExternallyMergedPr], 0);

    expect(env.activePrState).toBe('');
    expect(env.activePrTooltip).toEqual({
      status: 'merged',
      label: 'merged externally',
      time: '2026-05-22T14:52:00Z',
    });
  });

  it('shows merged externally in the live slot for an ambiguous externally merged-or-closed PR', () => {
    const [env] = enrichFromEnvironments([environmentWithExternallyClosedPr], 0);

    // A PR occupying the active/live slot is what made the commit live, so it merged;
    // activePrState stays untouched ('') while the tooltip resolves the ambiguity to
    // merged and notes it happened outside the controller.
    expect(env.activePrState).toBe('');
    expect(env.activePrTooltip).toEqual({
      status: 'merged',
      label: 'merged externally',
      time: null,
    });
    // The non-live prTooltip still reflects the ambiguity with the hedged label.
    expect(env.prTooltip).toEqual({
      status: 'closed',
      label: 'closed or merged externally',
      time: null,
    });
  });
});
