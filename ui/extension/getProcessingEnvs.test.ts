import { describe, it, expect } from 'vitest';
import { getProcessingEnvs } from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';

interface EnvOverrides {
  branch?: string;
  activeDrySha?: string;
  noteDrySha?: string;
  proposedDrySha?: string;
  hydratedSha?: string;
  hydratedCommitTime?: string;
}

const makeEnv = (o: EnvOverrides = {}): Environment => ({
  branch: o.branch ?? 'main',
  active: {
    dry: o.activeDrySha ? { sha: o.activeDrySha } : undefined,
  },
  proposed: {
    dry: o.proposedDrySha ? { sha: o.proposedDrySha } : undefined,
    hydrated:
      o.hydratedSha || o.hydratedCommitTime
        ? { sha: o.hydratedSha, commitTime: o.hydratedCommitTime ?? null }
        : undefined,
    note: o.noteDrySha ? { drySha: o.noteDrySha } : undefined,
  },
});

describe('getProcessingEnvs', () => {
  it('returns an empty set when no env has a dry sha', () => {
    const envs = [makeEnv({ branch: 'dev' }), makeEnv({ branch: 'prod' })];
    expect(getProcessingEnvs(envs).size).toBe(0);
  });

  it('returns an empty set when all envs share the target dry sha (at rest)', () => {
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'old',
        noteDrySha: 'aaa',
        proposedDrySha: 'aaa',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'old',
        noteDrySha: 'aaa',
        proposedDrySha: 'aaa',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    expect(getProcessingEnvs(envs).size).toBe(0);
  });

  it('marks a lagging env as processing when its note has caught up but differs from target', () => {
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'new',
        proposedDrySha: 'new',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'older',
        noteDrySha: 'old',
        proposedDrySha: 'old',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    const result = getProcessingEnvs(envs);
    expect(result.has('prod')).toBe(true);
    expect(result.has('dev')).toBe(false);
  });

  it('marks a pending env as processing when its note is transiently absent (falls back to dry sha)', () => {
    // Leading env defines target via note; lagging env has a proposed change
    // (active.dry != proposed.dry) but its note hasn't been written yet.
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'new',
        proposedDrySha: 'new',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'older',
        proposedDrySha: 'old',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    const result = getProcessingEnvs(envs);
    expect(result.has('prod')).toBe(true);
  });

  it('does not mark an env with no proposed change (active.dry == proposed.dry) as processing', () => {
    // prod has already deployed what it is proposing — nothing is pending,
    // so it must not show the processing placeholder even though it lags the target.
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'new',
        proposedDrySha: 'new',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'old',
        proposedDrySha: 'old',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    const result = getProcessingEnvs(envs);
    expect(result.has('prod')).toBe(false);
    expect(result.size).toBe(0);
  });

  it('does not mark an env with no proposed commit as processing', () => {
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'new',
        proposedDrySha: 'new',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({ branch: 'prod' }),
    ];
    const result = getProcessingEnvs(envs);
    expect(result.has('prod')).toBe(false);
    expect(result.size).toBe(0);
  });

  it('does not mark an env whose effective dry sha matches the target even if its hydrated commit is older', () => {
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'same',
        proposedDrySha: 'same',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'older',
        proposedDrySha: 'same',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    expect(getProcessingEnvs(envs).size).toBe(0);
  });

  it('prefers note.drySha over proposed.dry.sha when both are present', () => {
    // prod's note says it has reached the target, even though its dry.sha differs.
    const envs = [
      makeEnv({
        branch: 'dev',
        activeDrySha: 'older',
        noteDrySha: 'target',
        proposedDrySha: 'target',
        hydratedSha: 'h1',
        hydratedCommitTime: '2026-06-16T00:00:00Z',
      }),
      makeEnv({
        branch: 'prod',
        activeDrySha: 'older',
        noteDrySha: 'target',
        proposedDrySha: 'stale',
        hydratedSha: 'h2',
        hydratedCommitTime: '2026-06-15T00:00:00Z',
      }),
    ];
    expect(getProcessingEnvs(envs).has('prod')).toBe(false);
  });
});
