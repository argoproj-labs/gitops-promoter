import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, formatDate } from './util';
import { getEnvironmentStatus, getHealthStatus } from './getStatus';
import type {
  CommitStatus,
  Commit,
  Environment,
  PromotionStrategy,
  Check,
  EnrichedEnvDetails,
  PromotionPhase, ReferenceCommit
} from '../types/promotion';


function getChecks(commitStatuses: CommitStatus[]): Check[] {
  return commitStatuses.map((cs: CommitStatus) => ({
    name: cs.key,
    status: cs.phase || 'unknown',
    details: cs.details,
    url: cs.url
  }));
}

// Health check summary calculation functions
function calculateHealthSummary(checks: Check[]): { successCount: number; totalCount: number; shouldDisplay: boolean } {
  const totalCount = checks.length;
  const successCount = checks.filter(check => check.status === 'success').length;
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
  
  const date = referenceCommit.date ? formatDate(referenceCommit.date) : '-';
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
  const activeChecks = getChecks(index > 0 ? 
    history[index]?.active?.commitStatuses || [] : 
    active.commitStatuses || []
  );
  
  // In historical view, combine active and proposed checks for a single view
  const historicalChecks = index > 0 ? 
    getChecks([
      ...(history[index]?.active?.commitStatuses || []),
      ...(history[index]?.proposed?.commitStatuses || [])
    ]) : 
    activeChecks;
  const activeChecksSummary = calculateHealthSummary(historicalChecks);
  const activeReferenceData = extractReferenceCommitData(activeCommitInfo);
  
  // PROPOSED DATA
  const proposedDry = proposed.dry || {};
  const proposedChecks = getChecks(proposed.commitStatuses || []);
  const proposedChecksSummary = calculateHealthSummary(proposedChecks);
  const proposedReferenceData = extractReferenceCommitData(proposedDry);

  const promotionStatus = getEnvironmentStatus(environment);
  
  // Use PR data from selected history entry
  const selectedHistoryEntry = history[index] || history[0];
  const historyWithPr = selectedHistoryEntry?.pullRequest;

  // In historical view, proposed cards should only show status info, not commit details
  const isHistoric = index > 0;

  return {
    // Environment info
    branch,
    promotionStatus,

    // ACTIVE
    activeStatus: getHealthStatus(historicalChecks),
    activePrUrl: historyWithPr?.url || null,
    activePrNumber: historyWithPr?.id ? parseInt(historyWithPr.id, 10) : null,
    activeCommitSubject: activeCommitInfo.subject || '-',
    activeCommitMessage: extractBodyPreTrailer(activeCommitInfo.body || '-'),
    activeCommitAuthor: extractNameOnly(activeCommitInfo.author || '-'),
    activeCommitDate: activeCommitInfo.commitTime ? formatDate(activeCommitInfo.commitTime) : '-',
    activeCommitUrl: getCommitUrl(activeCommitInfo.repoURL ?? '', activeCommitInfo.sha ?? ''),
    activeSha: activeCommitInfo.sha ? activeCommitInfo.sha.slice(0, 7) : '-',
    activeReferenceCommit: activeReferenceData,
    activeReferenceCommitUrl: activeReferenceData ? getCommitUrl(activeCommitInfo.repoURL ?? '', activeReferenceData.sha ?? '') : null,
    activeChecks: historicalChecks,
    activeChecksSummary,

    // PROPOSED
    proposedStatus: isHistoric ? getHealthStatus(proposedChecks) : (proposedDry.sha && proposedDry.sha !== activeCommitInfo.sha ? 'pending' : getHealthStatus(proposedChecks)),
    prNumber: pullRequest?.id ? parseInt(pullRequest.id, 10) : null,
    prUrl: pullRequest?.url || null,
    proposedDryCommitSubject: proposedDry.subject || '-',
    proposedDryCommitBody: extractBodyPreTrailer(proposedDry.body || '-'),
    proposedDryCommitAuthor: extractNameOnly(proposedDry.author || '-'),
    proposedDryCommitDate: proposedDry.commitTime ? formatDate(proposedDry.commitTime) : '-',
    proposedDryCommitUrl: getCommitUrl(proposedDry.repoURL ?? '', proposedDry.sha ?? ''),
    proposedSha: proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-',
    proposedReferenceCommit: proposedReferenceData,
    proposedReferenceCommitUrl: proposedReferenceData ? getCommitUrl(proposedDry.repoURL ?? '', proposedReferenceData.sha ?? '') : null,
    proposedChecks,
    proposedChecksSummary,
  };
}

// Takes the PS objects (for dashboard)
export function enrichFromCRD(ps: PromotionStrategy, historyIndex: number = 0): EnrichedEnvDetails[] {
  if (!ps?.status?.environments) {
    return [];
  } 

  return ps.status.environments.map((environment: Environment) =>
    getEnvDetails(environment, historyIndex)
  );
}

// Takes the environments objects (for Card)
export function enrichFromEnvironments(environments: Environment[], historyIndex: number = 0): EnrichedEnvDetails[] {
  if (!environments) {
    return [];
  } 
  return environments.map((environment: Environment) =>
    getEnvDetails(environment, historyIndex)
  );
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
    return { total: 0, promoted: 0, pending: 0, failed: 0, overallStatus: 'unknown', displayText: '' };
  }

  const envs = ps.status.environments;
  let promoted = 0, pending = 0, failed = 0;

  // Count statuses
  for (const env of envs) {
    const status = getEnvironmentStatus(env);
    if (status === 'failure') failed++;
    else if (status === 'promoted') promoted++;
    else if (status === 'pending') pending++;
  }

  const total = envs.length;
  
  // Determine overall status
  const overallStatus = failed > 0 ? 'failure' : 
                       pending > 0 ? 'pending' : 
                       promoted === total ? 'promoted' : 'unknown';

  // E.g: 1/1 environments failed
  const displayText = failed > 0 ? `${failed}/${total} environments failed` :
                     pending > 0 ? `${pending}/${total} environments pending` :
                     promoted > 0 ? `${promoted}/${total} environments promoted` :
                     `${total}/${total} environments`;

  return { total, promoted, pending, failed, overallStatus, displayText };
}

//Wrappers
export function getPromotionPhase(ps: PromotionStrategy): PromotionPhase {
  return getPromotionStatus(ps).overallStatus;
}

export function getEnvironmentCountSummary(ps: PromotionStrategy): { total: number; promoted: number; summary: string } {
  const { total, promoted, displayText } = getPromotionStatus(ps);
  return { total, promoted, summary: displayText };
}

export type { 
  PromotionStrategy, 
  EnrichedEnvDetails, 
  PromotionPhase 
} from '../types/promotion';
