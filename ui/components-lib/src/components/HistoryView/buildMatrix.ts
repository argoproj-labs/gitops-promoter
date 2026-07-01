import { extractNameOnly, extractBodyPreTrailer } from '@shared/utils/util';
import type { Commit, PromotionStrategy, PullRequest } from '@shared/types/promotion';
import { LANE_COLORS } from './types';
import type { CellKind, CellState, CommitRow, EnvColumn } from './types';
import { healthFromStatuses, shortSha, commitKey } from './helpers';

/* ═════════════════════════════════════════════════════════════════
   Build the commit-first matrix
   ═════════════════════════════════════════════════════════════════ */

export function buildMatrix(strategy: PromotionStrategy): {
  envs: EnvColumn[];
  rows: CommitRow[];
} {
  const envs = (strategy.status?.environments ?? []).filter(
    (e) => (e.history?.length ?? 0) > 0 || e.active?.dry,
  );

  const specByBranch = new Map<string, { autoMerge?: boolean }>();
  for (const e of strategy.spec.environments ?? []) specByBranch.set(e.branch, e);

  const envColumns: EnvColumn[] = envs.map((env, i) => {
    const liveStatuses = env.active?.commitStatuses ?? [];
    const proposedStatuses = env.proposed?.commitStatuses ?? [];
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedDistinct =
      env.proposed?.dry && proposedSha && proposedSha !== liveSha
        ? env.proposed.dry
        : undefined;

    return {
      branch: env.branch,
      autoMerge: specByBranch.get(env.branch)?.autoMerge ?? false,
      color: LANE_COLORS[i % LANE_COLORS.length]!,
      liveCommit: env.active?.dry,
      liveStatuses,
      liveHealth: healthFromStatuses(liveStatuses),
      proposedCommit: proposedDistinct,
      proposedStatuses: proposedDistinct ? proposedStatuses : [],
      proposedHealth: proposedDistinct
        ? healthFromStatuses(proposedStatuses)
        : 'unknown',
      proposedPR: proposedDistinct ? env.pullRequest : undefined,
    };
  });

  // rowsById holds the merged matrix; we'll fill cells as we walk envs.
  const rowsById = new Map<string, CommitRow>();

  /** Look up or insert a row for this commit. */
  const getRow = (
    c: Commit | undefined,
    repoUrlFallback: string,
    pr?: PullRequest,
  ): CommitRow | null => {
    const key = commitKey(c);
    if (!key || !c) return null;
    let row = rowsById.get(key);
    if (!row) {
      row = {
        id: key,
        dryShaFull: c.sha ?? '',
        dryShaShort: shortSha(c.sha),
        subject: (c.subject ?? '').trim() || '(no subject)',
        author: c.author ? extractNameOnly(c.author) : '—',
        body: c.body ? extractBodyPreTrailer(c.body) : undefined,
        prId: pr?.id,
        prUrl: pr?.url,
        repoUrl: c.repoURL ?? repoUrlFallback,
        freshestAt: 0,
        earliestAt: 0,
        cells: {},
        hasLive: false,
        hasInFlight: false,
        hasFailed: false,
        hasNoop: false,
        isStuckUpstream: false,
      };
      rowsById.set(key, row);
    }
    if (pr?.id && !row.prId) {
      row.prId = pr.id;
      row.prUrl = pr.url;
    }
    return row;
  };

  /** Insert / upgrade a cell. Later cells overwrite when they have stronger
   *  semantics (live > in-flight > failed > was-here > no-op). */
  const cellRank: Record<CellKind, number> = {
    live: 6,
    'in-flight': 5,
    failed: 4,
    'was-here': 3,
    'no-op': 2,
    'not-reached': 1,
  };
  const setCell = (row: CommitRow, branch: string, next: CellState) => {
    const prev = row.cells[branch];
    if (!prev || cellRank[next.kind] >= cellRank[prev.kind]) {
      row.cells[branch] = next;
    }
  };

  envs.forEach((env) => {
    const branch = env.branch;
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedIsDistinct = !!proposedSha && proposedSha !== liveSha;

    // 1) Live cell
    if (env.active?.dry) {
      const statuses = env.active.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(env.active.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'live';
        setCell(row, branch, {
          kind,
          commit: env.active.dry,
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          at: env.active.dry.commitTime ?? undefined,
        });
      }
    }

    // 2) In-flight cell (distinct proposed)
    if (proposedIsDistinct && env.proposed?.dry) {
      const statuses = env.proposed.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(env.proposed.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'in-flight';
        setCell(row, branch, {
          kind,
          commit: env.proposed.dry,
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          isProposed: true,
          at: env.proposed.dry.commitTime ?? undefined,
        });
      }
    }

    // 3) History cells — walk newest → oldest. Detect no-op by comparing each
    // entry's dry SHA against the next-older entry's dry SHA (same heuristic
    // used in the previous design).
    const history = env.history ?? [];
    history.forEach((entry, idx) => {
      const commit = entry.active?.dry;
      if (!commit) return;
      const statuses = entry.active?.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const olderSha = history[idx + 1]?.active?.dry?.sha;
      const isNoop =
        !!commit.sha && !!olderSha && commit.sha === olderSha;
      let kind: CellKind = isNoop
        ? 'no-op'
        : health === 'failure'
          ? 'failed'
          : 'was-here';

      const row = getRow(commit, '', entry.pullRequest);
      if (!row) return;

      const supersededById = idx > 0 ? commitKey(history[idx - 1]?.active?.dry) ?? undefined : undefined;
      setCell(row, branch, {
        kind,
        commit,
        commitStatuses: statuses,
        health,
        pullRequest: entry.pullRequest,
        noopNote: isNoop ? `Same dry SHA as the previous entry, so ${branch} didn't change.` : undefined,
        supersededById,
        at: commit.commitTime ?? entry.pullRequest?.prMergeTime ?? undefined,
      });
    });
  });

  // Finalize rollups + timestamps
  const rows = Array.from(rowsById.values()).map((row) => {
    const times: number[] = [];
    for (const branch of envs.map((e) => e.branch)) {
      const c = row.cells[branch];
      if (c?.at) {
        const t = new Date(c.at).getTime();
        if (Number.isFinite(t)) times.push(t);
      }
      if (c?.commit?.commitTime) {
        const t = new Date(c.commit.commitTime).getTime();
        if (Number.isFinite(t)) times.push(t);
      }
    }
    if (times.length === 0 && row.dryShaFull) {
      // last-ditch: hold sort stable by pushing missing-time rows to the bottom
      times.push(0);
    }
    row.freshestAt = times.length ? Math.max(...times) : 0;
    row.earliestAt = times.length ? Math.min(...times.filter((t) => t > 0), ...(times.includes(0) ? [Infinity] : [])) : 0;
    if (!Number.isFinite(row.earliestAt)) row.earliestAt = row.freshestAt;

    // Fill missing cells as 'not-reached'
    for (const e of envs) {
      if (!row.cells[e.branch]) {
        row.cells[e.branch] = {
          kind: 'not-reached',
          commitStatuses: [],
          health: 'unknown',
        };
      }
    }

    const branches = envs.map((e) => e.branch);
    const states = branches.map((b) => row.cells[b].kind);
    row.hasLive = states.includes('live');
    row.hasInFlight = states.includes('in-flight');
    row.hasFailed = states.includes('failed');
    row.hasNoop = states.includes('no-op');
    // "Stuck upstream" calls out a real problem: a change that landed in the
    // first env, isn't moving anymore, and never reached the rest of the
    // pipeline. We deliberately exclude:
    //  • `live` — current head in the first env; promotion may simply not
    //    have happened yet, so calling it stuck is alarmist.
    //  • `in-flight` — already on its way, just not arrived.
    //  • `no-op` — by definition didn't change anything.
    // Stuck only makes sense in a multi-env pipeline: there must be a
    // downstream env that the commit failed to reach.
    if (branches.length < 2) {
      row.isStuckUpstream = false;
    } else {
      const firstBranch = branches[0];
      const firstCellKind = row.cells[firstBranch].kind;
      const stuckEligibleKinds: CellKind[] = ['was-here', 'failed'];
      const appearsInFirstAsStuck = stuckEligibleKinds.includes(firstCellKind);
      const appearsLater = branches.slice(1).some(
        (b) => row.cells[b].kind !== 'not-reached',
      );
      row.isStuckUpstream = appearsInFirstAsStuck && !appearsLater;
    }

    return row;
  });

  // Default sort: newest first
  rows.sort((a, b) => b.freshestAt - a.freshestAt);

  return { envs: envColumns, rows };
}
