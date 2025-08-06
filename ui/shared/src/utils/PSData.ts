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
function getEnvironmentStatus(env: Environment): 'pending' | 'success' | 'failure' | 'unknown' {
  const { active = {}, proposed = {}, pullRequest } = env;
  
  // Check for failures first (any check is failure)
  if (active.commitStatuses?.some(cs => cs.phase === 'failure')) {
    return 'failure';
  }
  
  // If there's a pull request -> PENDING
  if (pullRequest) {
    return 'pending';
  }
  
  // If no pull request and active SHA = proposed SHA -> PROMOTED
  if (proposed.dry?.sha === active.dry?.sha && proposed.dry?.sha) {
    return 'success';
  }
  
  // If no pull request but different SHAs -> UNKNOWN
  if (proposed.dry?.sha !== active.dry?.sha && proposed.dry?.sha) {
    return 'unknown';
  }

  return 'unknown';
}

function getProposedChecks(commitStatuses: CommitStatus[]): Check[] {
  return commitStatuses.map((cs: CommitStatus) => ({
    name: cs.key,
    status: cs.phase || 'unknown',
    details: cs.details,
    url: cs.url
  }));
}

function getActiveChecks(commitStatuses: CommitStatus[]): Check[] {
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
  const { active = {}, proposed = {}, pullRequest } = environment;

  const branch = environment.branch || '';

  // Phase
  const commitStatuses = active.commitStatuses || [];
  const phase = commitStatuses[0]?.phase || 'unknown';

    //Dry Commit (Active)
    const dry = active.dry || {};
    const drySha = dry.sha ? dry.sha.slice(0, 7) : '-';
    const dryCommitAuthor = extractNameOnly(dry.author || '-');
    const dryCommitSubject = dry.subject || '-';
    const dryCommitMessage = extractBodyPreTrailer(dry.body || '-');
    const dryCommitUrl = getCommitUrl(dry.repoURL ?? '', dry.sha ?? '');

    //Active Checks
    const activeChecks = getActiveChecks(commitStatuses);
    const activeChecksSummary = calculateHealthSummary(activeChecks);

    // Code Commits from references[0].commit (ACTIVE)
    const activeReferenceData = extractReferenceCommitData(dry);
    const referenceSha = activeReferenceData.sha;
    const referenceCommitAuthor = activeReferenceData.author;
    const referenceCommitSubject = activeReferenceData.subject;
    const referenceCommitBody = activeReferenceData.body;
    const referenceCommitDate = activeReferenceData.date;
    const referenceCommitUrl = activeReferenceData.url;


    // Proposed Dry
    const proposedDry = proposed.dry || {};
    const proposedSha = proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-';
    const proposedCommitStatuses = proposed.commitStatuses || [];
    const proposedDryCommitAuthor = extractNameOnly(proposedDry.author || '-');
    const proposedDryCommitSubject = proposedDry.subject || '-';
    const proposedDryCommitBody = extractBodyPreTrailer(proposedDry.body || '-');
    const proposedDryCommitUrl = getCommitUrl(proposedDry.repoURL ?? '', proposedDry.sha ?? '');
    const proposedDryCommitDate = proposedDry.commitTime ? formatDate(proposedDry.commitTime) : '-';

    //Proposed Checks
    const proposedChecks = getProposedChecks(proposedCommitStatuses);
    const proposedChecksSummary = calculateHealthSummary(proposedChecks);

    // Code Commits from references[0].commit (PROPOSED)
    const proposedReferenceData = extractReferenceCommitData(proposedDry);
    const proposedReferenceSha = proposedReferenceData.sha;
    const proposedReferenceCommitAuthor = proposedReferenceData.author;
    const proposedReferenceCommitSubject = proposedReferenceData.subject;
    const proposedReferenceCommitBody = proposedReferenceData.body;
    const proposedReferenceCommitDate = proposedReferenceData.date;
    const proposedReferenceCommitUrl = proposedReferenceData.url;


    // PR number and url
    const prNumber = pullRequest?.id ? parseInt(pullRequest.id, 10) : null;
    const prUrl = pullRequest?.url || null;

    const envDetails = {
      // Environment info
      branch,
      phase,
      promotionStatus: 'unknown',
      

      drySha,
      dryCommitAuthor,
      dryCommitSubject,
      dryCommitMessage,
      dryCommitDate: dry.commitTime ? formatDate(dry.commitTime) : '-',
      dryCommitUrl,
      referenceSha,
      referenceCommitAuthor,
      referenceCommitSubject,
      referenceCommitDate,
      referenceCommitUrl,
      referenceCommitBody,
      
      proposedSha,
      proposedDryCommitAuthor,
      proposedDryCommitSubject,
      proposedDryCommitBody,
      proposedDryCommitDate,
      proposedDryCommitUrl,
      proposedReferenceSha,
      proposedReferenceCommitAuthor,
      proposedReferenceCommitSubject,
      proposedReferenceCommitDate,
      proposedReferenceCommitUrl,
      proposedReferenceCommitBody,
      
      proposedChecks,
      activeChecks,
      activeChecksSummary,
      proposedChecksSummary,
      prNumber,
      prUrl,
    };

    // Determine promotion status using the same logic as getPromotionStatus
    const promotionStatus = getEnvironmentStatus(environment);

    return {
      ...envDetails,
      promotionStatus,
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
    return { total: 0, promoted: 0, pending: 0, failed: 0, overallStatus: 'default', displayText: '' };
  }

  const envs = ps.status.environments;
  let promoted = 0;
  let pending = 0;
  let failed = 0;

  // Use getEnvironmentStatus for each environment
  for (const env of envs) {
    const status = getEnvironmentStatus(env);
    if (status === 'failure') failed++;
    else if (status === 'success') promoted++;
    else if (status === 'pending') pending++;
  }

  const total = envs.length;
  
  // Determine overall status
  const overallStatus = failed > 0 ? 'failure' : 
                       pending > 0 ? 'pending' : 
                       promoted === total ? 'success' : 'default';

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


