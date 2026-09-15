import type { RowPlugin } from './types';

/**
 * Group of the promoter CRDs a plugin can target. Plugins name the commit status
 * they render by GVK, so a plugin authored for a kind the UI does not yet know
 * about still registers.
 */
export const PROMOTER_GROUP = 'promoter.argoproj.io';

/** Version a plugin registration matches when it does not name one. */
export const ANY_VERSION = '*';

export interface CommitStatusRowPluginRegistration {
  group: string;
  version: string;
  kind: string;
  plugin: RowPlugin;
}

export type PluginRegistryListener = () => void;

/** Registry key. `version` is part of the key so a plugin can pin one. */
function registryKey(group: string, version: string, kind: string): string {
  return `${group}/${version}/${kind}`;
}

const plugins = new Map<string, CommitStatusRowPluginRegistration>();
const listeners = new Set<PluginRegistryListener>();

function notify(): void {
  for (const listener of listeners) {
    listener();
  }
}

/**
 * Registers a row plugin for a commit status GVK.
 *
 * Later registrations replace earlier ones for the same GVK, so an externally
 * loaded plugin overrides the built-in row for that kind. Internal plugins are
 * registered at module load, before any external bundle runs.
 *
 * Omitting `version` matches any version: a plugin authored against v1alpha1
 * should keep rendering when the CRD moves to v1beta1 rather than silently
 * disappearing.
 */
export function registerCommitStatusRowPlugin(
  plugin: RowPlugin,
  kind: string,
  group: string = PROMOTER_GROUP,
  version: string = ANY_VERSION,
): void {
  plugins.set(registryKey(group, version, kind), { group, version, kind, plugin });
  notify();
}

/**
 * Returns the plugin registered for a commit status GVK, preferring an exact
 * version match over a version-agnostic one.
 *
 * `apiVersion` is the manager's `apiVersion` as served in the bundle
 * (`group/version`); a bare group or an empty string is treated as no version.
 */
export function getCommitStatusRowPlugin(
  kind: string | undefined,
  apiVersion?: string,
): RowPlugin | undefined {
  if (!kind) {
    return undefined;
  }
  const [group = PROMOTER_GROUP, version] = splitApiVersion(apiVersion);
  if (version) {
    const exact = plugins.get(registryKey(group, version, kind));
    if (exact) {
      return exact.plugin;
    }
  }
  return plugins.get(registryKey(group, ANY_VERSION, kind))?.plugin;
}

/** Splits `group/version` into its parts, tolerating a missing version. */
function splitApiVersion(apiVersion?: string): [string | undefined, string | undefined] {
  if (!apiVersion) {
    return [undefined, undefined];
  }
  const slash = apiVersion.lastIndexOf('/');
  if (slash === -1) {
    return [apiVersion, undefined];
  }
  return [apiVersion.slice(0, slash), apiVersion.slice(slash + 1)];
}

/**
 * Subscribes to registry changes and returns an unsubscribe function.
 *
 * External bundles load asynchronously and may register after the consuming
 * component has mounted, so consumers subscribe rather than reading once.
 */
export function subscribeToPluginRegistry(listener: PluginRegistryListener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** All current registrations, for diagnostics and tests. */
export function listCommitStatusRowPlugins(): CommitStatusRowPluginRegistration[] {
  return [...plugins.values()];
}

/** Clears every registration. Intended for tests. */
export function resetPluginRegistry(): void {
  plugins.clear();
  notify();
}
