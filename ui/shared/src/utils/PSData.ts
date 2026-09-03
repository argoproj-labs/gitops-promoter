import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, timeAgo } from './util';
import { getEnvironmentStatus, getHealthStatus } from './getStatus';
import type { components } from '../types/generated/view.gen';
import type {
  BranchCommitStatus,
  Commit,
  CommitStatusManager,
  CommitStatusManagerKind,
  EnrichedBranchCommitStatus,
  Environment,
  EnvironmentPullRequest,
  History,
  PromotionStrategy,
  Check,
  EnrichedEnvDetails,
  HealthSummaryResult,
  PrTooltip,
  PromotionPhase,
  ReferenceCommit,
  RelativeTimeAgo,
} from '../types/promotion';

export interface CommitStatusManagerBundle {
  timedCommitStatuses?: components['schemas']['TimedCommitStatus'][];
  gitCommitStatuses?: components['schemas']['GitCommitStatus'][];
  scheduledCommitStatuses?: components['schemas']['ScheduledCommitStatus'][];
  argoCDCommitStatuses?: components['schemas']['ArgoCDCommitStatus'][];
  webRequestCommitStatuses?: components['schemas']['WebRequestCommitStatus'][];
}

function findManager(
  key: string,
  branch: string,
  managers: CommitStatusManagerBundle,
): { kind: CommitStatusManagerKind; manager: CommitStatusManager } | undefined {
  for (const tcs of managers.timedCommitStatuses ?? []) {
    if (tcs.spec.key === key && tcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'TimedCommitStatus', manager: tcs };
    }
  }
  for (const gcs of managers.gitCommitStatuses ?? []) {
    if (gcs.spec.key === key && gcs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'GitCommitStatus', manager: gcs };
    }
  }
  for (const scs of managers.scheduledCommitStatuses ?? []) {
    if (scs.spec.key === key && scs.status?.environments?.some((e) => e.branch === branch)) {
      return { kind: 'ScheduledCommitStatus', manager: scs };
    }
  }
  for (const acs of managers.argoCDCommitStatuses ?? []) {
    if (acs.spec?.key === key) {
      return { kind: 'ArgoCDCommitStatus', manager: acs };
    }
  }
  for (const wrcs of managers.webRequestCommitStatuses ?? []) {
    if (wrcs.spec.key === key) {
      return { kind: 'WebRequestCommitStatus', manager: wrcs };
    }
  }
  return undefined;
}

function getChecks(
  commitStatuses: (BranchCommitStatus | EnrichedBranchCommitStatus)[],
  branch: string,
): Check[] {
  return commitStatuses.map((cs: BranchCommitStatus | EnrichedBranchCommitStatus) => {
    const enriched = cs as EnrichedBranchCommitStatus;
    return {
      name: cs.key,
      status: cs.phase,
      description: cs.description,
      url: cs.url,
      branch,
      kind: enriched.kind,
      manager: enriched.manager,
    };
  });
}

/**
 * Stamps `kind`/`manager` onto every commit-status entry nested in `ps.status.environments`
 * (active, proposed, and each history entry's active/proposed) using the same join semantics
 * as `findManager`. Returns a new `PromotionStrategy`-shaped value; does not mutate `ps`.
 */
export function mergeCommitStatusManagers(
  ps: PromotionStrategy,
  managers: CommitStatusManagerBundle,
): PromotionStrategy {
  if (!ps.status?.environments) {
    return ps;
  }

  const enrichStatuses = (
    commitStatuses: BranchCommitStatus[] | undefined,
    branch: string,
  ): EnrichedBranchCommitStatus[] | undefined =>
    commitStatuses?.map((cs) => {
      const match = findManager(cs.key, branch, managers);
      return { ...cs, kind: match?.kind, manager: match?.manager };
    });

  const environments: Environment[] = ps.status.environments.map((environment: Environment) => {
    const branch = environment.branch || '';

    const active = environment.active
      ? { ...environment.active, commitStatuses: enrichStatuses(environment.active.commitStatuses, branch) }
      : environment.active;
    const proposed = environment.proposed
      ? {
          ...environment.proposed,
          commitStatuses: enrichStatuses(environment.proposed.commitStatuses, branch),
        }
      : environment.proposed;

    const history: History[] | undefined = environment.history?.map((entry: History) => ({
      ...entry,
      active: entry.active
        ? { ...entry.active, commitStatuses: enrichStatuses(entry.active.commitStatuses, branch) }
        : entry.active,
      proposed: entry.proposed
        ? {
            ...entry.proposed,
            commitStatuses: enrichStatuses(entry.proposed.commitStatuses, branch),
          }
        : entry.proposed,
    }));

    return { ...environment, active, proposed, history };
  });

  return { ...ps, status: { ...ps.status, environments } };
}

// Health check summary calculation functions
function calculateHealthSummary(checks: Check[]): HealthSummaryResult {
  const totalCount = checks.length;
  const successCount = checks.filter((check) => check.status === 'success').length;
  const shouldDisplay = totalCount > 0;
  return { successCount, totalCount, shouldDisplay };
}

// Extract reference commit data
function extractReferenceCommitData(dryCommit: Commit): null | ReferenceCommit {
  const referenceCommit = dryCommit.references && dryCommit.references[0]?.commit;

  if (!referenceCommit) {
    return null;
  }

  const sha = referenceCommit.sha ? referenceCommit.sha.slice(0, 7) : '-';
  const author = referenceCommit.author ? extractNameOnly(referenceCommit.author) : '-';
  const subject = referenceCommit.subject || '-';
  const body = referenceCommit.body || '-';

  // Pass RFC 3339 through for TimeAgo; do not formatDate here.
  const date = referenceCommit.date;
  const url = getCommitUrl(referenceCommit.repoURL || '', referenceCommit.sha || '');

  return { sha, author, subject, body, date, url };
}

function derivePrTooltip(pr: EnvironmentPullRequest | null): PrTooltip | null {
  if (!pr) {
    return null;
  }
  const state = pr.state || '';
  const isMerged = state === 'merged' || (!state && !!pr.prMergeTime);
  if (isMerged) {
    return { status: 'merged', label: 'merged', time: pr.prMergeTime ?? null };
  }
  if (state === 'closed') {
    return { status: 'closed', label: 'closed', time: null };
  }
  if (pr.externallyMergedOrClosed && !pr.prMergeTime) {
    return { status: 'closed', label: 'closed or merged externally', time: null };
  }
  return pr.prCreationTime ? { status: 'opened', label: 'opened', time: pr.prCreationTime } : null;
}

function deriveActivePrTooltip(pr: EnvironmentPullRequest | null): PrTooltip | null {
  if (pr && pr.state === 'merged') {
    return { status: 'merged', label: 'merged', time: pr.prMergeTime ?? null };
  }
  if (pr && pr.externallyMergedOrClosed) {
    return { status: 'merged', label: 'merged externally', time: pr.prMergeTime ?? null };
  }
  return derivePrTooltip(pr);
}

function getEnvDetails(environment: Environment, index: number = 0): EnrichedEnvDetails {
  const { active = {}, proposed = {}, pullRequest, history = [] } = environment;
  const branch = environment.branch || '';

  //
  const activeHistory = history[index]?.active || active;
  const activeCommitInfo = activeHistory.dry || {};

  // Use active field for current view, history field for history view
  const activeChecks = getChecks(
    index > 0 ? history[index]?.active?.commitStatuses || [] : active.commitStatuses || [],
    branch,
  );

  const activeChecksSummary = calculateHealthSummary(activeChecks);
  const activeReferenceData = extractReferenceCommitData(activeCommitInfo);

  // PROPOSED DATA - use historical proposed when viewing history
  const proposedSource = index > 0 ? history[index]?.proposed : proposed;
  const proposedDry = index > 0 ? proposedSource?.hydrated || {} : proposed.dry || {};
  const proposedChecks = getChecks(proposedSource?.commitStatuses || [], branch);
  const proposedChecksSummary = calculateHealthSummary(proposedChecks);
  const proposedReferenceData = extractReferenceCommitData(proposedDry);

  const promotionStatus = getEnvironmentStatus(environment);

  // Use PR data from the selected history entry only; live PR fallbacks apply at index 0.
  const entryPr = history[index]?.pullRequest ?? null;
  const historyWithPr = entryPr?.id ? entryPr : null;

  // For the live active badge, fall back to environment.pullRequest when state is merged
  // and history[0] has no PR data (e.g. externally merged PRs)
  const mergedEnvPr =
    pullRequest?.id && (pullRequest.state === 'merged' || pullRequest.externallyMergedOrClosed)
      ? pullRequest
      : null;
  const activePr = index > 0 ? historyWithPr : (historyWithPr ?? mergedEnvPr);

  // Resolve merge time: prefer prMergeTime, fall back to hydrated commitTime
  let historyMergeTimeAgo: RelativeTimeAgo | null = null;
  if (index > 0) {
    const mergeTimeStr =
      history[index]?.pullRequest?.prMergeTime ||
      history[index]?.active?.hydrated?.commitTime ||
      null;
    historyMergeTimeAgo = mergeTimeStr ? timeAgo(mergeTimeStr) : null;
  }

  // In historical view, proposed cards should only show status info, not commit details
  const isHistoric = index > 0;

  return {
    // Environment info
    branch,
    promotionStatus,

    // ACTIVE
    activeStatus: getHealthStatus(activeChecks),
    activePrUrl: activePr?.url || null,
    activePrNumber: activePr?.id ? parseInt(activePr.id, 10) : null,
    activePrCreationTime: activePr?.prCreationTime || null,
    activePrMergeTime: activePr?.prMergeTime || null,
    activePrState: activePr?.state ?? null,
    activePrTooltip: deriveActivePrTooltip(activePr),
    activeCommitSubject: activeCommitInfo.subject || '-',
    activeCommitMessage: extractBodyPreTrailer(activeCommitInfo.body || '-'),
    activeCommitAuthor: extractNameOnly(activeCommitInfo.author || '-'),
    activeCommitDate: activeCommitInfo.commitTime || '',
    activeCommitUrl: getCommitUrl(activeCommitInfo.repoURL ?? '', activeCommitInfo.sha ?? ''),
    activeSha: activeCommitInfo.sha ? activeCommitInfo.sha.slice(0, 7) : '-',
    activeReferenceCommit: activeReferenceData,
    activeReferenceCommitUrl: activeReferenceData ? (activeReferenceData.url ?? null) : null,
    activeChecks,
    activeChecksSummary,

    // PROPOSED
    proposedStatus: isHistoric
      ? getHealthStatus(proposedChecks)
      : proposedDry.sha && proposedDry.sha !== activeCommitInfo.sha
        ? 'pending'
        : getHealthStatus(proposedChecks),
    prNumber: pullRequest?.id ? parseInt(pullRequest.id, 10) : null,
    prUrl: pullRequest?.url || null,
    prTooltip: derivePrTooltip(pullRequest ?? null),
    proposedDryCommitSubject: proposedDry.subject || '-',
    proposedDryCommitBody: extractBodyPreTrailer(proposedDry.body || '-'),
    proposedDryCommitAuthor: extractNameOnly(proposedDry.author || '-'),
    proposedDryCommitDate: proposedDry.commitTime || '',
    proposedDryCommitUrl: getCommitUrl(proposedDry.repoURL ?? '', proposedDry.sha ?? ''),
    proposedSha: proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-',
    proposedReferenceCommit: proposedReferenceData,
    proposedReferenceCommitUrl: proposedReferenceData ? (proposedReferenceData.url ?? null) : null,
    proposedChecks,
    proposedChecksSummary,

    // History
    historyMergeTimeAgo,
  };
}

// Returns branch names whose proposed commit has not yet been hydrated to the
// newest dry commit. Envs with no proposed commit are not processing.
export function getProcessingEnvs(environments: Environment[]): Set<string> {
  const effectiveDrySha = (e: Environment) => e.proposed.note?.drySha || e.proposed.dry?.sha || '';
  const hasProposedChange = (e: Environment) =>
    !!e.proposed.dry?.sha && e.active.dry?.sha !== e.proposed.dry.sha;

  let target = '',
    newest = -Infinity;
  for (const e of environments) {
    const sha = effectiveDrySha(e);
    if (!sha) continue;
    const t = Date.parse(e.proposed.hydrated?.commitTime ?? '') || 0;
    if (!target || t > newest) {
      target = sha;
      newest = t;
    }
  }
  if (!target) return new Set();
  return new Set(
    environments
      .filter((e) => hasProposedChange(e) && effectiveDrySha(e) !== target)
      .map((e) => e.branch),
  );
}

// Takes the PS objects (for dashboard)
export function enrichFromCRD(
  ps: PromotionStrategy,
  historyIndex: number = 0,
): EnrichedEnvDetails[] {
  if (!ps.status?.environments) {
    return [];
  }

  return ps.status.environments.map((environment: Environment) =>
    getEnvDetails(environment, historyIndex),
  );
}

// Takes the environments objects (for Card)
export function enrichFromEnvironments(
  environments: Environment[],
  historyIndex: number = 0,
): EnrichedEnvDetails[] {
  return environments.map((environment: Environment) => getEnvDetails(environment, historyIndex));
}

// Get overall promotion status and counts
export function getPromotionStatus(ps: PromotionStrategy): {
  total: number;
  promoted: number;
  pending: number;
  failed: number;
  overallStatus: PromotionPhase;
  displayText: string;
} {
  if (!ps.status?.environments) {
    return {
      total: 0,
      promoted: 0,
      pending: 0,
      failed: 0,
      overallStatus: 'unknown',
      displayText: '',
    };
  }

  const envs = ps.status.environments;
  let promoted = 0,
    pending = 0,
    failed = 0;

  // Count statuses
  for (const env of envs) {
    const status = getEnvironmentStatus(env);
    if (status === 'failure') failed++;
    else if (status === 'promoted') promoted++;
    else if (status === 'pending') pending++;
  }

  const total = envs.length;

  // Determine overall status
  const overallStatus =
    failed > 0 ? 'failure' : pending > 0 ? 'pending' : promoted === total ? 'promoted' : 'unknown';

  // E.g: 1/1 environments failed
  const displayText =
    failed > 0
      ? `${failed}/${total} environments failed`
      : pending > 0
        ? `${pending}/${total} environments pending`
        : promoted > 0
          ? `${promoted}/${total} environments promoted`
          : `${total}/${total} environments`;

  return { total, promoted, pending, failed, overallStatus, displayText };
}

//Wrappers
export function getPromotionPhase(ps: PromotionStrategy): PromotionPhase {
  return getPromotionStatus(ps).overallStatus;
}

export function getEnvironmentCountSummary(ps: PromotionStrategy): {
  total: number;
  promoted: number;
  summary: string;
} {
  const { total, promoted, displayText } = getPromotionStatus(ps);
  return { total, promoted, summary: displayText };
}

export type { PromotionStrategy, EnrichedEnvDetails, PromotionPhase } from '../types/promotion';
