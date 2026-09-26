import { describe, it, expect } from 'vitest';
import { buildMatrix } from '@lib/components/HistoryView/buildMatrix';
import { canShowRevertCommand } from '@lib/components/HistoryView/revertCommand';
import type { Environment, PromotionStrategy } from '@shared/types/promotion';

const BRANCH = 'environments/development';

// Dry (config) SHAs. A restore re-deploys OLD_DRY, so the restore entry and the original
// promotion entry both report it — which is exactly the collision the row keying handles.
const OLD_DRY = '7523c5bdfeef00000000000000000000000000aa';
const NEW_DRY = 'dcf3fcf38d9900000000000000000000000000bb';

// Hydrated (environment-branch) SHAs.
const OLD_HYDRATED = '15cbb66df82600000000000000000000000000cc';
const NEW_HYDRATED = '230af2a6112000000000000000000000000000dd';
const RESTORE_HYDRATED = '8f3ac7256179000000000000000000000000000e';

const dryCommit = (sha: string, subject: string, commitTime: string) => ({
  sha,
  subject,
  commitTime,
  author: 'Zach Aller <zach@example.com>',
  repoURL: 'https://github.com/org/repo',
});

/**
 * @param restored when set, the newest history entry is a restore back to OLD_DRY and the
 *   active branch sits on that restore commit.
 */
function strategyWithHistory(restored: boolean): PromotionStrategy {
  const oldEntry = {
    active: {
      dry: dryCommit(OLD_DRY, 'chore: bump version to v1.0.1993', '2026-09-17T17:24:26Z'),
      hydrated: { sha: OLD_HYDRATED },
      commitStatuses: [],
    },
    pullRequest: { id: '2957', url: 'https://github.com/org/repo/pull/2957', state: 'merged' },
  };
  const newEntry = {
    active: {
      dry: dryCommit(NEW_DRY, 'chore: bump version to v1.0.1997', '2026-09-22T14:22:40Z'),
      hydrated: { sha: NEW_HYDRATED },
      commitStatuses: [],
    },
    pullRequest: {
      id: '2972',
      url: 'https://github.com/org/repo/pull/2972',
      state: 'merged',
      prMergeTime: '2026-09-24T21:33:41Z',
    },
  };
  const restoreEntry = {
    active: {
      dry: dryCommit(OLD_DRY, 'chore: bump version to v1.0.1993', '2026-09-17T17:24:26Z'),
      hydrated: {
        sha: RESTORE_HYDRATED,
        subject: `Revert ${BRANCH} to ${OLD_HYDRATED.slice(0, 7)}`,
        author: 'Zach Aller <zach@example.com>',
      },
      commitStatuses: [],
    },
    // Copied verbatim from the restored version, then re-stamped with the restore time.
    pullRequest: {
      id: '2957',
      url: 'https://github.com/org/repo/pull/2957',
      state: 'merged',
      prMergeTime: '2026-09-25T12:53:44Z',
    },
    restoredFrom: OLD_HYDRATED,
  };

  const liveDry = restored ? OLD_DRY : NEW_DRY;
  const env = {
    branch: BRANCH,
    active: {
      dry: dryCommit(
        liveDry,
        restored ? 'chore: bump version to v1.0.1993' : 'chore: bump version to v1.0.1997',
        restored ? '2026-09-17T17:24:26Z' : '2026-09-22T14:22:40Z',
      ),
      hydrated: restored
        ? {
            sha: RESTORE_HYDRATED,
            subject: `Revert ${BRANCH} to ${OLD_HYDRATED.slice(0, 7)}`,
            author: 'Zach Aller <zach@example.com>',
          }
        : { sha: NEW_HYDRATED },
      commitStatuses: [],
    },
    // match_trees: after a restore the proposed tree equals the active tree.
    proposed: { dry: dryCommit(liveDry, 'same tree', '2026-09-17T17:24:26Z'), hydrated: {} },
    lastHealthyDryShas: [],
    history: restored ? [restoreEntry, newEntry, oldEntry] : [newEntry, oldEntry],
  } as unknown as Environment;

  return {
    spec: { environments: [{ branch: BRANCH }] },
    status: { environments: [env] },
  } as unknown as PromotionStrategy;
}

describe('buildMatrix restore rows', () => {
  it('collapses a restore into the original row when the entry is unmarked', () => {
    // Without restoredFrom the restore's dry sha keys it onto the v1.0.1993 row, which is
    // the behavior that made reverts invisible in the grid.
    const { rows } = buildMatrix(strategyWithHistory(false));
    expect(rows.filter((r) => r.dryShaFull === OLD_DRY)).toHaveLength(1);
    expect(rows.every((r) => !r.restoredFrom)).toBe(true);
  });

  it('gives a marked restore its own row alongside the original promotion', () => {
    const { rows } = buildMatrix(strategyWithHistory(true));
    const forOldDry = rows.filter((r) => r.dryShaFull === OLD_DRY);

    expect(forOldDry).toHaveLength(2);
    const restoreRow = forOldDry.find((r) => r.restoredFrom);
    const originalRow = forOldDry.find((r) => !r.restoredFrom);
    expect(restoreRow).toBeDefined();
    expect(originalRow).toBeDefined();
    expect(restoreRow!.restoredFrom).toBe(OLD_HYDRATED);
    expect(restoreRow!.id).not.toBe(originalRow!.id);
  });

  it('keeps the restored version dry identity on both rows and names the revert commit', () => {
    // Two rows carrying the same dry sha and subject is the signal that the branch went
    // back in time; the revert commit rides alongside rather than replacing the subject.
    const { rows } = buildMatrix(strategyWithHistory(true));
    const forOldDry = rows.filter((r) => r.dryShaFull === OLD_DRY);
    const restoreRow = forOldDry.find((r) => r.restoredFrom)!;
    const originalRow = forOldDry.find((r) => !r.restoredFrom)!;

    expect(restoreRow.subject).toBe(originalRow.subject);
    expect(restoreRow.dryShaShort).toBe(originalRow.dryShaShort);
    expect(restoreRow.restoreSubject).toBe(`Revert ${BRANCH} to ${OLD_HYDRATED.slice(0, 7)}`);
    expect(restoreRow.restoreShaShort).toBe(RESTORE_HYDRATED.slice(0, 7));
    expect(originalRow.restoreSubject).toBeUndefined();
  });

  it('sorts the restore row to the top using the restore time, not the dry commit time', () => {
    const { rows } = buildMatrix(strategyWithHistory(true));
    expect(rows[0]!.restoredFrom).toBe(OLD_HYDRATED);
    expect(rows[0]!.freshestAt).toBe(new Date('2026-09-25T12:53:44Z').getTime());
  });

  it('puts the live cell on the restore row and leaves the original superseded', () => {
    const { rows } = buildMatrix(strategyWithHistory(true));
    const forOldDry = rows.filter((r) => r.dryShaFull === OLD_DRY);
    const restoreRow = forOldDry.find((r) => r.restoredFrom)!;
    const originalRow = forOldDry.find((r) => !r.restoredFrom)!;

    expect(restoreRow.cells[BRANCH]!.kind).toBe('live');
    expect(restoreRow.cells[BRANCH]!.restoredFrom).toBe(OLD_HYDRATED);
    expect(originalRow.cells[BRANCH]!.kind).toBe('was-here');
  });

  it('marks a superseded restore as restored rather than was-here', () => {
    const strategy = strategyWithHistory(true);
    // Promote past the restore: active moves back to v1.0.1997, as auto-merge would do.
    const env = strategy.status!.environments![0] as Environment;
    env.active = {
      dry: dryCommit(NEW_DRY, 'chore: bump version to v1.0.1997', '2026-09-22T14:22:40Z'),
      hydrated: { sha: NEW_HYDRATED },
      commitStatuses: [],
    };

    const { rows } = buildMatrix(strategy);
    const restoreRow = rows.find((r) => r.restoredFrom)!;
    expect(restoreRow.cells[BRANCH]!.kind).toBe('restored');
  });

  const heldStrategy = (
    proposedDry: string,
    pullRequest: Environment['pullRequest'],
    blockedDrySha: string,
  ) => {
    const strategy = strategyWithHistory(true);
    const env = strategy.status!.environments![0] as Environment;
    env.proposed = {
      dry: dryCommit(proposedDry, 'chore: bump version to v1.0.2006', '2026-09-25T16:00:00Z'),
      hydrated: { sha: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' },
      commitStatuses: [],
    };
    env.pullRequest = pullRequest;
    env.revertCommit = { name: 'revert-staging', blockedDrySha };
    return strategy;
  };

  it('marks the reverted commit as held and drops the last merged pull request', () => {
    const reverted = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    const strategy = heldStrategy(
      reverted,
      { id: '3017', url: 'https://github.com/org/repo/pull/3017', state: 'merged' },
      reverted,
    );

    const { rows, envs } = buildMatrix(strategy);
    const cell = rows.find((r) => r.dryShaFull === reverted)!.cells[BRANCH]!;
    expect(cell.kind).toBe('in-flight');
    expect(cell.isProposed).toBe(true);
    expect(cell.revertCommit).toBe('revert-staging');
    expect(cell.revertedByRevertCommit).toBe(true);
    expect(cell.pullRequest).toBeUndefined();
    expect(envs[0]!.proposedPR).toBeUndefined();
  });

  it('keeps the open pull request on a newer commit held by the RevertCommit', () => {
    const newer = 'cccccccccccccccccccccccccccccccccccccccc';
    const open = { id: '3020', url: 'https://github.com/org/repo/pull/3020', state: 'open' };
    const strategy = heldStrategy(newer, open, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');

    const { rows, envs } = buildMatrix(strategy);
    const cell = rows.find((r) => r.dryShaFull === newer)!.cells[BRANCH]!;
    expect(cell.revertCommit).toBe('revert-staging');
    expect(cell.revertedByRevertCommit).toBe(false);
    expect(cell.pullRequest?.id).toBe('3020');
    expect(envs[0]!.proposedPR?.id).toBe('3020');
  });
});

describe('buildMatrix restore command eligibility', () => {
  const failing = [{ key: 'ci', phase: 'failure' }];

  it('offers the restore command on a past active version', () => {
    const { rows } = buildMatrix(strategyWithHistory(false));
    const cell = rows.find((r) => r.dryShaFull === OLD_DRY)!.cells[BRANCH]!;
    expect(cell.kind).toBe('was-here');
    expect(canShowRevertCommand(cell)).toBe(true);
  });

  it('does not offer it on a failing live commit', () => {
    const strategy = strategyWithHistory(false);
    const env = strategy.status!.environments![0] as Environment;
    env.active.commitStatuses = failing as Environment['active']['commitStatuses'];

    const { rows } = buildMatrix(strategy);
    const cell = rows.find((r) => r.dryShaFull === NEW_DRY)!.cells[BRANCH]!;
    expect(cell.kind).toBe('failed');
    expect(canShowRevertCommand(cell)).toBe(false);
  });

  it('does not offer it on a failing proposed commit, which was never promoted', () => {
    const proposedDry = 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee';
    const strategy = strategyWithHistory(false);
    const env = strategy.status!.environments![0] as Environment;
    env.proposed = {
      dry: dryCommit(proposedDry, 'chore: bump version to v1.0.2006', '2026-09-25T16:00:00Z'),
      hydrated: { sha: 'ffffffffffffffffffffffffffffffffffffffff' },
      commitStatuses: failing,
    } as unknown as Environment['proposed'];

    const { rows } = buildMatrix(strategy);
    const cell = rows.find((r) => r.dryShaFull === proposedDry)!.cells[BRANCH]!;
    expect(cell.kind).toBe('failed');
    expect(canShowRevertCommand(cell)).toBe(false);
  });
});
