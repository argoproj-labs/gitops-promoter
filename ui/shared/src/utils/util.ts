import type { PromotionStrategy, Rfc3339DateTime } from '../types/promotion';

/** Relative time from an {@link Rfc3339DateTime} (e.g. `"3 hours ago"`). */
export const timeAgo = (dateString: Rfc3339DateTime): string => {
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
};

/**
 * Anchor target/rel for a link. Same-origin links open in the current window
 * (no new tab); cross-origin links open in a new tab with a safe rel. If the URL
 * can't be parsed, treat it as external.
 */
export function linkTargetProps(url?: string): { target?: string; rel?: string } {
  if (url) {
    try {
      if (new URL(url, window.location.href).origin === window.location.origin) {
        return {};
      }
    } catch {
      // fall through to external
    }
  }
  return { target: '_blank', rel: 'noopener noreferrer' };
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
  const trailerStart = lines.findIndex((line) =>
    /^([A-Za-z0-9-]+:|Signed-off-by:)/.test(line.trim()),
  );
  if (trailerStart === -1) return body.trim();
  return lines.slice(0, trailerStart).join('\n').trim();
}

/**
 * Locale display string from an {@link Rfc3339DateTime} such as `2026-05-22T15:00:36Z`.
 * Shape depends on the browser locale and timezone (e.g. `May 22, 2026, 11:00 AM EDT`).
 */
export function formatDate(date?: Rfc3339DateTime): string {
  if (!date) return '-';
  const d = new Date(date);
  return d
    .toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      hour12: true,
      timeZoneName: 'short',
    })
    .replace(',', '')
    .replace(/:00 /, ' '); // Remove seconds if present
}

// Get the last commit time from a PromotionStrategy
export function getLastCommitTime(ps: PromotionStrategy): Date | null {
  const commitTimes = (
    ps.status?.environments.flatMap((env) => [
      env.active.dry?.commitTime,
      env.active.hydrated?.commitTime,
      env.proposed.dry?.commitTime,
      env.proposed.hydrated?.commitTime,
    ]) ?? []
  ).filter(Boolean) as Rfc3339DateTime[];

  if (commitTimes.length) {
    return new Date(Math.max(...commitTimes.map((t) => new Date(t).getTime())));
  }

  if (ps.metadata.creationTimestamp) {
    return new Date(ps.metadata.creationTimestamp);
  }

  return null;
}

// Get the overall promotion status from individual environment statuses
export function getOverallPromotionStatus(
  environmentStatuses: string[],
): 'promoted' | 'failure' | 'pending' | 'unknown' {
  if (environmentStatuses.length === 0) return 'unknown';

  if (environmentStatuses.some((status) => status === 'failure')) {
    return 'failure';
  }
  if (environmentStatuses.some((status) => status === 'pending')) {
    return 'pending';
  }

  if (environmentStatuses.every((status) => status === 'promoted')) {
    return 'promoted';
  }

  return 'unknown';
}
