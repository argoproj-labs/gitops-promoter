import { describe, it, expect, beforeEach, vi } from 'vitest';
import * as ActualReact from 'react';
import { loadPluginBundle } from './loadPluginBundle';
import { resetPluginRegistry } from './registry';

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

  it('logs a clear error when the script fails to load', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    loadPluginBundle(hostReact, '/plugins.js');
    document.body.querySelector('script')?.onerror?.(new Event('error'));

    expect(consoleErrorSpy).toHaveBeenCalledWith('Failed to load plugin bundle from /plugins.js');
    consoleErrorSpy.mockRestore();
  });

  it('asserts the shared React instance once the script loads', () => {
    window.React = hostReact;
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    loadPluginBundle(hostReact);
    document.body.querySelector('script')?.onload?.(new Event('load'));

    expect(consoleErrorSpy).not.toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });

  it('logs, rather than throws, when the loaded bundle brought its own React', () => {
    window.React = { ...ActualReact };
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    loadPluginBundle(hostReact);
    expect(() => document.body.querySelector('script')?.onload?.(new Event('load'))).not.toThrow();

    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringMatching(/brought its own copy/));
    consoleErrorSpy.mockRestore();
  });
});
