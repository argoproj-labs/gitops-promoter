import { describe, it, expect } from 'vitest';
import { parseGoDuration } from './util';

describe('parseGoDuration', () => {
  it.each([
    ['1h', 3_600_000],
    ['1m', 60_000],
    ['1s', 1_000],
    ['1ms', 1],
  ])('parses %s as %d ms', (input, expected) => {
    expect(parseGoDuration(input)).toBe(expected);
  });

  it.each([
    ['1us', 0.001],
    ['1µs', 0.001],
    ['500us', 0.5],
    ['500µs', 0.5],
    ['1ns', 0.000001],
    ['1000ns', 0.001],
  ])('parses the sub-millisecond unit %s as %d ms', (input, expected) => {
    expect(parseGoDuration(input)).toBeCloseTo(expected, 10);
  });

  it('distinguishes minutes from milliseconds', () => {
    expect(parseGoDuration('1m')).toBe(60_000);
    expect(parseGoDuration('1ms')).toBe(1);
  });

  it('prefers the longest unit match so "ms" never parses as "m" plus a stray "s"', () => {
    expect(parseGoDuration('30ms')).toBe(30);
    expect(parseGoDuration('30m')).toBe(1_800_000);
  });

  it.each([
    ['1h30m', 5_400_000],
    ['2h45m30s', 9_930_000],
    ['1m30.5s', 90_500],
    ['1h0m0s', 3_600_000],
    ['1h2m3s4ms', 3_723_004],
  ])('parses the compound form %s as %d ms', (input, expected) => {
    expect(parseGoDuration(input)).toBe(expected);
  });

  it('accumulates every unit in a fully mixed duration', () => {
    expect(parseGoDuration('1h2m3s4ms5us6ns')).toBeCloseTo(3_723_004.005006, 6);
  });

  it.each([
    ['0.5s', 500],
    ['1.5h', 5_400_000],
    ['2.25ms', 2.25],
    ['1.5m', 90_000],
    ['0.5ms', 0.5],
  ])('parses the fractional value %s as %d ms', (input, expected) => {
    expect(parseGoDuration(input)).toBeCloseTo(expected, 10);
  });

  it.each([
    ['-30s', -30_000],
    ['-1h30m', -5_400_000],
    ['-2h45m30s', -9_930_000],
    ['-1ms', -1],
  ])('parses the negative duration %s as %d ms', (input, expected) => {
    expect(parseGoDuration(input)).toBe(expected);
  });

  it('negates after skipping leading whitespace', () => {
    expect(parseGoDuration('  -5m')).toBe(-300_000);
    expect(parseGoDuration('\t-1h')).toBe(-3_600_000);
  });

  it.each([
    ['0', 0],
    ['0s', 0],
    ['0ms', 0],
    ['0h0m0s', 0],
  ])('parses the zero form %s as 0', (input, expected) => {
    expect(parseGoDuration(input)).toBe(expected);
  });

  it('accepts a bare "0" as the only unitless form', () => {
    expect(parseGoDuration('0')).toBe(0);
    expect(parseGoDuration('00')).toBe(null);
    expect(parseGoDuration('1')).toBe(null);
  });

  it('normalizes a negative zero result to positive zero', () => {
    expect(Object.is(parseGoDuration('-0s'), 0)).toBe(true);
    expect(Object.is(parseGoDuration('-0s'), -0)).toBe(false);
    expect(Object.is(parseGoDuration('-0h0m0s'), 0)).toBe(true);
  });

  it('handles large values without losing whole-millisecond accuracy', () => {
    expect(parseGoDuration('9999h')).toBe(35_996_400_000);
    expect(parseGoDuration('9999h59m59s')).toBe(35_999_999_000);
    expect(parseGoDuration('100000h')).toBe(360_000_000_000);
  });

  it.each(['', '   ', '\n\t '])(
    'returns null for the empty or whitespace-only input %j',
    (input) => {
      expect(parseGoDuration(input)).toBe(null);
    },
  );

  it.each(['abc', 'xyz', '100'])('returns null for %s, which contains no valid term', (input) => {
    expect(parseGoDuration(input)).toBe(null);
  });

  it.each(['5d', '2w', '3y'])(
    'returns null for the unsupported Go-adjacent unit in %s',
    (input) => {
      expect(parseGoDuration(input)).toBe(null);
    },
  );

  it.each(['1H', '1M', '1S'])('returns null for the uppercase unit in %s', (input) => {
    expect(parseGoDuration(input)).toBe(null);
  });

  it.each(['1 h', '1h 30m', '30 s'])(
    'returns null when whitespace separates a value from its unit in %j',
    (input) => {
      expect(parseGoDuration(input)).toBe(null);
    },
  );

  it.each(['1h!!30m', 'abc1h', 'garbage 2s garbage', 'x-5m', '1h30m!'])(
    'returns null for junk in %j',
    (input) => {
      expect(parseGoDuration(input)).toBe(null);
    },
  );

  it.each(['1h1h', '30s30s', '2h-30m+15m', '1ms1ms'])(
    'returns null for the repeated unit in %s',
    (input) => {
      expect(parseGoDuration(input)).toBe(null);
    },
  );

  it.each(['5m-', '5m+', '-', '+'])('returns null for the dangling sign in %j', (input) => {
    expect(parseGoDuration(input)).toBe(null);
  });

  it('treats "m" and "ms" as distinct units rather than a repeat', () => {
    expect(parseGoDuration('1m1ms')).toBe(60_001);
    expect(parseGoDuration('1s1ms')).toBe(1_001);
  });

  it('subtracts a term introduced by a mid-string minus sign', () => {
    expect(parseGoDuration('1h-30m')).toBe(1_800_000);
    expect(parseGoDuration('1m-30s')).toBe(30_000);
  });

  it('adds a term introduced by a mid-string plus sign', () => {
    expect(parseGoDuration('1h+30m')).toBe(5_400_000);
    expect(parseGoDuration('1m+30s')).toBe(90_000);
  });

  it('applies each mid-string sign to the term that follows it', () => {
    expect(parseGoDuration('1h-30m-15s')).toBe(1_785_000);
    expect(parseGoDuration('1h+30m-15s')).toBe(5_385_000);
    expect(parseGoDuration('2h-30m+15s')).toBe(5_415_000);
  });

  it('negates the whole signed expression when a leading minus precedes mid-string signs', () => {
    expect(parseGoDuration('-1h-30m')).toBe(-1_800_000);
    expect(parseGoDuration('-1h-30m-15s')).toBe(-1_785_000);
    expect(parseGoDuration('+1h-30m')).toBe(1_800_000);
  });

  it('leaves leading-minus compound forms unchanged', () => {
    expect(parseGoDuration('-1h30m')).toBe(-5_400_000);
    expect(parseGoDuration('-2h45m30s')).toBe(-9_930_000);
    expect(parseGoDuration('-1.5h')).toBe(-5_400_000);
    expect(parseGoDuration('  -5m')).toBe(-300_000);
  });

  it('parses a leading plus sign as positive', () => {
    expect(parseGoDuration('+5m')).toBe(300_000);
    expect(parseGoDuration('+1h30m')).toBe(5_400_000);
  });

  it('rejects exponent notation, which Go does not accept', () => {
    expect(parseGoDuration('1e3s')).toBe(null);
    expect(parseGoDuration('1E3s')).toBe(null);
  });

  it('parses a value with no digit before the decimal point', () => {
    expect(parseGoDuration('.5s')).toBe(500);
    expect(parseGoDuration('-.5s')).toBe(-500);
    expect(parseGoDuration('.5h')).toBe(1_800_000);
  });

  it('parses a value with a trailing decimal point', () => {
    expect(parseGoDuration('1.s')).toBe(1_000);
  });

  it('returns null for a lone decimal point with no digits', () => {
    expect(parseGoDuration('.s')).toBe(null);
    expect(parseGoDuration('.')).toBe(null);
  });

  it('is stateless across calls despite the module-level style global regex', () => {
    expect(parseGoDuration('1h30m')).toBe(5_400_000);
    expect(parseGoDuration('1h30m')).toBe(5_400_000);
    expect(parseGoDuration('45s')).toBe(45_000);
    expect(parseGoDuration('45s')).toBe(45_000);
  });
});
