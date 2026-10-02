import { describe, expect, it } from 'vitest';
import { getEnvironmentStatus, getPromotionStatus } from './getStatus';
import type { Environment, PromotionStrategy } from '../types/promotion';

function env(opts: {
  activeSha?: string;
  proposedSha?: string;
  activePhases?: string[];
  proposedPhases?: string[];
}): Environment {
  return {
    branch: 'environments/dev',
    active: {
      dry: { sha: opts.activeSha },
      hydrated: {},
      commitStatuses: (opts.activePhases ?? []).map((phase) => ({ key: 'active-check', phase })),
    },
    proposed: {
      dry: { sha: opts.proposedSha },
      hydrated: {},
      commitStatuses: (opts.proposedPhases ?? []).map((phase) => ({
        key: 'proposed-check',
        phase,
      })),
    },
    lastHealthyDryShas: [],
  } as unknown as Environment;
}

describe('getEnvironmentStatus', () => {
  it('is promoted when active and proposed dry SHAs match', () => {
    expect(getEnvironmentStatus(env({ activeSha: 'abc', proposedSha: 'abc' }))).toBe('promoted');
  });

  it('is promoted when SHAs match even if an active check is failing', () => {
    expect(
      getEnvironmentStatus(
        env({ activeSha: 'abc', proposedSha: 'abc', activePhases: ['failure'] }),
      ),
    ).toBe('promoted');
  });

  it('is promoted when SHAs match even if a proposed check is failing', () => {
    expect(
      getEnvironmentStatus(
        env({ activeSha: 'abc', proposedSha: 'abc', proposedPhases: ['failure'] }),
      ),
    ).toBe('promoted');
  });

  it('is pending when proposed dry SHA differs from active', () => {
    expect(getEnvironmentStatus(env({ activeSha: 'abc', proposedSha: 'def' }))).toBe('pending');
  });

  it('is pending when SHAs differ even if an active check is failing', () => {
    expect(
      getEnvironmentStatus(
        env({ activeSha: 'abc', proposedSha: 'def', activePhases: ['failure'] }),
      ),
    ).toBe('pending');
  });

  it('is failure when SHAs differ and a proposed check is failing', () => {
    expect(
      getEnvironmentStatus(
        env({ activeSha: 'abc', proposedSha: 'def', proposedPhases: ['failure'] }),
      ),
    ).toBe('failure');
  });

  it('is unknown when proposed dry SHA is missing', () => {
    expect(getEnvironmentStatus(env({ activeSha: 'abc' }))).toBe('unknown');
  });

  it('is unknown when both dry SHAs are unset', () => {
    expect(getEnvironmentStatus(env({}))).toBe('unknown');
  });
});

describe('getPromotionStatus', () => {
  it('counts a SHA-matched environment as promoted despite a failing active check', () => {
    const ps = {
      status: {
        environments: [env({ activeSha: 'abc', proposedSha: 'abc', activePhases: ['failure'] })],
      },
    } as unknown as PromotionStrategy;

    expect(getPromotionStatus(ps)).toMatchObject({
      total: 1,
      promoted: 1,
      pending: 0,
      failed: 0,
      overallStatus: 'promoted',
    });
  });

  it('is unknown overall when environments have no dry SHAs yet', () => {
    const ps = {
      status: { environments: [env({})] },
    } as unknown as PromotionStrategy;

    expect(getPromotionStatus(ps)).toMatchObject({
      total: 1,
      promoted: 0,
      pending: 0,
      failed: 0,
      overallStatus: 'unknown',
    });
  });
});
