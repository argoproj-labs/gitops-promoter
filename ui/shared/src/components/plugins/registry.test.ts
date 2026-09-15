import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  ANY_VERSION,
  PROMOTER_GROUP,
  getCommitStatusRowPlugin,
  listCommitStatusRowPlugins,
  registerCommitStatusRowPlugin,
  resetPluginRegistry,
  subscribeToPluginRegistry,
} from './registry';
import type { RowPlugin } from './types';

const headerA = () => null;
const headerB = () => null;
const pluginA: RowPlugin = { rowHeader: headerA };
const pluginB: RowPlugin = { rowHeader: headerB };

describe('commit status row plugin registry', () => {
  beforeEach(() => {
    resetPluginRegistry();
  });

  it('returns a plugin registered for a kind in the promoter group', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(pluginA);
    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`)).toBe(
      pluginA,
    );
  });

  it('returns undefined for an unregistered kind or a missing kind', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('GitCommitStatus')).toBeUndefined();
    expect(getCommitStatusRowPlugin(undefined)).toBeUndefined();
  });

  it('does not match a kind registered under a different group', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus', 'example.com');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', 'example.com/v1')).toBe(pluginA);
    expect(
      getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`),
    ).toBeUndefined();
  });

  it('lets a later registration override an earlier one for the same kind', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    registerCommitStatusRowPlugin(pluginB, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(pluginB);
    expect(listCommitStatusRowPlugins()).toHaveLength(1);
  });

  it('matches any version by default so a CRD version bump keeps rendering', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1beta1`)).toBe(
      pluginA,
    );
  });

  it('prefers a version-pinned registration over a version-agnostic one', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    registerCommitStatusRowPlugin(pluginB, 'TimedCommitStatus', PROMOTER_GROUP, 'v1alpha1');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`)).toBe(
      pluginB,
    );
    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1beta1`)).toBe(
      pluginA,
    );
  });

  it('falls back to the promoter group when apiVersion carries no version', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', PROMOTER_GROUP)).toBe(pluginA);
    expect(getCommitStatusRowPlugin('TimedCommitStatus', '')).toBe(pluginA);
  });

  it('notifies subscribers on registration and stops after unsubscribe', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToPluginRegistry(listener);

    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    expect(listener).toHaveBeenCalledTimes(1);

    unsubscribe();
    registerCommitStatusRowPlugin(pluginB, 'GitCommitStatus');
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('records the registration GVK for diagnostics', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(listCommitStatusRowPlugins()).toEqual([
      {
        group: PROMOTER_GROUP,
        version: ANY_VERSION,
        kind: 'TimedCommitStatus',
        plugin: pluginA,
      },
    ]);
  });
});
