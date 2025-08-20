import type { Environment, PromotionPhase, PromotionStrategy, Check } from '../types/promotion';


export function getHealthStatus(checks: Check[]): 'success' | 'failure' | 'pending' | 'unknown' {
  if (!Array.isArray(checks) || checks.length === 0) {
    return 'unknown';
  }
  if (checks.some(c => c.status === 'failure')) {
    return 'failure';
  }
  if (checks.some(c => c.status === 'pending')) {
    return 'pending';
  }
  if (checks.every(c => c.status === 'success')) {
    return 'success';
  }
  return 'unknown';
}

// Determine status of a single environment (promoted/pending/failure/unknown)
export function getEnvironmentStatus(env: Environment): 'pending' | 'promoted' | 'failure' | 'unknown' {
  const { active = {}, proposed = {} } = env;

  const proposedSha = proposed.dry?.sha;
  const activeSha = active.dry?.sha;

  const proposedChecks = proposed.commitStatuses || [];
  const activeChecks = active.commitStatuses || [];


  //FAILURE -> any check is failure
  if (proposedChecks.some(cs => cs.phase === 'failure') || activeChecks.some(cs => cs.phase === 'failure')) {
    return 'failure';
  }


  //PENDING -> proposed sha is different from active sha
  if (proposedSha && proposedSha !== activeSha) {
    return 'pending';
  }


  //PROMOTED -> proposed sha is the same as active sha
  if (proposedSha === activeSha) {
    return 'promoted';
  }

  return 'unknown';
}

// Overall promotion status across all environments
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

  for (const env of envs) {
    const status = getEnvironmentStatus(env);
    if (status === 'failure') failed++;
    else if (status === 'promoted') promoted++;
    else if (status === 'pending') pending++;
  }

  const total = envs.length;
  const overallStatus: PromotionPhase = failed > 0 ? 'failure' :
                       pending > 0 ? 'pending' :
                       promoted === total ? 'promoted' : 'unknown';

  const displayText = failed > 0 ? `${failed}/${total} environments failed` :
                     pending > 0 ? `${pending}/${total} environments pending` :
                     promoted > 0 ? `${promoted}/${total} environments promoted` :
                     `${total}/${total} environments`;

  return { total, promoted, pending, failed, overallStatus, displayText };
}


