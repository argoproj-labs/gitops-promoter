import { describe, it, expect } from 'vitest';
import { commitStatusPlugins } from './index';

describe('commitStatusPlugins', () => {
  it('has a plugin registered for TimedCommitStatus', () => {
    expect(commitStatusPlugins['TimedCommitStatus']).toBeDefined();
    expect(typeof commitStatusPlugins['TimedCommitStatus']).toBe('function');
  });

  it('has no plugin registered for ArgoCDCommitStatus', () => {
    expect(commitStatusPlugins['ArgoCDCommitStatus']).toBeUndefined();
  });
});
