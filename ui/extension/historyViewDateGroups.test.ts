import { describe, it, expect } from 'vitest';
import {
  groupRowsByDay,
  localDayKey,
  formatDayLabel,
  UNKNOWN_DAY_KEY,
  UNKNOWN_DAY_LABEL,
} from '@components-lib/components/HistoryView/groupRowsByDay';
import type { CommitRow } from '@components-lib/components/HistoryView/types';

const at = (y: number, m: number, d: number, h = 12, min = 0, s = 0, ms = 0): number =>
  new Date(y, m - 1, d, h, min, s, ms).getTime();

const row = (id: string, freshestAt: number): CommitRow => ({
  id,
  dryShaFull: `${id}0000000`,
  dryShaShort: id,
  subject: id,
  author: 'a',
  repoUrl: '',
  freshestAt,
  earliestAt: freshestAt,
  cells: {},
  hasLive: false,
  hasInFlight: false,
  hasFailed: false,
  hasNoop: false,
});

const shape = (groups: ReturnType<typeof groupRowsByDay>) =>
  groups.map((g) => [g.key, g.rows.map((r) => r.id)]);

describe('groupRowsByDay', () => {
  it('returns no groups for empty input', () => {
    expect(groupRowsByDay([])).toEqual([]);
  });

  it('groups newest-first rows into newest-first days', () => {
    const rows = [
      row('a', at(2026, 10, 3, 15)),
      row('b', at(2026, 10, 3, 9)),
      row('c', at(2026, 10, 1, 18)),
    ];
    expect(shape(groupRowsByDay(rows))).toEqual([
      ['2026-10-03', ['a', 'b']],
      ['2026-10-01', ['c']],
    ]);
  });

  it('groups oldest-first rows into oldest-first days', () => {
    const rows = [
      row('c', at(2026, 10, 1, 18)),
      row('b', at(2026, 10, 3, 9)),
      row('a', at(2026, 10, 3, 15)),
    ];
    expect(shape(groupRowsByDay(rows))).toEqual([
      ['2026-10-01', ['c']],
      ['2026-10-03', ['b', 'a']],
    ]);
  });

  it('keeps several rows on one day in a single group in input order', () => {
    const rows = [
      row('a', at(2026, 10, 2, 23)),
      row('b', at(2026, 10, 2, 12)),
      row('c', at(2026, 10, 2, 8)),
      row('d', at(2026, 10, 2, 0, 30)),
    ];
    const groups = groupRowsByDay(rows);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.rows.map((r) => r.id)).toEqual(['a', 'b', 'c', 'd']);
  });

  it('splits on local midnight', () => {
    const rows = [
      row('after', at(2026, 10, 2, 0, 0, 0, 0)),
      row('before', at(2026, 10, 1, 23, 59, 59, 999)),
    ];
    expect(shape(groupRowsByDay(rows))).toEqual([
      ['2026-10-02', ['after']],
      ['2026-10-01', ['before']],
    ]);
  });

  it('puts rows without a timestamp in a trailing unknown group', () => {
    const rows = [row('x', 0), row('a', at(2026, 10, 1)), row('y', 0)];
    const groups = groupRowsByDay(rows);
    expect(shape(groups)).toEqual([
      ['2026-10-01', ['a']],
      [UNKNOWN_DAY_KEY, ['x', 'y']],
    ]);
    expect(groups[1]!.label).toBe(UNKNOWN_DAY_LABEL);
  });

  it('returns only the unknown group when no row has a timestamp', () => {
    expect(shape(groupRowsByDay([row('x', 0)]))).toEqual([[UNKNOWN_DAY_KEY, ['x']]]);
  });

  it('labels a day group with its formatted local date', () => {
    const t = at(2026, 10, 1, 9);
    const [group] = groupRowsByDay([row('a', t)]);
    expect(group!.label).toBe(formatDayLabel(t));
    expect(group!.label).toContain('2026');
  });
});

describe('localDayKey', () => {
  it('uses local calendar components', () => {
    expect(localDayKey(at(2026, 1, 5, 0, 0))).toBe('2026-01-05');
    expect(localDayKey(at(2026, 12, 31, 23, 59, 59, 999))).toBe('2026-12-31');
  });
});
