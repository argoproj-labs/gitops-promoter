import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, formatDate } from './util';

interface CommitStatus {
  key: string;
  phase: string;
  url?: string;
  details?: string;
}

interface Commit {
  sha?: string;
  author?: string;
  subject?: string;
  body?: string;
  commitTime?: string | null;
  repoURL?: string;
  references?: Array<{
    commit: {
      author?: string;
      sha?: string;
      subject?: string;
      date?: string;
      body?: string;
      repoURL?: string;
    };
  }>;
}

interface PullRequest {
  id: string;
  url?: string;
}

interface Environment {
  branch: string;
  active: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  proposed: {
    dry?: Commit;
    hydrated?: Commit;
    commitStatuses?: CommitStatus[];
  };
  pullRequest?: PullRequest;
}

export interface PromotionStrategy {
  kind: string;
  apiVersion: string;
  metadata: {
    name: string;
    namespace: string;
    uid: string;
    resourceVersion: string;
    generation: number;
    creationTimestamp: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
  spec: {
    gitRepositoryRef: {
      name: string;
      namespace?: string;
    };
    activeCommitStatuses?: { key: string }[] | null;
    proposedCommitStatuses?: { key: string }[] | null;
    environments: {
      branch: string;
      autoMerge?: boolean;
      activeCommitStatuses?: { key: string }[] | null;
      proposedCommitStatuses?: { key: string }[] | null;
    }[];
  };
  status?: { environments?: Environment[] };
}

interface Check {
  name: string;
  status: string;
  details?: string;
  url?: string;
}

interface EnrichedEnvDetails {
  // Environment info
  branch: string;
  phase: string;
  promotionStatus: string;
  
  // Active commits
  drySha: string;
  dryCommitAuthor: string;
  dryCommitSubject: string;
  dryCommitMessage: string;
  dryCommitDate: string;
  dryCommitUrl: string;
  activeChecks: Check[];
  activeChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };
  
  referenceSha: string;
  referenceCommitAuthor: string;
  referenceCommitSubject: string;
  referenceCommitDate: string;
  referenceCommitUrl: string;
  referenceCommitBody: string;
  
  // Proposed commits
  proposedSha: string;
  prNumber: number | null;
  prUrl: string | null;
  proposedDryCommitAuthor: string;
  proposedDryCommitSubject: string;
  proposedDryCommitBody: string;
  proposedDryCommitDate: string;
  proposedDryCommitUrl: string;
  proposedChecks: Check[];
  proposedChecksSummary: { successCount: number; totalCount: number; shouldDisplay: boolean };

  
  proposedReferenceSha: string;
  proposedReferenceCommitAuthor: string;
  proposedReferenceCommitSubject: string;
  proposedReferenceCommitDate: string;
  proposedReferenceCommitUrl: string;
  proposedReferenceCommitBody: string;
  
}


//TODO: HOW SHOULD WE HANDLE PROPOSED CARDS DISAPPEARING?
function getPromotionStatus(env: {
  proposedSha: string;
  drySha: string;
  checks: Check[];
  totalProposedChecks: number;
  activeChecks: Check[];
}): 'pending' | 'promoted' | 'success' | 'failure' | 'unknown' {
  const { proposedSha, drySha: sha, checks = [], activeChecks = [] } = env;
  let promotionStatus: 'pending' | 'promoted' | 'success' | 'failure' | 'unknown' = 'unknown';

  // Check for failures in proposed checks first
  if (checks.some((c: Check) => c.status === 'failure')) {
    promotionStatus = 'failure';
  }
  // Pending (PR OPEN)
  else if (proposedSha !== sha) {
    promotionStatus = 'pending';
  }

  // Promoted (PR MERGED && ACTIVE CHECKS IN PROGRESS)
  else if (proposedSha === sha) {
    if (activeChecks && activeChecks.length > 0 && !activeChecks.every((c: Check) => c.status === 'success')) {
      promotionStatus = 'promoted';
    }
    // Success (PR MERGED && ACTIVE CHECKS PASSED)
    else if (activeChecks && activeChecks.length > 0 && activeChecks.every((c: Check) => c.status === 'success')) {
      promotionStatus = 'success';
    }
  }

  return promotionStatus;
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
function extractReferenceCommitData(dryCommit: any): {
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
  const subject = referenceCommit.subject || referenceCommit.message || '-';
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

    const promotionStatus = getPromotionStatus({
      ...envDetails,
      checks: envDetails.proposedChecks,
      totalProposedChecks: envDetails.proposedChecks.length,
      activeChecks: envDetails.activeChecks,
      proposedSha: envDetails.proposedSha,
      drySha: envDetails.drySha,
    });

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


