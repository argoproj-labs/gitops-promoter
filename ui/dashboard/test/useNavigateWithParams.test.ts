import { describe, it, expect } from 'vitest';
import { mergeAllowedParams } from '../src/hooks/useNavigateWithParams';

const to = '/promotion-strategies/default/my-strategy';

describe('mergeAllowedParams', () => {
  it('preserves an allowlisted param', () => {
    expect(mergeAllowedParams(to, '?mock=true')).toBe(`${to}?mock=true`);
  });

  it('drops per-page history params', () => {
    expect(mergeAllowedParams(to, '?filter=failed&sort=oldest&envs=staging,prod')).toBe(to);
  });

  it('drops selection params', () => {
    expect(mergeAllowedParams(to, '?commit=abc123&env=environments/prd')).toBe(to);
  });

  it('keeps an allowlisted param alongside dropped ones', () => {
    expect(mergeAllowedParams(to, '?filter=failed&mock=true&sort=oldest')).toBe(`${to}?mock=true`);
  });

  it('preserves repeated allowlisted values', () => {
    expect(mergeAllowedParams(to, '?mock=true&mock=false')).toBe(`${to}?mock=true&mock=false`);
  });

  it('does not emit a bare trailing ? when nothing survives', () => {
    const next = mergeAllowedParams(to, '?filter=failed');
    expect(next).toBe(to);
    expect(next).not.toContain('?');
  });

  it('returns the destination unchanged for an empty search', () => {
    expect(mergeAllowedParams(to, '')).toBe(to);
  });

  it('leaves a destination that carries its own query string untouched', () => {
    expect(mergeAllowedParams(`${to}/history?commit=abc123`, '?mock=true')).toBe(
      `${to}/history?commit=abc123`,
    );
  });

  it('leaves a destination with its own empty query string untouched', () => {
    expect(mergeAllowedParams(`${to}?`, '?mock=true')).toBe(`${to}?`);
  });

  it('accepts a search string without a leading ?', () => {
    expect(mergeAllowedParams(to, 'mock=true&filter=failed')).toBe(`${to}?mock=true`);
  });
});
