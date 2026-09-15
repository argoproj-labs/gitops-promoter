import { describe, expect, it } from 'vitest';
import { resolveNamespace } from '../src/pages/resolveNamespace';

describe('resolveNamespace', () => {
  it('prefers the URL namespace over the persisted one', () => {
    expect(resolveNamespace('from-url', 'persisted', ['from-url', 'persisted'])).toBe('from-url');
  });

  it('falls back to the persisted namespace when the URL has none', () => {
    expect(resolveNamespace(null, 'persisted', ['persisted'])).toBe('persisted');
  });

  it('falls back to the persisted namespace when the URL one is unknown', () => {
    expect(resolveNamespace('gone', 'persisted', ['persisted'])).toBe('persisted');
  });

  it('accepts the URL namespace before the namespace list has loaded', () => {
    expect(resolveNamespace('from-url', 'persisted', [])).toBe('from-url');
  });

  it('returns empty when neither source has a namespace', () => {
    expect(resolveNamespace(null, '', [])).toBe('');
  });
});
