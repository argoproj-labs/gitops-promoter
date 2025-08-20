import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, formatDate } from './util';
import { getEnvironmentStatus , getPromotionStatus, getHealthStatus } from './getStatus';
import type { 
  CommitStatus, 
  Commit, 
  Environment, 
  PromotionStrategy, 
  Check, 
  EnrichedEnvDetails
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

// Helper function to extract reference commit data consistently
function extractReferenceCommitData(dryCommit: Commit): {
  sha: string;
  author: string;
  subject: string;
  body: string;
  date: string;
  url: string;
} {
  const referenceCommit = dryCommit.references && dryCommit.references[0]?.commit;
  
  if (!referenceCommit) {
    return {
      sha: '-',
      author: '-',
      subject: '-',
      body: '-',
      date: '-',
      url: ''
    };
  }
  
  const sha = referenceCommit.sha ? referenceCommit.sha.slice(0, 7) : '-';
  const author = referenceCommit.author ? extractNameOnly(referenceCommit.author) : '-';
  const subject = referenceCommit.subject || '-';
  const body = referenceCommit.body || '-';
  
  const date = referenceCommit.date ? formatDate(referenceCommit.date) : '-';
  const url = getCommitUrl(referenceCommit.repoURL || '', referenceCommit.sha || '');
  
  return { sha, author, subject, body, date, url };
}


function getEnvDetails(environment: Environment): EnrichedEnvDetails {
  const { active = {}, proposed = {}, pullRequest, history = [] } = environment;

  const branch = environment.branch || '';

  // Active Data - Use history[0] for commit detail, current active for status
  const activeEnv = history[0]?.active || active;
  const activeCommit = activeEnv.dry || {};
  const activeChecks = getChecks(active.commitStatuses || []);
  const activeChecksSummary = calculateHealthSummary(activeChecks);
  const activeReferenceData = extractReferenceCommitData(activeCommit);

  // Proposed data
  const proposedDry = proposed.dry || {};
  const proposedCommitStatuses = proposed.commitStatuses || [];
  const proposedChecks = getChecks(proposedCommitStatuses);
  const proposedChecksSummary = calculateHealthSummary(proposedChecks);
  const proposedReferenceData = extractReferenceCommitData(proposedDry);

  const promotionStatus = getEnvironmentStatus(environment);
  const historyWithPr = history.length > 0 ? history[0] : null;

  return {
    // Environment info
    branch,
    promotionStatus,

    // ACTIVE
    activeStatus: getHealthStatus(activeChecks),
    activePrUrl: historyWithPr?.pullRequest?.url || null,
    activePrNumber: historyWithPr?.pullRequest?.id ? parseInt(historyWithPr.pullRequest.id, 10) : null,
    activeCommitSubject: activeCommit.subject || '-',
    activeCommitMessage: extractBodyPreTrailer(activeCommit.body || '-'),
    activeCommitAuthor: extractNameOnly(activeCommit.author || '-'),
    activeCommitDate: activeCommit.commitTime ? formatDate(activeCommit.commitTime) : '-',
    activeCommitUrl: getCommitUrl(activeCommit.repoURL ?? '', activeCommit.sha ?? ''),
    activeSha: activeCommit.sha ? activeCommit.sha.slice(0, 7) : '-',
    activeReferenceCommitSubject: activeReferenceData.subject,
    activeReferenceCommitBody: activeReferenceData.body,
    activeReferenceCommitAuthor: activeReferenceData.author,
    activeReferenceCommitDate: activeReferenceData.date,
    activeReferenceCommitUrl: activeReferenceData.url,
    activeReferenceSha: activeReferenceData.sha,
    activeChecks,
    activeChecksSummary,

    // PROPOSED
    proposedStatus: proposedDry.sha && proposedDry.sha !== activeCommit.sha ? 'pending' : getHealthStatus(proposedChecks),
    prNumber: pullRequest?.id ? parseInt(pullRequest.id, 10) : null,
    prUrl: pullRequest?.url || null,
    proposedDryCommitSubject: proposedDry.subject || '-',
    proposedDryCommitBody: extractBodyPreTrailer(proposedDry.body || '-'),
    proposedDryCommitAuthor: extractNameOnly(proposedDry.author || '-'),
    proposedDryCommitDate: proposedDry.commitTime ? formatDate(proposedDry.commitTime) : '-',
    proposedDryCommitUrl: getCommitUrl(proposedDry.repoURL ?? '', proposedDry.sha ?? ''),
    proposedSha: proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-',
    proposedReferenceCommitSubject: proposedReferenceData.subject,
    proposedReferenceCommitBody: proposedReferenceData.body,
    proposedReferenceCommitAuthor: proposedReferenceData.author,
    proposedReferenceCommitDate: proposedReferenceData.date,
    proposedReferenceCommitUrl: proposedReferenceData.url,
    proposedReferenceSha: proposedReferenceData.sha,
    proposedChecks,
    proposedChecksSummary,
  };
}

export function enrichPromotionStrategy(ps: PromotionStrategy): EnrichedEnvDetails[] {
  if (!ps?.status?.environments) {
    return [];
  } 

  // Pass spec environments to getEnvDetails
  return ps.status.environments.map((environment: Environment) =>
    getEnvDetails(environment)
  );
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


