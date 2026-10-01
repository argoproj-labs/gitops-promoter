import { describe, it, expect } from 'vitest';
import { buildMatrix } from '@components-lib/components/HistoryView/buildMatrix';
import type {
  Commit,
  CommitStatus,
  Environment,
  History,
  PromotionStrategy,
} from '@shared/types/promotion';

const status = (phase: string, key = 'e2e'): CommitStatus => ({ key, phase });

const commit = (char: string, minutesAgo: number): Commit =>
  ({
    sha: char.repeat(40),
    subject: `commit ${char}`,
    author: 'Someone <someone@example.com>',
    commitTime: new Date(Date.now() - minutesAgo * 60_000).toISOString(),
  }) as Commit;

const historyEntry = (dry: Commit, statuses: CommitStatus[]): History =>
  ({ active: { dry, commitStatuses: statuses } }) as History;

const strategyWith = (env: Environment): PromotionStrategy =>
  ({
    spec: { environments: [{ branch: env.branch }] },
    status: { environments: [env] },
  }) as PromotionStrategy;

const kindFor = (strategy: PromotionStrategy, c: Commit, branch = 'production') =>
  buildMatrix(strategy).rows.find((r) => r.dryShaFull === c.sha)?.cells[branch]?.kind;

describe('buildMatrix cell kinds', () => {
  const live = commit('a', 10);
  const brokenPast = commit('b', 20);
  const healthyPast = commit('c', 30);

  const env = (liveStatuses: CommitStatus[]): Environment =>
    ({
      branch: 'production',
      active: { dry: live, commitStatuses: liveStatuses },
      history: [
        historyEntry(live, liveStatuses),
        historyEntry(brokenPast, [status('success', 'lint'), status('failure')]),
        historyEntry(healthyPast, [status('success')]),
      ],
    }) as Environment;

  it('classifies a replaced history entry with failing checks as "was-failed"', () => {
    expect(kindFor(strategyWith(env([status('success')])), brokenPast)).toBe('was-failed');
  });

  it('keeps a currently live commit with failing checks as "failed"', () => {
    expect(kindFor(strategyWith(env([status('failure')])), live)).toBe('failed');
  });

  it('keeps a currently live commit with passing checks as "live"', () => {
    expect(kindFor(strategyWith(env([status('success')])), live)).toBe('live');
  });

  it('keeps a replaced history entry with passing checks as "was-here"', () => {
    expect(kindFor(strategyWith(env([status('success')])), healthyPast)).toBe('was-here');
  });

  it('keeps a failing proposed commit as "failed"', () => {
    const proposed = commit('d', 5);
    const strategy = strategyWith({
      ...env([status('success')]),
      proposed: { dry: proposed, commitStatuses: [status('failure')] },
    } as Environment);
    expect(kindFor(strategy, proposed)).toBe('failed');
  });

  it('counts a "was-failed" cell toward the row\'s hasFailed flag', () => {
    const row = buildMatrix(strategyWith(env([status('success')]))).rows.find(
      (r) => r.dryShaFull === brokenPast.sha,
    );
    expect(row?.hasFailed).toBe(true);
  });
});
