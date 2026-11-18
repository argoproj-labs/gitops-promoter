import { describe, expect, it } from 'vitest';
import {
  formatEnvSelection,
  groupRowsByDay,
  parseEnvSelection,
} from '@lib/components/PromotionExplorer/helpers';

describe('env selection param', () => {
  it('parses a single legacy value', () => {
    expect(parseEnvSelection('environments/prod')).toEqual(['environments/prod']);
  });

  it('parses a comma separated list, dropping empties and duplicates', () => {
    expect(parseEnvSelection('a,b,,a,c')).toEqual(['a', 'b', 'c']);
  });

  it('parses a missing value as an empty selection', () => {
    expect(parseEnvSelection(null)).toEqual([]);
    expect(parseEnvSelection(undefined)).toEqual([]);
    expect(parseEnvSelection('')).toEqual([]);
  });

  it('formats a selection and round-trips it', () => {
    const selection = ['environments/dev', 'environments/prod-us'];
    expect(formatEnvSelection(selection)).toBe('environments/dev,environments/prod-us');
    expect(parseEnvSelection(formatEnvSelection(selection))).toEqual(selection);
  });

  it('formats an empty selection as null', () => {
    expect(formatEnvSelection([])).toBeNull();
  });
});

describe('groupRowsByDay', () => {
  const row = (id: string, at: number) => ({ id, freshestAt: at });
  const local = (day: number, hour: number) => new Date(2026, 10, day, hour).getTime();

  it('groups consecutive rows by local calendar day, keeping order', () => {
    const rows = [
      row('a', local(2, 23)),
      row('b', local(2, 0)),
      row('c', local(1, 12)),
      row('d', 0),
    ];
    const groups = groupRowsByDay(rows);
    expect(groups.map((g) => g.rows.map((r) => r.id))).toEqual([['a', 'b'], ['c'], ['d']]);
    expect(groups.map((g) => g.key)).toEqual(['2026-11-2', '2026-11-1', 'unknown']);
    expect(groups[0].at).toBe(local(2, 23));
  });

  it('returns no groups for no rows', () => {
    expect(groupRowsByDay([])).toEqual([]);
  });
});
