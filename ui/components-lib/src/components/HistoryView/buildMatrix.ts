import { extractNameOnly, extractBodyPreTrailer, getCommitUrl } from '@shared/utils/util';
import type {
  Commit,
  PromotionStrategy,
  PullRequest,
  ReferenceCommit,
} from '@shared/types/promotion';
import { LANE_COLORS } from './types';
import type { CellKind, CellState, CommitRow, EnvColumn } from './types';
import { healthFromStatuses, shortSha, commitKey } from './helpers';

/**
 * Pull the upstream code commits registered on a dry commit into `ReferenceCommit[]`,
 * attaching a resolved `url` per ref (refs are cross-repo, so each carries its own
 * `repoURL`). Empty refs (config-only changes) yield an empty array.
 */
function toReferenceCommits(dry: Commit | undefined): ReferenceCommit[] {
  return (dry?.references ?? [])
    .map((r) => r.commit)
    .filter((c): c is NonNullable<typeof c> => !!c)
    .map((c) => ({
      ...c,
      url: c.repoURL && c.sha ? getCommitUrl(c.repoURL, c.sha) : undefined,
    }));
}

type StatusEnvironment = NonNullable<
  NonNullable<PromotionStrategy['status']>['environments']
>[number];

function envHasContent(env: StatusEnvironment): boolean {
  return (env.history?.length ?? 0) > 0 || !!env.active?.dry;
}

function buildEnvColumn(
  env: StatusEnvironment,
  i: number,
  specByBranch: Map<string, { autoMerge?: boolean }>,
): EnvColumn {
  const liveStatuses = env.active?.commitStatuses ?? [];
  const proposedStatuses = env.proposed?.commitStatuses ?? [];
  const liveSha = env.active?.dry?.sha;
  const proposedSha = env.proposed?.dry?.sha;
  const proposedDistinct =
    env.proposed?.dry && proposedSha && proposedSha !== liveSha ? env.proposed.dry : undefined;

  return {
    branch: env.branch,
    autoMerge: specByBranch.get(env.branch)?.autoMerge ?? false,
    color: LANE_COLORS[i % LANE_COLORS.length]!,
    liveCommit: env.active?.dry,
    liveStatuses,
    liveHealth: healthFromStatuses(liveStatuses),
    proposedCommit: proposedDistinct,
    proposedStatuses: proposedDistinct ? proposedStatuses : [],
    proposedHealth: proposedDistinct ? healthFromStatuses(proposedStatuses) : 'unknown',
    proposedPR: proposedDistinct ? env.pullRequest : undefined,
  };
}

function getRow(
  rowsById: Map<string, CommitRow>,
  commit: Commit | undefined,
  repoUrlFallback: string,
  pr?: PullRequest,
): CommitRow | null {
  const key = commitKey(commit);
  if (!key || !commit) return null;
  let row = rowsById.get(key);
  if (!row) {
    const ref = commit.references?.[0]?.commit;
    const refUrl = ref ? getCommitUrl(ref.repoURL ?? '', ref.sha ?? '') : '';
    row = {
      id: key,
      dryShaFull: commit.sha ?? '',
      dryShaShort: shortSha(commit.sha),
      subject: (commit.subject ?? '').trim() || '(no subject)',
      author: commit.author ? extractNameOnly(commit.author) : '—',
      body: commit.body ? extractBodyPreTrailer(commit.body) : undefined,
      prId: pr?.id,
      prUrl: pr?.url,
      refShaShort: ref?.sha ? shortSha(ref.sha) : undefined,
      refUrl: refUrl || undefined,
      repoUrl: commit.repoURL ?? repoUrlFallback,
      freshestAt: 0,
      earliestAt: 0,
      cells: {},
      hasLive: false,
      hasInFlight: false,
      hasFailed: false,
      hasNoop: false,
    };
    rowsById.set(key, row);
  }
  if (pr?.id && !row.prId) {
    row.prId = pr.id;
    row.prUrl = pr.url;
  }
  return row;
}

const cellRank: Record<CellKind, number> = {
  live: 6,
  'in-flight': 5,
  failed: 4,
  'was-here': 3,
  'no-op': 2,
  'no-changes': 1,
};

function setCell(row: CommitRow, branch: string, next: CellState) {
  const prev = row.cells[branch];
  if (!prev || cellRank[next.kind] >= cellRank[prev.kind]) {
    row.cells[branch] = next;
  }
}

type HistoryEntry = NonNullable<StatusEnvironment['history']>[number];

function wentLiveAt(entry: HistoryEntry | undefined): number | null {
  const raw = entry?.pullRequest?.prMergeTime ?? entry?.active?.dry?.commitTime;
  if (!raw) return null;
  const t = new Date(raw).getTime();
  return Number.isFinite(t) ? t : null;
}

function processHistory(rowsById: Map<string, CommitRow>, env: StatusEnvironment) {
  const branch = env.branch;
  const history = env.history ?? [];
  history.forEach((entry, idx) => {
    const commit = entry.active?.dry;
    if (!commit) return;
    const statuses = entry.active?.commitStatuses ?? [];
    const health = healthFromStatuses(statuses);
    const olderSha = history[idx + 1]?.active?.dry?.sha;
    const isNoop = !!commit.sha && !!olderSha && commit.sha === olderSha;
    const kind: CellKind = isNoop ? 'no-op' : health === 'failure' ? 'failed' : 'was-here';

    const row = getRow(rowsById, commit, '', entry.pullRequest);
    if (!row) return;

    const supersededById =
      idx > 0 ? (commitKey(history[idx - 1]?.active?.dry) ?? undefined) : undefined;

    const wentLive = wentLiveAt(entry);
    const replacer = idx > 0 ? history[idx - 1] : undefined;
    const replacedAt = wentLiveAt(replacer);
    const replacedAtRaw =
      replacer?.pullRequest?.prMergeTime ?? replacer?.active?.dry?.commitTime ?? undefined;
    const liveDurationMs =
      wentLive != null && replacedAt != null && replacedAt > wentLive
        ? replacedAt - wentLive
        : undefined;

    setCell(row, branch, {
      kind,
      commit,
      hydrated: entry.active?.hydrated,
      references: toReferenceCommits(commit),
      commitStatuses: statuses,
      health,
      pullRequest: entry.pullRequest,
      noopNote: isNoop
        ? `Same dry SHA as the previous entry, so ${branch} didn't change.`
        : undefined,
      supersededById,
      liveDurationMs,
      replacedAt: replacedAtRaw,
      at: commit.commitTime ?? entry.pullRequest?.prMergeTime ?? undefined,
    });
  });
}

function finalizeRow(row: CommitRow, envs: StatusEnvironment[]): CommitRow {
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
    times.push(0);
  }
  row.freshestAt = times.length ? Math.max(...times) : 0;
  row.earliestAt = times.length
    ? Math.min(...times.filter((t) => t > 0), ...(times.includes(0) ? [Infinity] : []))
    : 0;
  if (!Number.isFinite(row.earliestAt)) row.earliestAt = row.freshestAt;

  for (const e of envs) {
    if (!row.cells[e.branch]) {
      row.cells[e.branch] = {
        kind: 'no-changes',
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

  return row;
}

export function buildMatrix(strategy: PromotionStrategy): {
  envs: EnvColumn[];
  rows: CommitRow[];
} {
  const envs = (strategy.status?.environments ?? []).filter(envHasContent);

  const specByBranch = new Map<string, { autoMerge?: boolean }>();
  for (const e of strategy.spec.environments ?? []) specByBranch.set(e.branch, e);

  const envColumns: EnvColumn[] = envs.map((env, i) => buildEnvColumn(env, i, specByBranch));

  const rowsById = new Map<string, CommitRow>();

  envs.forEach((env) => {
    const branch = env.branch;
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedIsDistinct = !!proposedSha && proposedSha !== liveSha;

    if (env.active?.dry) {
      const statuses = env.active.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(rowsById, env.active.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'live';
        setCell(row, branch, {
          kind,
          commit: env.active.dry,
          hydrated: env.active.hydrated,
          references: toReferenceCommits(env.active.dry),
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          at: env.active.dry.commitTime ?? undefined,
        });
      }
    }

    if (proposedIsDistinct && env.proposed?.dry) {
      const statuses = env.proposed.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const row = getRow(rowsById, env.proposed.dry, '', env.pullRequest);
      if (row) {
        const kind: CellKind = health === 'failure' ? 'failed' : 'in-flight';
        setCell(row, branch, {
          kind,
          commit: env.proposed.dry,
          hydrated: env.proposed.hydrated,
          references: toReferenceCommits(env.proposed.dry),
          commitStatuses: statuses,
          health,
          pullRequest: env.pullRequest,
          isProposed: true,
          at: env.proposed.dry.commitTime ?? undefined,
        });
      }
    }

    processHistory(rowsById, env);
  });

  const rows = Array.from(rowsById.values()).map((row) => finalizeRow(row, envs));

  rows.sort((a, b) => b.freshestAt - a.freshestAt);

  return { envs: envColumns, rows };
}
