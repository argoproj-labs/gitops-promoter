import type { Commit, CommitStatus } from '@shared/types/promotion';
import type { HealthKey } from './types';

/* ─── small helpers ───────────────────────────────────────────────── */

export function healthFromStatuses(statuses: CommitStatus[] | undefined): HealthKey {
  if (!statuses || statuses.length === 0) return 'unknown';
  if (statuses.some((s) => s.phase === 'failure')) return 'failure';
  if (statuses.some((s) => s.phase === 'pending')) return 'pending';
  if (statuses.every((s) => s.phase === 'success')) return 'success';
  return 'unknown';
}

export function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : '';
}

export function commitKey(c: Commit | undefined): string | null {
  const s = c?.sha;
  if (s && s.length >= 7) return s.slice(0, 7);
  // Fallback when sha is missing: subject+author so we still group sensibly.
  if (c?.subject || c?.author) return `nokey:${c?.subject ?? ''}|${c?.author ?? ''}`;
  return null;
}
