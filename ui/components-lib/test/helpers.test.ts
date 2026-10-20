import { describe, expect, it } from 'vitest';
import { formatEnvSelection, parseEnvSelection } from '@lib/components/PromotionExplorer/helpers';

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
