import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, timeAgo } from './util';
import { getEnvironmentStatus, getHealthStatus } from './getStatus';
import type {
  BranchCommitStatus,
  Commit,
  Environment,
  PromotionStrategy,
  Check,
  EnrichedEnvDetails,
  PromotionPhase,
  ReferenceCommit,
  RelativeTimeAgo,
} from '../types/promotion';

function getChecks(commitStatuses: BranchCommitStatus[]): Check[] {
  return commitStatuses.map((cs: BranchCommitStatus) => ({
    name: cs.key,
    status: cs.phase,
    description: cs.description,
    url: cs.url,
  }));
}

// Health check summary calculation functions
function calculateHealthSummary(checks: Check[]): {
  successCount: number;
  totalCount: number;
  shouldDisplay: boolean;
} {
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

function getEnvDetails(environment: Environment, index: number = 0): EnrichedEnvDetails {
  const { active = {}, proposed = {}, pullRequest, history = [] } = environment;
  const branch = environment.branch || '';

  //
  const activeHistory = history[index]?.active || active;
  const activeCommitInfo = activeHistory.dry || {};

  // Use active field for current view, history field for history view
  const activeChecks = getChecks(
    index > 0 ? history[index]?.active?.commitStatuses || [] : active.commitStatuses || [],
  );

  const activeChecksSummary = calculateHealthSummary(activeChecks);
  const activeReferenceData = extractReferenceCommitData(activeCommitInfo);

  // PROPOSED DATA - use historical proposed when viewing history
  const proposedSource = index > 0 ? history[index]?.proposed : proposed;
  const proposedDry = index > 0 ? proposedSource?.hydrated || {} : proposed.dry || {};
  const proposedChecks = getChecks(proposedSource?.commitStatuses || []);
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

  // Merge time for the live (index 0) active PR
  const liveMergeTimeStr = activePr?.prMergeTime || history[0]?.pullRequest?.prMergeTime || null;
  const activeMergeTimeAgo: RelativeTimeAgo | null = liveMergeTimeStr
    ? timeAgo(liveMergeTimeStr)
    : null;

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
    activeMergeTimeAgo,
  };
}

// Returns branch names whose proposed commit has not yet been hydrated to the
// newest dry commit. Envs with no proposed commit are not processing.
export function getProcessingEnvs(environments: Environment[]): Set<string> {
  const effectiveDrySha = (e: Environment) =>
    e.proposed?.note?.drySha || e.proposed?.dry?.sha || '';
  const hasProposedChange = (e: Environment) =>
    !!e.proposed?.dry?.sha && e.active?.dry?.sha !== e.proposed?.dry?.sha;

  let target = '',
    newest = -Infinity;
  for (const e of environments) {
    const sha = effectiveDrySha(e);
    if (!sha) continue;
    const t = Date.parse(e.proposed?.hydrated?.commitTime ?? '') || 0;
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
