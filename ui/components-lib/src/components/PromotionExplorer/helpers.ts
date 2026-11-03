import type { Commit, CommitStatus } from '@shared/types/promotion';
import type { CellKind, HealthKey } from './types';

// Cell kinds with no commit activity for the env: non-selectable, excluded from env-scoped rows.
export function isEmptyCellKind(kind: CellKind | undefined): boolean {
  return kind === 'no-changes' || kind === 'unknown-history';
}

export function healthFromStatuses(statuses: CommitStatus[] | undefined): HealthKey {
  if (!statuses || statuses.length === 0) return 'unknown';
  if (statuses.some((s) => s.phase === 'failure')) return 'failure';
  if (statuses.some((s) => s.phase === 'pending')) return 'pending';
  if (statuses.every((s) => s.phase === 'success')) return 'success';
  return 'unknown';
}

export function safeHttpUrl(url?: string | null): string | undefined {
  if (!url) return undefined;
  try {
    const parsed = new URL(url.trim());
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

export function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : '';
}

export function commitKey(c: Commit | undefined): string | null {
  const s = c?.sha;
  if (s && s.length >= 7) return s.slice(0, 7);
  if (c?.subject || c?.author) return `nokey:${c.subject ?? ''}|${c.author ?? ''}`;
  return null;
}

export function parseEnvSelection(value: string | null | undefined): string[] {
  if (!value) return [];
  return [...new Set(value.split(',').filter(Boolean))];
}

export function formatEnvSelection(branches: readonly string[]): string | null {
  return branches.length > 0 ? branches.join(',') : null;
}

export interface DayGroup<T> {
  key: string;
  at: number;
  rows: T[];
}

function localDayKey(at: number): string {
  if (at <= 0) return 'unknown';
  const d = new Date(at);
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

export function groupRowsByDay<T extends { freshestAt: number }>(
  rows: readonly T[],
): DayGroup<T>[] {
  const groups: DayGroup<T>[] = [];
  for (const row of rows) {
    const key = localDayKey(row.freshestAt);
    const last = groups.at(-1);
    if (last?.key === key) last.rows.push(row);
    else groups.push({ key, at: row.freshestAt, rows: [row] });
  }
  return groups;
}

export function formatDayHeading(at: number): string {
  return at > 0
    ? new Date(at).toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      })
    : 'an unknown date';
}
