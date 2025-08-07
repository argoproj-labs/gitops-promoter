//Get duration ago (E.g: 1 day ago)
export const timeAgo = (dateString: string): string => {
    const now = new Date();
    const date = new Date(dateString);
  
  
    const diffMs = now.getTime() - date.getTime();
    const diffSeconds = Math.floor(diffMs / 1000);
    const diffMinutes = Math.floor(diffSeconds / 60);
    const diffHours = Math.floor(diffMinutes / 60);
    const diffDays = Math.floor(diffHours / 24);
  
    if (diffSeconds < 60) {
      return `${diffSeconds <= 1 ? 1 : diffSeconds} second${diffSeconds === 1 ? '' : 's'} ago`;
    } else if (diffMinutes < 60) {
      return `${diffMinutes} minute${diffMinutes === 1 ? '' : 's'} ago`;
    } else if (diffHours < 24) {
      return `${diffHours} hour${diffHours === 1 ? '' : 's'} ago`;
    } else {
      return `${diffDays} day${diffDays === 1 ? '' : 's'} ago`;
    }
  }

// Get the commit url from the repo url and sha
export function getCommitUrl(repoUrl: string, sha: string): string {
  if (!repoUrl || !sha) return '';
  const cleanRepoUrl = repoUrl.replace(/\/$/, '');
  return `${cleanRepoUrl}/commit/${sha}`;
}
  
//Extract name from 'Name <email>'
export function extractNameOnly(author: string): string {
  const match = author.match(/^([^<]+)</);
  if (match) return match[1].trim();
  return author;
}

//Extracts the body before trailers
export function extractBodyPreTrailer(body: string): string {
  if (!body) return '';
  const lines = body.split(/\r?\n/);
  const trailerStart = lines.findIndex(line =>
    /^([A-Za-z0-9-]+:|Signed-off-by:)/.test(line.trim())
  );
  if (trailerStart === -1) return body.trim();
  return lines.slice(0, trailerStart).join('\n').trim();
}
  
// date formatting (e.g: Jul 5 2025, 12:15pm EDT)
export function formatDate(date?: string): string {
  if (!date) return '-';
  const d = new Date(date);
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
    timeZoneName: 'short'
  }).replace(',', '').replace(/:00 /, ' '); // Remove seconds if present
}

// Get the last commit time from a PromotionStrategy
export function getLastCommitTime(ps: any): Date | null {
  //Determine the last commit time between both active/proposed hydrated commit
  const commitTimes = [
    ...(ps.status?.environments?.map((env: any) => env.active?.dry?.commitTime) || []),
    ...(ps.status?.environments?.map((env: any) => env.active?.hydrated?.commitTime) || []),
    ...(ps.status?.environments?.map((env: any) => env.proposed?.dry?.commitTime) || []),
    ...(ps.status?.environments?.map((env: any) => env.proposed?.hydrated?.commitTime) || [])
  ].filter(Boolean);

  if (commitTimes.length) {
    return new Date(Math.max(...commitTimes.map(t => new Date(t as string).getTime())));
  }

  if (ps.metadata?.creationTimestamp) {
    return new Date(ps.metadata.creationTimestamp);
  }

  return null;
}
  
// Get the overall promotion status from individual environment statuses
export function getOverallPromotionStatus(environmentStatuses: string[]): 'promoted' | 'failure' | 'pending' | 'unknown' {
  if (environmentStatuses.length === 0) return 'unknown';
  
  if (environmentStatuses.some(status => status === 'failure')) {
    return 'failure';
  }
  if (environmentStatuses.some(status => status === 'pending')) {
    return 'pending';
  }
  
  if (environmentStatuses.every(status => status === 'promoted')) {
    return 'promoted';
  }
  
  return 'unknown';
}
  