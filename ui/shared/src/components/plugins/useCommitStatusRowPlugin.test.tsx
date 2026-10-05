/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { useCommitStatusRowPlugin } from './useCommitStatusRowPlugin';
import { PluginErrorBoundary } from './PluginErrorBoundary';
import {
  PROMOTER_GROUP,
  registerCommitStatusRowPlugin,
  registerCommitStatusRowPluginByAnnotation,
  resetPluginRegistry,
} from './registry';
import type { RowPlugin } from './types';

const pluginA: RowPlugin = { rowHeader: () => React.createElement('span', null, 'A') };
const pluginB: RowPlugin = { rowHeader: () => React.createElement('span', null, 'B') };
const crashingPlugin: RowPlugin = {
  rowHeader: () => {
    throw new Error('boom');
  },
};

// Renders whatever the hook resolves, so the DOM reflects the current plugin.
const Probe: React.FC<{
  kind?: string;
  apiVersion?: string;
  annotations?: Record<string, string>;
}> = ({ kind, apiVersion, annotations }) => {
  const plugin = useCommitStatusRowPlugin(kind, apiVersion, annotations);
  if (!plugin) {
    return React.createElement('span', null, 'none');
  }
  return React.createElement(plugin.rowHeader, {
    check: { name: 'c', status: 'pending', branch: 'production' },
    manager: {} as never,
  });
};

// Mirrors how HealthCheckItem/DrawerCheckItem wire the hook's result into
// PluginErrorBoundary, so this exercises the same registration-to-fallback
// path a user actually sees when a registered plugin crashes while rendering.
const ProbeWithBoundary: React.FC<{ kind?: string }> = ({ kind }) => {
  const plugin = useCommitStatusRowPlugin(kind);
  const check = { name: 'c', status: 'pending', branch: 'production' };
  if (!plugin) {
    return React.createElement('span', null, 'default row');
  }
  return React.createElement(PluginErrorBoundary, {
    key: kind,
    pluginKind: kind,
    fallback: React.createElement('span', null, 'default row'),
    children: React.createElement(plugin.rowHeader, { check, manager: {} as never }),
  });
};

describe('useCommitStatusRowPlugin', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    resetPluginRegistry();
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => {
    root?.unmount();
    root = undefined as unknown as ReturnType<typeof createRoot>;
    container.remove();
  });

  const render = (props: {
    kind?: string;
    apiVersion?: string;
    annotations?: Record<string, string>;
  }) => {
    root ??= createRoot(container);
    act(() => {
      root.render(React.createElement(Probe, props));
    });
  };

  it('resolves a plugin registered before render', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    render({ kind: 'TimedCommitStatus', apiVersion: `${PROMOTER_GROUP}/v1alpha1` });

    expect(container.textContent).toBe('A');
  });

  it('renders nothing for a kind with no plugin', () => {
    render({ kind: 'GitCommitStatus' });

    expect(container.textContent).toBe('none');
  });

  it('resolves a plugin by GVK+annotation, preferring it over a plain GVK match', () => {
    registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
    registerCommitStatusRowPluginByAnnotation(
      pluginB,
      'WebRequestCommitStatus',
      'example.com/plugin',
      'my-plugin',
    );

    render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'my-plugin' },
    });

    expect(container.textContent).toBe('B');
  });

  it('ignores a GVK+annotation registration for a different kind', () => {
    registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
    registerCommitStatusRowPluginByAnnotation(
      pluginB,
      'GitCommitStatus',
      'example.com/plugin',
      'my-plugin',
    );

    render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'my-plugin' },
    });

    expect(container.textContent).toBe('A');
  });

  it('re-renders when the annotations prop changes to match a different plugin', () => {
    registerCommitStatusRowPluginByAnnotation(
      pluginA,
      'WebRequestCommitStatus',
      'example.com/plugin',
      'plugin-a',
    );
    registerCommitStatusRowPluginByAnnotation(
      pluginB,
      'WebRequestCommitStatus',
      'example.com/plugin',
      'plugin-b',
    );

    render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'plugin-a' },
    });
    expect(container.textContent).toBe('A');

    render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'plugin-b' },
    });
    expect(container.textContent).toBe('B');
  });

  it('end-to-end: a registered plugin that throws while rendering falls back to the default row', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    registerCommitStatusRowPlugin(crashingPlugin, 'TimedCommitStatus');

    root = createRoot(container);
    act(() => {
      root.render(React.createElement(ProbeWithBoundary, { kind: 'TimedCommitStatus' }));
    });

    expect(container.textContent).toBe('default row');
    consoleErrorSpy.mockRestore();
  });
});
