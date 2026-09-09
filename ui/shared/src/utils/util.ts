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
 * Compact duration from a millisecond span (e.g. `"3d 4h"`, `"2h 15m"`, `"8m"`,
 * `"45s"`). Shows at most the two largest non-zero units. Sub-minute spans
 * round to seconds; a non-positive span returns `"<1m"`.
 */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '<1m';
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return hours % 24 ? `${days}d ${hours % 24}h` : `${days}d`;
  if (hours > 0) return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
  if (minutes > 0) return `${minutes}m`;
  return `${seconds}s`;
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
    ps.status?.environments?.flatMap((env) => [
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

/**
 * Sort every `commitStatuses` list on a parsed PromotionStrategy by key, in
 * place. The CRD emits these in a non-deterministic order (aggregated from
 * multiple CommitStatus resources), so normalizing once at ingestion gives every
 * consumer (overview, history matrix, drawer) the same stable ordering. Mutates
 * and returns the same object for call-site convenience.
 */
export function sortStrategyCommitStatuses(ps: PromotionStrategy): PromotionStrategy {
  const byKey = (a: { key: string }, b: { key: string }) => a.key.localeCompare(b.key);
  for (const env of ps.status?.environments ?? []) {
    env.active.commitStatuses?.sort(byKey);
    env.proposed.commitStatuses?.sort(byKey);
    for (const entry of env.history ?? []) {
      entry.active?.commitStatuses?.sort(byKey);
      entry.proposed?.commitStatuses?.sort(byKey);
    }
  }
  return ps;
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
