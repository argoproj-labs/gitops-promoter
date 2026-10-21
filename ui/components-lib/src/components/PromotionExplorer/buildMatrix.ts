import { extractNameOnly, extractBodyPreTrailer, getCommitUrl } from '@shared/utils/util';
import type {
  Commit,
  Environment,
  PromotionStrategy,
  PullRequest,
  ReferenceCommit,
} from '@shared/types/promotion';
import { LANE_COLORS } from './types';
import type { CellKind, CellState, CommitRow, EnvColumn } from './types';
import { healthFromStatuses, shortSha, commitKey } from './helpers';
import { proposedIsBlocked } from '@shared/utils/environments';

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

// The per-environment shape the matrix renders: the generated EnvironmentStatus plus the
// history re-projected from the ChangeTransferPolicyHistory resources by the store adapters.
type StatusEnvironment = Environment;

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
  const pr = blockedEnvPullRequest(env);

  return {
    branch: env.branch,
    changeTransferPolicyName: env.changeTransferPolicyName,
    instanceId: env.instanceId,
    autoMerge: specByBranch.get(env.branch)?.autoMerge ?? false,
    color: LANE_COLORS[i % LANE_COLORS.length]!,
    liveCommit: env.active?.dry,
    liveStatuses,
    liveHealth: healthFromStatuses(liveStatuses),
    proposedCommit: proposedDistinct,
    proposedStatuses: proposedDistinct ? proposedStatuses : [],
    proposedHealth: proposedDistinct ? healthFromStatuses(proposedStatuses) : 'unknown',
    proposedPR: proposedDistinct ? pr : undefined,
  };
}

// While a RestoreActiveCommit blocks the environment, status.pullRequest is the promotion that landed
// the commit the restore moved off the active branch. That merged pull request belongs on the
// blocked commit. An open one belongs on a newer proposed commit that is waiting out the block.
// A merged pull request on any other proposed commit is a previous promotion, not this commit's.
function blockedEnvPullRequest(env: StatusEnvironment): PullRequest | undefined {
  if (!env.restoreActiveCommit) return env.pullRequest;
  if (env.pullRequest?.state === 'open' || proposedIsBlocked(env)) return env.pullRequest;
  return undefined;
}

// status.pullRequest is the pull request the controller is tracking right now. While a newer
// commit is proposed, that is the open promotion, not the one that landed the commit still
// running. The live commit's pull request is on the history entry for the active tip. Until
// history catches up to a merge, a merged status.pullRequest is that same promotion.
function liveCommitPullRequest(env: StatusEnvironment): PullRequest | undefined {
  const fromHistory = historyPullRequestForActive(env);
  if (fromHistory?.id) return fromHistory;

  const pr = env.pullRequest;
  if (pr?.id && (pr.state === 'merged' || pr.externallyMergedOrClosed)) return pr;
  return undefined;
}

function historyPullRequestForActive(env: StatusEnvironment): PullRequest | undefined {
  const hydrated = env.active?.hydrated?.sha;
  const dry = env.active?.dry?.sha;
  if (!hydrated && !dry) return undefined;
  for (const entry of env.history ?? []) {
    // A restore copies the restored version's pull request; that promotion already has a row.
    if (entry.restoredFrom) continue;
    const sameTip = hydrated
      ? entry.active?.hydrated?.sha === hydrated
      : entry.active?.dry?.sha === dry;
    if (!sameTip) continue;
    return entry.pullRequest?.id ? entry.pullRequest : undefined;
  }
  return undefined;
}

function getRow(
  rowsById: Map<string, CommitRow>,
  commit: Commit | undefined,
  repoUrlFallback: string,
  pr?: PullRequest,
  keyOverride?: string,
): CommitRow | null {
  const key = keyOverride ?? commitKey(commit);
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
  live: 7,
  'in-flight': 6,
  failed: 5,
  restored: 4,
  'was-here': 3,
  'no-op': 2,
  'unknown-history': 1,
  'no-changes': 1,
};

function cellAtMs(cell: CellState): number {
  if (!cell.at) return 0;
  const t = new Date(cell.at).getTime();
  return Number.isFinite(t) ? t : 0;
}

function setCell(row: CommitRow, branch: string, next: CellState) {
  const prev = row.cells[branch];
  if (!prev) {
    row.cells[branch] = next;
    return;
  }
  const rankDiff = cellRank[next.kind] - cellRank[prev.kind];
  // History is walked newest-first, so an older entry of the same kind must not
  // overwrite a newer one. That happens when one environment restores the same dry
  // commit twice and both entries share a row.
  if (rankDiff > 0 || (rankDiff === 0 && cellAtMs(next) >= cellAtMs(prev))) {
    row.cells[branch] = next;
  }
}

type HistoryEntry = NonNullable<StatusEnvironment['history']>[number];

/**
 * When the entry's commit landed on the active branch. A restore keeps the restored version's
 * pull request, merge time included, so its own time is the restore commit's commit time.
 */
function landedAtRaw(entry: HistoryEntry | undefined): string | undefined {
  const restoreTime = entry?.restoredFrom ? entry.active?.hydrated?.commitTime : undefined;
  if (restoreTime) return restoreTime;
  return entry?.pullRequest?.prMergeTime ?? entry?.active?.dry?.commitTime ?? undefined;
}

function wentLiveAt(entry: HistoryEntry | undefined): number | null {
  const raw = landedAtRaw(entry);
  if (!raw) return null;
  const t = new Date(raw).getTime();
  return Number.isFinite(t) ? t : null;
}

/**
 * Row key for a restore entry, or null when the entry is an ordinary promotion.
 *
 * A restore reuses the restored version's tree, so its dry sha repeats the row the
 * original promotion already owns. The same dry key plus `-restore` keeps that row
 * distinct, and every environment restored back to that commit shares it. Each
 * environment's own restore commit stays on the cell.
 */
function restoreRowKey(entry: HistoryEntry | undefined): string | null {
  if (!entry?.restoredFrom) return null;
  const key = commitKey(entry.active?.dry);
  if (!key) return null;
  return `${key}-restore`;
}

/**
 * Mark a restore row.
 *
 * The row keeps the restored version's dry identity — same subject, same dry sha as
 * the original promotion's row — because two rows carrying identical dry data is what
 * shows the branch moved back to an earlier version. The amber cell fill is what
 * marks this row as the restore.
 */
function applyRestoreIdentity(row: CommitRow, entry: HistoryEntry) {
  row.restoredFrom = entry.restoredFrom;
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
    const restoreKey = restoreRowKey(entry);
    const kind: CellKind = restoreKey
      ? 'restored'
      : isNoop
        ? 'no-op'
        : health === 'failure'
          ? 'failed'
          : 'was-here';

    // A restore copies the restored version's history note, so its pull request id is that
    // earlier promotion's, not a pull request that created the restore. Leave it off this row;
    // the promotion that the restore moved off the branch is attached to the blocked commit.
    const row = getRow(
      rowsById,
      commit,
      '',
      restoreKey ? undefined : entry.pullRequest,
      restoreKey ?? undefined,
    );
    if (!row) return;
    if (restoreKey) applyRestoreIdentity(row, entry);

    const supersededById =
      idx > 0 ? (commitKey(history[idx - 1]?.active?.dry) ?? undefined) : undefined;

    const wentLive = wentLiveAt(entry);
    const replacer = idx > 0 ? history[idx - 1] : undefined;
    const replacedAt = wentLiveAt(replacer);
    const replacedAtRaw = landedAtRaw(replacer);
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
      pullRequest: restoreKey ? undefined : entry.pullRequest,
      restoredFrom: entry.restoredFrom,
      restoreUnblockedAt: entry.restoreUnblockedAt,
      noopNote: isNoop
        ? `Same dry SHA as the previous entry, so ${branch} didn't change.`
        : undefined,
      supersededById,
      liveDurationMs,
      replacedAt: replacedAtRaw,
      // A restore's dry commit predates the restore itself, so its own timestamp would
      // sort the row back next to the original promotion. The restore's time is what
      // places the row at the top.
      at: restoreKey
        ? landedAtRaw(entry)
        : (commit.commitTime ?? entry.pullRequest?.prMergeTime ?? undefined),
    });
  });
}

// The CRD caps per-environment history at 5 entries. A commit older than a capped
// env's oldest surviving entry may have run there but its record was dropped, so such
// cells render as 'unknown-history' rather than claiming 'no-changes'.
const HISTORY_CAP = 5;

interface EnvHorizon {
  oldestKnownAt: number;
  truncated: boolean;
}

function computeHorizons(envs: StatusEnvironment[]): Map<string, EnvHorizon> {
  const horizons = new Map<string, EnvHorizon>();
  for (const env of envs) {
    const times: number[] = [];
    const push = (raw?: string) => {
      if (!raw) return;
      const t = new Date(raw).getTime();
      if (Number.isFinite(t)) times.push(t);
    };
    push(env.active?.dry?.commitTime);
    for (const h of env.history ?? []) push(h.active?.dry?.commitTime);
    horizons.set(env.branch, {
      oldestKnownAt: times.length ? Math.min(...times) : Infinity,
      truncated: (env.history?.length ?? 0) >= HISTORY_CAP,
    });
  }
  return horizons;
}

function finalizeRow(
  row: CommitRow,
  envs: StatusEnvironment[],
  horizons: Map<string, EnvHorizon>,
): CommitRow {
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
  // A restore row's dry commit is older than the restore, and the row header reports
  // earliestAt as when the commit was "introduced". For a restore the meaningful time
  // is the restore itself, so collapse the range onto it.
  if (row.restoredFrom) row.earliestAt = row.freshestAt;

  const rowCommitAt = row.freshestAt;

  for (const e of envs) {
    if (!row.cells[e.branch]) {
      const horizon = horizons.get(e.branch);
      const agedOut =
        !!horizon &&
        horizon.truncated &&
        rowCommitAt > 0 &&
        Number.isFinite(horizon.oldestKnownAt) &&
        rowCommitAt < horizon.oldestKnownAt;
      row.cells[e.branch] = {
        kind: agedOut ? 'unknown-history' : 'no-changes',
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
  const envs: StatusEnvironment[] = (strategy.status?.environments ?? []).filter(envHasContent);

  const specByBranch = new Map<string, { autoMerge?: boolean }>();
  for (const e of strategy.spec.environments ?? []) specByBranch.set(e.branch, e);

  const envColumns: EnvColumn[] = envs.map((env, i) => buildEnvColumn(env, i, specByBranch));

  const rowsById = new Map<string, CommitRow>();

  envs.forEach((env) => {
    const branch = env.branch;
    const liveSha = env.active?.dry?.sha;
    const proposedSha = env.proposed?.dry?.sha;
    const proposedIsDistinct = !!proposedSha && proposedSha !== liveSha;

    // When the active tip is itself a restore commit, the live cell belongs on that
    // restore's row. Keying it by dry sha instead would light up the original
    // promotion's row and leave the restore row looking superseded.
    const restoreByHydrated = new Map<string, HistoryEntry>();
    for (const entry of env.history ?? []) {
      const hydratedSha = entry.active?.hydrated?.sha;
      if (restoreRowKey(entry) && hydratedSha) restoreByHydrated.set(hydratedSha, entry);
    }
    const activeRestore = env.active?.hydrated?.sha
      ? restoreByHydrated.get(env.active.hydrated.sha)
      : undefined;
    const activeRestoreKey = activeRestore
      ? (restoreRowKey(activeRestore) ?? undefined)
      : undefined;

    if (env.active?.dry) {
      const statuses = env.active.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      // The restore commit is written directly onto the active branch, so the policy's current
      // pull request is not its. That pull request stays on the commit it promoted.
      const livePullRequest = activeRestore ? undefined : liveCommitPullRequest(env);
      const row = getRow(rowsById, env.active.dry, '', livePullRequest, activeRestoreKey);
      if (row) {
        if (activeRestore) applyRestoreIdentity(row, activeRestore);
        const kind: CellKind = health === 'failure' ? 'failed' : 'live';
        setCell(row, branch, {
          kind,
          commit: env.active.dry,
          hydrated: env.active.hydrated,
          references: toReferenceCommits(env.active.dry),
          commitStatuses: statuses,
          health,
          pullRequest: livePullRequest,
          isLive: true,
          // A live restore outranks the history entry that describes it, so the restore's
          // marker and timestamp have to be carried here or they are lost and the row
          // sorts by the restored version's original (older) commit time.
          restoredFrom: activeRestore?.restoredFrom,
          restoreUnblockedAt: activeRestore?.restoreUnblockedAt,
          at: activeRestore ? landedAtRaw(activeRestore) : (env.active.dry.commitTime ?? undefined),
        });
      }
    }

    if (proposedIsDistinct && env.proposed?.dry) {
      const statuses = env.proposed.commitStatuses ?? [];
      const health = healthFromStatuses(statuses);
      const blockName = env.restoreActiveCommit?.name;
      // Durable across RestoreActiveCommit deletion: blockedDrySha from the CR, or from history
      // when the active tip is still the restore that moved this dry SHA off.
      const blocked = proposedIsBlocked(env);
      const pullRequest = blockedEnvPullRequest(env);
      const row = getRow(rowsById, env.proposed.dry, '', pullRequest);
      if (row) {
        const kind: CellKind =
          !blockName && !blocked && health === 'failure' ? 'failed' : 'in-flight';
        setCell(row, branch, {
          kind,
          commit: env.proposed.dry,
          hydrated: env.proposed.hydrated,
          references: toReferenceCommits(env.proposed.dry),
          commitStatuses: statuses,
          health,
          pullRequest,
          isProposed: true,
          restoreActiveCommit: blockName,
          blockedByRestoreActiveCommit: blocked || undefined,
          at: env.proposed.dry.commitTime ?? undefined,
        });
      }
    }

    processHistory(rowsById, env);
  });

  const horizons = computeHorizons(envs);
  const rows = Array.from(rowsById.values()).map((row) => finalizeRow(row, envs, horizons));

  rows.sort((a, b) => b.freshestAt - a.freshestAt);

  return { envs: envColumns, rows };
}
