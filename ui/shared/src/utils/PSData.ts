import { getCommitUrl, extractNameOnly, extractBodyPreTrailer, parseTrailers, formatDate, extractEnvNameFromBranch } from './util';

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
  commitTime?: string;
  repoURL?: string;
}

interface PullRequest {
  id: string;
  prCreationTime: string;
  state: string;
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

interface PromotionStrategy {
  metadata?: { namespace?: string };
  spec?: { environments?: { branch: string; autoMerge?: boolean }[] };
  status?: { environments?: Environment[] };
}

interface Check {
  name: string;
  status: string;
  details?: string;
  detailLinks?: string;
}

interface EnrichedEnvDetails {
  branch: string;
  phase: string;
  lastSync: string;
  drySha: string;
  dryCommitAuthor: string;
  dryCommitMessage: string;
  dryCommitSubject: string;
  dryCommitUrl: string;
  dryCommitDate: string;
  dryCommitTrailers: any;
  dryCommitTrailerBody: string | undefined;
  proposedSha: string;
  hydratedSha: string;
  hydratedCommitAuthor: string;
  hydratedCommitSubject: string;
  hydratedCommitBody: string;
  hydratedCommitUrl: string;
  hydratedCommitDate: string;
  proposedChecks: Check[];
  activeChecks: Check[];
  prNumber: number | null;
  prUrl: string | null;
  prCreatedAt: string | null;
  mergeDate: string;
  promotionStatus: string;
  percent: number;
  autoMerge: boolean;
}


function getPromotionStatus(env: {
  proposedSha: string;
  drySha: string;
  checks: Check[];
  totalProposedChecks: number;
  activeChecks: Check[];
}): 'pending' | 'promoted' | 'success' | 'failure' | 'unknown' {
  const { proposedSha, drySha: sha, checks = [], totalProposedChecks = 0, activeChecks = [] } = env;
  let promotionStatus: 'pending' | 'promoted' | 'success' | 'failure' | 'unknown' = 'unknown';


  //STATE 1: Pending (PR OPEN)
  if (proposedSha !== sha) {
    promotionStatus = 'pending';


  //STATE 1B: Pending (PR OPEN && PROPOSED CHECKS IN PROGRESS  - applies to second environment)
  } else if (proposedSha !== sha && totalProposedChecks > 0) {
    promotionStatus = 'pending';


  //STATE 2: Promoted (PR MERGED && ACTIVE CHECKS IN PROGRESS)
  } else if (proposedSha === sha) {
    if (activeChecks && activeChecks.length > 0 && !activeChecks.every((c: Check) => c.status === 'success')) {
      promotionStatus = 'promoted';


      //STATE 3: Success (PR MERGED && ACTIVE CHECKS PASSED)
    } else if (activeChecks && activeChecks.length > 0 && activeChecks.every((c: Check) => c.status === 'success')) {
      promotionStatus = 'success';
    }

    
    //STATUS 5: Failure (Failure in Proposed Checks)
  } else if (checks.some((c: Check) => c.status === 'failure')) {
    promotionStatus = 'failure';
  } else {
    promotionStatus = 'unknown';
  }

  return promotionStatus;
}

function calculatePercent(passed: number, total: number, promotionStatus: string): number {
  if (total > 0) {
    return (passed / total) * 100;
  } else if (promotionStatus === 'promoted') {
    return 100;
  }
  return 0;
}



function getProposedChecks(commitStatuses: CommitStatus[]): Check[] {
  return commitStatuses.map((cs: CommitStatus) => ({
    name: cs.key,
    status: cs.phase || 'unknown',
    details: cs.details,
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

function getEnvDetails(environment: Environment, specEnvs: { branch: string; autoMerge?: boolean }[]): EnrichedEnvDetails {
  const { active = {}, proposed = {}, pullRequest } = environment;


    //TODO: HOW DO WE EXTRACT JUST THE BRANCH NAME? 
    const branch = extractEnvNameFromBranch(environment.branch || '');

    // Phase
    const commitStatuses = active.commitStatuses || [];
    const phase = commitStatuses[0]?.phase || 'unknown';


    // Dry commit
    const dry = active.dry || {};
    const drySha = dry.sha ? dry.sha.slice(0, 7) : '-';
    const dryCommitAuthor = extractNameOnly(proposed.dry?.author || '-');
    const dryCommitSubject = proposed.dry?.subject || '-';
    const dryCommitMessage = extractBodyPreTrailer(proposed.dry?.body || '-');
    const dryCommitUrl = getCommitUrl(dry.repoURL ?? '', dry.sha ?? '');
    const { trailers: dryCommitTrailers, trailerBody: dryCommitTrailerBody } = parseTrailers(proposed.dry?.body || '');



    // Hydrated Sha
    const hydrated = active.hydrated || {};
    const hydratedSha = hydrated.sha ? hydrated.sha.slice(0, 7) : '-';
    const hydratedCommitAuthor = hydrated.author || '-';
    const hydratedCommitSubject = hydrated.subject || '-';
    const hydratedCommitBody = hydrated.body || '-';
    const lastSync = hydrated.commitTime ? formatDate(hydrated.commitTime) : '-';


    // Hydrated commit
    const hydratedCommitUrl = getCommitUrl(dry.repoURL ?? '', hydrated.sha ?? '');
    const hydratedCommitDate = hydrated.commitTime ? formatDate(hydrated.commitTime) : '-';


    // Proposed
    const proposedDry = proposed.dry || {};
    const proposedSha = proposedDry.sha ? proposedDry.sha.slice(0, 7) : '-';
    const proposedCommitStatuses = proposed.commitStatuses || [];
    const proposedChecks = getProposedChecks(proposedCommitStatuses);
    const totalProposedChecks = proposedChecks.length;
    const passed = proposedChecks.filter((check: Check) => check.status === 'success').length;


    // Active checks
    const activeChecks = getActiveChecks(commitStatuses);



    // PR number and url
    const prNumber = pullRequest?.id ? parseInt(pullRequest.id, 10) : null;
    const repoURL = proposed.dry?.repoURL || proposed.hydrated?.repoURL || '';
    const prUrl = prNumber && repoURL ? `${repoURL}/pull/${prNumber}` : null;



    const prCreatedAt = pullRequest?.prCreationTime || '-';
    const mergeDate = hydrated.commitTime ? formatDate(hydrated.commitTime) : '';
    
    // Find the matching spec environment for autoMerge
    const specEnv = specEnvs.find(e => e.branch === environment.branch);
    const autoMerge = specEnv?.autoMerge ?? false;

    const envDetails = {
      branch,
      phase,
      lastSync,
      drySha,
      dryCommitAuthor,
      dryCommitMessage,
      dryCommitSubject,
      dryCommitUrl,
      dryCommitDate: proposed.dry?.commitTime ? formatDate(proposed.dry.commitTime) : '-',
      dryCommitTrailers,
      dryCommitTrailerBody,
      proposedSha,
      hydratedSha,
      hydratedCommitAuthor,
      hydratedCommitSubject,
      hydratedCommitBody,
      hydratedCommitUrl,
      hydratedCommitDate,
      proposedChecks,
      activeChecks,
      prNumber,
      prUrl,
      prCreatedAt,
      mergeDate,
      autoMerge,
    };


    const promotionStatus = getPromotionStatus({
      ...envDetails,
      checks: envDetails.proposedChecks,
      totalProposedChecks: envDetails.proposedChecks.length,
      activeChecks: envDetails.activeChecks,
      proposedSha: envDetails.proposedSha,
      drySha: envDetails.drySha,
    });

    
    const percent = calculatePercent(passed, totalProposedChecks, promotionStatus);
    return {
      ...envDetails,
      promotionStatus,
      percent,
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


