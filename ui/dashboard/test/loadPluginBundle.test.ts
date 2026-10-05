import { describe, it, expect, beforeEach, vi } from 'vitest';
import * as ActualReact from 'react';
import { loadPluginBundle } from '../src/loadPluginBundle';
import { resetPluginRegistry } from '@shared/components/plugins/registry';

const hostReact = ActualReact;

describe('loadPluginBundle', () => {
  beforeEach(() => {
    resetPluginRegistry();
    delete window.promoterPluginsAPI;
    window.React = undefined as unknown as typeof ActualReact;
    document.body.innerHTML = '';
  });

  it('installs the host API before appending the script', () => {
    const appendChild = vi.spyOn(document.body, 'appendChild');

    loadPluginBundle(hostReact);

    expect(typeof window.promoterPluginsAPI?.registerCommitStatusRowPlugin).toBe('function');
    expect(appendChild).toHaveBeenCalledTimes(1);
  });

  it('appends a script tag pointed at the given url', () => {
    loadPluginBundle(hostReact, '/custom-plugins.js');

    const script = document.body.querySelector('script');
    expect(script?.src).toContain('/custom-plugins.js');
  });

  it('defaults the url to /plugins.js', () => {
    loadPluginBundle(hostReact);

    const script = document.body.querySelector('script');
    expect(script?.src).toContain('/plugins.js');
  });

  it('resolves without throwing when the script fails to load', () => {
    loadPluginBundle(hostReact, '/plugins.js');

    expect(() =>
      document.body.querySelector('script')?.onerror?.(new Event('error')),
    ).not.toThrow();
  });

  it('asserts the shared React instance once the script loads', () => {
    window.React = hostReact;

    loadPluginBundle(hostReact);

    expect(() => document.body.querySelector('script')?.onload?.(new Event('load'))).not.toThrow();
  });

  it('swallows, rather than throws, when the loaded bundle brought its own React', () => {
    window.React = { ...ActualReact };

    loadPluginBundle(hostReact);

    expect(() => document.body.querySelector('script')?.onload?.(new Event('load'))).not.toThrow();
  });

  it('resolves the returned promise once the script loads', async () => {
    window.React = hostReact;
    const done = loadPluginBundle(hostReact);

    document.body.querySelector('script')?.onload?.(new Event('load'));

    await expect(done).resolves.toBeUndefined();
  });

  it('resolves the returned promise even when the script fails to load', async () => {
    const done = loadPluginBundle(hostReact);

    document.body.querySelector('script')?.onerror?.(new Event('error'));

    await expect(done).resolves.toBeUndefined();
  });

  it('resolves and removes the script if it neither loads nor errors in time', async () => {
    vi.useFakeTimers();

    const done = loadPluginBundle(hostReact);
    expect(document.body.querySelector('script')).not.toBeNull();

    await vi.runAllTimersAsync();
    await expect(done).resolves.toBeUndefined();

    expect(document.body.querySelector('script')).toBeNull();

    vi.useRealTimers();
  });

  it('ignores a late load event after the timeout already resolved', async () => {
    vi.useFakeTimers();

    const done = loadPluginBundle(hostReact);
    const script = document.body.querySelector('script');

    await vi.runAllTimersAsync();
    await done;

    expect(() => script?.onload?.(new Event('load'))).not.toThrow();

    vi.useRealTimers();
  });
});
