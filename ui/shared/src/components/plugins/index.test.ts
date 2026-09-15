import { describe, it, expect } from 'vitest';
import { commitStatusPlugins, getCommitStatusRowPlugin, PROMOTER_GROUP } from './index';

describe('commitStatusPlugins', () => {
  it('has a plugin registered for TimedCommitStatus', () => {
    expect(commitStatusPlugins['TimedCommitStatus']).toBeDefined();
    expect(typeof commitStatusPlugins['TimedCommitStatus']?.rowHeader).toBe('function');
  });

  it('has no plugin registered for ArgoCDCommitStatus', () => {
    expect(commitStatusPlugins['ArgoCDCommitStatus']).toBeUndefined();
  });

  it('registers the internal plugins in the GVK registry at module load', () => {
    // External bundles load later and override by registering the same kind, so
    // the built-ins must already be present before any bundle runs.
    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`)).toBe(
      commitStatusPlugins['TimedCommitStatus'],
    );
    expect(getCommitStatusRowPlugin('ArgoCDCommitStatus')).toBeUndefined();
  });
});
