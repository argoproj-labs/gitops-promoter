import type { CommitRow } from './types';

export interface RowDayGroup {
  key: string;
  label: string;
  rows: CommitRow[];
}

export const UNKNOWN_DAY_KEY = 'unknown';
export const UNKNOWN_DAY_LABEL = 'Unknown date';

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

export function localDayKey(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

export function formatDayLabel(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

export function groupRowsByDay(rows: CommitRow[]): RowDayGroup[] {
  const groups = new Map<string, RowDayGroup>();
  const unknown: RowDayGroup = { key: UNKNOWN_DAY_KEY, label: UNKNOWN_DAY_LABEL, rows: [] };

  for (const row of rows) {
    if (!(row.freshestAt > 0)) {
      unknown.rows.push(row);
      continue;
    }
    const key = localDayKey(row.freshestAt);
    let group = groups.get(key);
    if (!group) {
      group = { key, label: formatDayLabel(row.freshestAt), rows: [] };
      groups.set(key, group);
    }
    group.rows.push(row);
  }

  const result = Array.from(groups.values());
  if (unknown.rows.length > 0) result.push(unknown);
  return result;
}
