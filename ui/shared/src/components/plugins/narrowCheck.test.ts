import { describe, it, expect } from 'vitest';
import { narrowCheck } from './narrowCheck';
import type { Check, CommitStatusManager } from '../../types/promotion';

const timedManager: CommitStatusManager = {
  spec: {
    promotionStrategyRef: { name: 'my-strategy' },
    environments: [{ branch: 'production', duration: '5m' }],
  },
  status: {
    environments: [
      {
        branch: 'production',
        sha: 'a'.repeat(40),
        commitTime: '2026-01-01T00:00:00Z',
        requiredDuration: '5m',
        phase: 'pending',
        atMostDurationRemaining: '5m',
      },
    ],
  },
};

const gitManager: CommitStatusManager = {
  spec: {},
};

const timedCheck: Check = {
  name: 'timer',
  status: 'pending',
  branch: 'production',
  kind: 'TimedCommitStatus',
  manager: timedManager,
};

const gitCheck: Check = {
  name: 'git-status',
  status: 'success',
  branch: 'production',
  kind: 'GitCommitStatus',
  manager: gitManager,
};

describe('narrowCheck', () => {
  it('returns the check narrowed when kind matches', () => {
    const result = narrowCheck(timedCheck, 'TimedCommitStatus');

    expect(result).toBeDefined();
    expect(result?.kind).toBe('TimedCommitStatus');
    expect(result?.manager.status?.environments?.[0].branch).toBe('production');
  });

  it('returns undefined when kind does not match', () => {
    const result = narrowCheck(gitCheck, 'TimedCommitStatus');

    expect(result).toBeUndefined();
  });
});
