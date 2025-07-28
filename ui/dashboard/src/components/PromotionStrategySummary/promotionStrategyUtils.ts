import type { PromotionStrategy } from '@shared/utils/PSData';


export function getLastCommitTime(ps: PromotionStrategy): Date | null {

    //Determine the last commit time between both active/proposed hydrated commit
  const commitTimes = [
    ...(ps.status?.environments?.map(env => env.active?.dry?.commitTime) || []),
    ...(ps.status?.environments?.map(env => env.active?.hydrated?.commitTime) || []),
    ...(ps.status?.environments?.map(env => env.proposed?.dry?.commitTime) || []),
    ...(ps.status?.environments?.map(env => env.proposed?.hydrated?.commitTime) || [])
  ].filter(Boolean);

  if (commitTimes.length) {
    return new Date(Math.max(...commitTimes.map(t => new Date(t as string).getTime())));
  }

  if (ps.metadata?.creationTimestamp) {
    return new Date(ps.metadata.creationTimestamp);
  }

  return null
}


// Get the status for all environments and return an overall phase
export function getPromotionPhase(ps: PromotionStrategy): 'success' | 'failure' | 'pending' | 'default' {

  const envPhases = ps.status?.environments?.map(env => env.active?.commitStatuses?.[0]?.phase) || [];
  if (envPhases.length > 0) {
    if (envPhases.every(phase => phase === 'success')) return 'success';
    if (envPhases.some(phase => phase === 'failure')) return 'failure';
    if (envPhases.some(phase => phase === 'pending')) return 'pending';
    return 'default';
  }
  return 'default';
} 