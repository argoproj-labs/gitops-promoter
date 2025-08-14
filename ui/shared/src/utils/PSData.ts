import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, formatDate } from './util';
import type { 
  CommitStatus, 
  Commit, 
  Environment, 
  PromotionStrategy, 
  Check, 
  EnrichedEnvDetails,
  PromotionPhase 
} from '../types/promotion';


//TODO: HOW SHOULD WE HANDLE PROPOSED CARDS DISAPPEARING?
function getEnvironmentStatus(env: Environment): 'pending' | 'promoted' | 'failure' | 'unknown' {
  const { active = {}, proposed = {} } = env;
  
  const proposedSha = proposed.dry?.sha;
  const activeSha = active.dry?.sha;
  
  // Get checks
  const proposedChecks = proposed.commitStatuses || [];
  const activeChecks = active.commitStatuses || [];
  
  // Check for failures in any checks (proposed or active)
  if (proposedChecks.some((cs: CommitStatus) => cs.phase === 'failure') ||
      activeChecks.some((cs: CommitStatus) => cs.phase === 'failure')) {
    return 'failure';
  }
  
  // Pending (PR OPEN) - if proposed SHA is different from active SHA
  if (proposedSha && proposedSha !== activeSha) {
    return 'pending';
  }
  
  // Promoted (PR MERGED && ACTIVE CHECKS IN PROGRESS)
  if (proposedSha === activeSha) {
    return 'promoted';
  }
  
  return 'unknown';
}

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

function getEnvDetails(environment: Environment, specEnvs: { branch: string; autoMerge?: boolean }[]): EnrichedEnvDetails {
  const { active = {}, proposed = {}, pullRequest, history = [] } = environment;

  const branch = environment.branch || '';

  // Use history[0] for active data, fallback to current active
  const activeData = history[0]?.active || active;
  const commitStatuses = activeData.commitStatuses || [];
  const phase = commitStatuses[0]?.phase || 'unknown';

  // Active data
  const dry = activeData.dry || {};
  const activeChecks = getChecks(commitStatuses);
  const activeChecksSummary = calculateHealthSummary(activeChecks);
  const activeReferenceData = extractReferenceCommitData(dry);

  // Proposed data
  const proposedDry = proposed.dry || {};
  const proposedCommitStatuses = proposed.commitStatuses || [];
  const proposedChecks = getChecks(proposedCommitStatuses);
  const proposedChecksSummary = calculateHealthSummary(proposedChecks);
  const proposedReferenceData = extractReferenceCommitData(proposedDry);

  // Determine promotion status
  const promotionStatus = getEnvironmentStatus(environment);

  const historyWithPr = history.length > 0 ? history[0] : null;

  return {
    
    // Environment info
    branch,
    phase,
    promotionStatus,
    
    // Active commits
    drySha: dry.sha ? dry.sha.slice(0, 7) : '-',
    dryCommitAuthor: extractNameOnly(dry.author || '-'),
    dryCommitSubject: dry.subject || '-',
    dryCommitMessage: extractBodyPreTrailer(dry.body || '-'),
    dryCommitDate: dry.commitTime ? formatDate(dry.commitTime) : '-',
    dryCommitUrl: getCommitUrl(dry.repoURL ?? '', dry.sha ?? ''),
    activeChecks,
    activeChecksSummary,
    activePrUrl: historyWithPr?.pullRequest?.url || null,
    activePrNumber: historyWithPr?.pullRequest?.id ? parseInt(historyWithPr.pullRequest.id, 10) : null,
    
    referenceSha: activeReferenceData.sha,
    referenceCommitAuthor: activeReferenceData.author,
    referenceCommitSubject: activeReferenceData.subject,
    referenceCommitDate: activeReferenceData.date,
    referenceCommitUrl: activeReferenceData.url,
    referenceCommitBody: activeReferenceData.body,
    
    // Proposed commits
    proposedSha: proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-',
    prNumber: pullRequest?.id ? parseInt(pullRequest.id, 10) : null,
    prUrl: pullRequest?.url || null,
    proposedDryCommitAuthor: extractNameOnly(proposedDry.author || '-'),
    proposedDryCommitSubject: proposedDry.subject || '-',
    proposedDryCommitBody: extractBodyPreTrailer(proposedDry.body || '-'),
    proposedDryCommitDate: proposedDry.commitTime ? formatDate(proposedDry.commitTime) : '-',
    proposedDryCommitUrl: getCommitUrl(proposedDry.repoURL ?? '', proposedDry.sha ?? ''),
    proposedChecks,
    proposedChecksSummary,

    proposedReferenceSha: proposedReferenceData.sha,
    proposedReferenceCommitAuthor: proposedReferenceData.author,
    proposedReferenceCommitSubject: proposedReferenceData.subject,
    proposedReferenceCommitDate: proposedReferenceData.date,
    proposedReferenceCommitUrl: proposedReferenceData.url,
    proposedReferenceCommitBody: proposedReferenceData.body,
  };
}

export function enrichPromotionStrategy(ps: PromotionStrategy): EnrichedEnvDetails[] {
  if (!ps?.status?.environments) {
    return [];
  } 

  // Pass spec environments to getEnvDetails
  return ps.status.environments.map((environment: Environment) =>
    getEnvDetails(environment, ps.spec?.environments || [])
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


