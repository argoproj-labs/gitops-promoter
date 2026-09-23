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

export interface CommitStatusRowPluginAnnotationRegistration {
  group: string;
  version: string;
  kind: string;
  annotationKey: string;
  annotationValue: string;
  plugin: RowPlugin;
}

/** Registry key. `version` is part of the key so a plugin can pin one. */
function registryKey(group: string, version: string, kind: string): string {
  return `${group}/${version}/${kind}`;
}

/**
 * Annotation registry key. Annotation registrations are scoped to a GVK, not
 * a standalone dimension, so the key is a GVK key plus the annotation pair.
 */
function annotationRegistryKey(
  group: string,
  version: string,
  kind: string,
  annotationKey: string,
  annotationValue: string,
): string {
  return `${registryKey(group, version, kind)}/${annotationKey}=${annotationValue}`;
}

const plugins = new Map<string, CommitStatusRowPluginRegistration>();
const annotationPlugins = new Map<string, CommitStatusRowPluginAnnotationRegistration>();

/**
 * Rejects a registration whose `rowHeader` isn't callable. This is the only
 * shape check performed here — a `rowHeader` that renders something reasonable
 * to a JSX call site but throws or misbehaves once rendered is caught later by
 * `PluginErrorBoundary`, which is a render-time concern this registry-time
 * check cannot substitute for.
 */
function isValidRowPlugin(plugin: RowPlugin): boolean {
  if (typeof plugin?.rowHeader !== 'function') {
    console.error(
      'Ignoring plugin registration: rowHeader must be a function component, got',
      plugin?.rowHeader,
    );
    return false;
  }
  if (plugin.rowContent !== undefined && typeof plugin.rowContent !== 'function') {
    console.error(
      'Ignoring plugin registration: rowContent must be a function component when provided, got',
      plugin.rowContent,
    );
    return false;
  }
  return true;
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
  if (!isValidRowPlugin(plugin)) {
    return;
  }
  plugins.set(registryKey(group, version, kind), { group, version, kind, plugin });
}

/**
 * Registers a row plugin for a commit status GVK that also carries a given
 * annotation key/value.
 *
 * This lets a plugin author claim specific commit status instances of a kind
 * (e.g. one particular `WebRequestCommitStatus` resource among many) rather
 * than every resource of that kind. The annotation refines a GVK match; it
 * does not stand in for one, so a resource of a different kind never matches
 * regardless of its annotations. A match here takes priority over a plain
 * GVK match in {@link getCommitStatusRowPlugin}. As with GVK registrations, a
 * later registration for the same GVK/annotation combination replaces an
 * earlier one, and omitting `version` matches any version.
 */
export function registerCommitStatusRowPluginByAnnotation(
  plugin: RowPlugin,
  kind: string,
  annotationKey: string,
  annotationValue: string,
  group: string = PROMOTER_GROUP,
  version: string = ANY_VERSION,
): void {
  if (!isValidRowPlugin(plugin)) {
    return;
  }
  annotationPlugins.set(
    annotationRegistryKey(group, version, kind, annotationKey, annotationValue),
    {
      group,
      version,
      kind,
      annotationKey,
      annotationValue,
      plugin,
    },
  );
}

/**
 * Returns the plugin registered for a commit status, preferring a GVK+annotation
 * match over a plain GVK match, and within each preferring an exact version
 * match over a version-agnostic one.
 *
 * `apiVersion` is the manager's `apiVersion` as served in the bundle
 * (`group/version`); a bare group or an empty string is treated as no version.
 * `annotations` is the manager's `metadata.annotations`. If more than one
 * annotation on the resource matches a registration, which one wins is
 * unspecified — plugin authors should not register conflicting annotations
 * for the same GVK.
 */
export function getCommitStatusRowPlugin(
  kind: string | undefined,
  apiVersion?: string,
  annotations?: Record<string, string>,
): RowPlugin | undefined {
  if (!kind) {
    return undefined;
  }
  const [group = PROMOTER_GROUP, version] = splitApiVersion(apiVersion);

  if (annotations) {
    for (const [key, value] of Object.entries(annotations)) {
      if (version) {
        const exact = annotationPlugins.get(
          annotationRegistryKey(group, version, kind, key, value),
        );
        if (exact) {
          return exact.plugin;
        }
      }
      const anyVersion = annotationPlugins.get(
        annotationRegistryKey(group, ANY_VERSION, kind, key, value),
      );
      if (anyVersion) {
        return anyVersion.plugin;
      }
    }
  }

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

/** All current GVK registrations, for diagnostics and tests. */
export function listCommitStatusRowPlugins(): CommitStatusRowPluginRegistration[] {
  return [...plugins.values()];
}

/** All current GVK+annotation registrations, for diagnostics and tests. */
export function listCommitStatusRowPluginsByAnnotation(): CommitStatusRowPluginAnnotationRegistration[] {
  return [...annotationPlugins.values()];
}

/** Clears every registration. Intended for tests. */
export function resetPluginRegistry(): void {
  plugins.clear();
  annotationPlugins.clear();
}
