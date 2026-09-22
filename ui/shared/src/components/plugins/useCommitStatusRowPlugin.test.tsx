/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import { useCommitStatusRowPlugin } from './useCommitStatusRowPlugin';
import {
  PROMOTER_GROUP,
  registerCommitStatusRowPlugin,
  registerCommitStatusRowPluginByAnnotation,
  resetPluginRegistry,
} from './registry';
import type { RowPlugin } from './types';

const pluginA: RowPlugin = { rowHeader: () => React.createElement('span', null, 'A') };
const pluginB: RowPlugin = { rowHeader: () => React.createElement('span', null, 'B') };

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

describe('useCommitStatusRowPlugin', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    resetPluginRegistry();
    container = document.createElement('div');
    document.body.appendChild(container);
    vi.useFakeTimers();
  });

  afterEach(() => {
    root?.unmount();
    container.remove();
    vi.useRealTimers();
  });

  const render = async (props: {
    kind?: string;
    apiVersion?: string;
    annotations?: Record<string, string>;
  }) => {
    root = createRoot(container);
    root.render(React.createElement(Probe, props));
    await vi.advanceTimersByTimeAsync(0);
  };

  it('resolves a plugin registered before mount', async () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    await render({ kind: 'TimedCommitStatus', apiVersion: `${PROMOTER_GROUP}/v1alpha1` });

    expect(container.textContent).toBe('A');
  });

  it('renders nothing for a kind with no plugin', async () => {
    await render({ kind: 'GitCommitStatus' });

    expect(container.textContent).toBe('none');
  });

  it('picks up a plugin registered after mount', async () => {
    // The case the subscription exists for: plugin bundles are fetched at
    // runtime and can register after the row has already rendered.
    await render({ kind: 'TimedCommitStatus' });
    expect(container.textContent).toBe('none');

    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    await vi.advanceTimersByTimeAsync(0);

    expect(container.textContent).toBe('A');
  });

  it('re-renders when a later registration overrides the plugin', async () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    await render({ kind: 'TimedCommitStatus' });
    expect(container.textContent).toBe('A');

    registerCommitStatusRowPlugin(pluginB, 'TimedCommitStatus');
    await vi.advanceTimersByTimeAsync(0);

    expect(container.textContent).toBe('B');
  });

  it('ignores registrations for other kinds', async () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    await render({ kind: 'TimedCommitStatus' });

    registerCommitStatusRowPlugin(pluginB, 'GitCommitStatus');
    await vi.advanceTimersByTimeAsync(0);

    expect(container.textContent).toBe('A');
  });

  it('resolves a plugin by GVK+annotation, preferring it over a plain GVK match', async () => {
    registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
    registerCommitStatusRowPluginByAnnotation(
      pluginB,
      'WebRequestCommitStatus',
      'example.com/plugin',
      'my-plugin',
    );

    await render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'my-plugin' },
    });

    expect(container.textContent).toBe('B');
  });

  it('ignores a GVK+annotation registration for a different kind', async () => {
    registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
    registerCommitStatusRowPluginByAnnotation(
      pluginB,
      'GitCommitStatus',
      'example.com/plugin',
      'my-plugin',
    );

    await render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'my-plugin' },
    });

    expect(container.textContent).toBe('A');
  });

  it('re-renders when the annotations prop changes to match a different plugin', async () => {
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

    await render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'plugin-a' },
    });
    expect(container.textContent).toBe('A');

    await render({
      kind: 'WebRequestCommitStatus',
      annotations: { 'example.com/plugin': 'plugin-b' },
    });
    expect(container.textContent).toBe('B');
  });
});
