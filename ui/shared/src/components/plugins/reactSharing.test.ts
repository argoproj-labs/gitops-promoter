import { describe, it, expect, beforeEach } from 'vitest';
import * as ActualReact from 'react';
import { assertSharedReactInstance } from './reactSharing';

const hostReact = ActualReact;

describe('assertSharedReactInstance', () => {
  beforeEach(() => {
    window.React = undefined as unknown as typeof ActualReact;
  });

  it('passes when window.React is the same reference as the host React', () => {
    window.React = hostReact;

    expect(() => assertSharedReactInstance(hostReact)).not.toThrow();
  });

  it('throws a clear error when window.React is missing', () => {
    expect(() => assertSharedReactInstance(hostReact)).toThrow(/window\.React is missing/);
  });

  it('throws a clear error when window.React is a different object than the host React', () => {
    window.React = { ...ActualReact };

    expect(() => assertSharedReactInstance(hostReact)).toThrow(/brought its own copy of React/);
  });
});
