import { describe, it, expect, beforeEach } from 'vitest';
import { installPluginHostApi } from './hostApi';
import { getCommitStatusRowPlugin, resetPluginRegistry } from './registry';
import type { RowPlugin } from './types';

const plugin: RowPlugin = { rowHeader: () => null };

describe('installPluginHostApi', () => {
  beforeEach(() => {
    resetPluginRegistry();
    delete window.promoterPluginsAPI;
  });

  it('exposes the register function on window', () => {
    installPluginHostApi();

    expect(typeof window.promoterPluginsAPI?.registerCommitStatusRowPlugin).toBe('function');
  });

  it('registers a plugin through the window API, as a bundle would', () => {
    installPluginHostApi();

    window.promoterPluginsAPI?.registerCommitStatusRowPlugin(plugin, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(plugin);
  });

  it('is idempotent and keeps the first API object', () => {
    const first = installPluginHostApi();
    const second = installPluginHostApi();

    expect(second).toBe(first);
    expect(window.promoterPluginsAPI).toBe(first);
  });
});
