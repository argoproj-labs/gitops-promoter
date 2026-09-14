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

  it('does not negate when the sign follows other leading characters', () => {
    expect(parseGoDuration('x-5m')).toBe(300_000);
  });

  it.each([
    ['0', 0],
    ['0s', 0],
    ['0ms', 0],
    ['0h0m0s', 0],
  ])('parses the zero form %s as 0', (input, expected) => {
    expect(parseGoDuration(input)).toBe(expected);
  });

  it('returns 0 for empty and whitespace-only input', () => {
    expect(parseGoDuration('')).toBe(0);
    expect(parseGoDuration('   ')).toBe(0);
    expect(parseGoDuration('\n\t ')).toBe(0);
  });

  it('handles large values without losing whole-millisecond accuracy', () => {
    expect(parseGoDuration('9999h')).toBe(35_996_400_000);
    expect(parseGoDuration('9999h59m59s')).toBe(35_999_999_000);
    expect(parseGoDuration('100000h')).toBe(360_000_000_000);
  });

  it('returns 0 for input containing no recognizable unit', () => {
    expect(parseGoDuration('abc')).toBe(0);
    expect(parseGoDuration('xyz')).toBe(0);
    expect(parseGoDuration('100')).toBe(0);
  });

  it('ignores unsupported Go-adjacent units like days and weeks', () => {
    expect(parseGoDuration('5d')).toBe(0);
    expect(parseGoDuration('2w')).toBe(0);
    expect(parseGoDuration('3y')).toBe(0);
  });

  it('is case-sensitive and ignores uppercase unit suffixes', () => {
    expect(parseGoDuration('1H')).toBe(0);
    expect(parseGoDuration('1M')).toBe(0);
    expect(parseGoDuration('1S')).toBe(0);
  });

  it('requires the unit to immediately follow the number', () => {
    expect(parseGoDuration('1 h')).toBe(0);
  });

  // Pins current behavior: the global scan skips junk between matches instead of rejecting the
  // input, so malformed strings silently parse as if the junk were absent.
  it('skips embedded junk rather than rejecting the input', () => {
    expect(parseGoDuration('1h!!30m')).toBe(5_400_000);
    expect(parseGoDuration('abc1h')).toBe(3_600_000);
    expect(parseGoDuration('1h 30m')).toBe(5_400_000);
    expect(parseGoDuration('garbage 2s garbage')).toBe(2_000);
  });

  // Pins current behavior: only a leading '-' negates, so a mid-string '-' is dropped and
  // "1h-30m" adds the 30m instead of subtracting it. Go would reject this string outright.
  it('ignores a minus sign that appears mid-string', () => {
    expect(parseGoDuration('1h-30m')).toBe(5_400_000);
    expect(parseGoDuration('5m-')).toBe(300_000);
  });

  // Pins current behavior: repeated units are summed rather than rejected as invalid.
  it('sums repeated units instead of rejecting them', () => {
    expect(parseGoDuration('1h1h')).toBe(7_200_000);
    expect(parseGoDuration('30s30s')).toBe(60_000);
  });

  // Pins current behavior: a leading '+' is not part of the regex and is simply skipped, so a
  // positive-signed duration parses as positive by accident rather than by design.
  it('skips a leading plus sign', () => {
    expect(parseGoDuration('+5m')).toBe(300_000);
  });

  // Pins current behavior: exponent notation is not supported, so "1e3s" matches only the "3s"
  // tail and yields 3s instead of 1000s.
  it('misparses exponent notation', () => {
    expect(parseGoDuration('1e3s')).toBe(3_000);
  });

  // Pins current behavior: the regex requires a leading digit, so ".5s" matches only "5s" and
  // yields 5000ms instead of 500ms.
  it('misparses a value with no digit before the decimal point', () => {
    expect(parseGoDuration('.5s')).toBe(5_000);
  });

  // Pins current behavior: a lone '-' with no numeric match produces negative zero.
  it('returns negative zero for a sign with no value', () => {
    expect(parseGoDuration('-')).toBe(-0);
    expect(Object.is(parseGoDuration('-'), -0)).toBe(true);
    expect(Object.is(parseGoDuration('-0s'), -0)).toBe(true);
  });

  it('is stateless across calls despite the module-level style global regex', () => {
    expect(parseGoDuration('1h30m')).toBe(5_400_000);
    expect(parseGoDuration('1h30m')).toBe(5_400_000);
    expect(parseGoDuration('45s')).toBe(45_000);
    expect(parseGoDuration('45s')).toBe(45_000);
  });
});
