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
 * `$$typeof` symbols for the two wrapper objects React.memo/React.forwardRef
 * return in place of a plain function. A *rendered element* (e.g. `<Foo/>`)
 * also carries a `$$typeof` (`react.element`), so checking for the field's
 * mere presence would wrongly accept an element passed where a component
 * type belongs; check for these two specific symbols instead.
 */
const COMPONENT_WRAPPER_TYPES = new Set([Symbol.for('react.memo'), Symbol.for('react.forward_ref')]);

/**
 * A plain function component, or the object React.memo/React.forwardRef wrap
 * one in — both are valid values for `rowHeader`/`rowContent`, and neither is
 * `typeof value === 'function'`.
 */
function isComponentLike(value: unknown): boolean {
  if (typeof value === 'function') {
    return true;
  }
  return (
    typeof value === 'object' &&
    value !== null &&
    '$$typeof' in value &&
    COMPONENT_WRAPPER_TYPES.has((value as { $$typeof: symbol }).$$typeof)
  );
}

/**
 * Rejects a registration whose `rowHeader` isn't a component. This is the only
 * shape check performed here — a `rowHeader` that renders something reasonable
 * to a JSX call site but throws or misbehaves once rendered is caught later by
 * `PluginErrorBoundary`, which is a render-time concern this registry-time
 * check cannot substitute for.
 *
 * `plugin` is typed loosely because it crosses from untyped, plain-JS external
 * plugin bundles (see developing-ui-plugins.md) into this typed registry -
 * `RowPlugin` describes the intended shape, not a runtime guarantee. `null`/
 * `undefined` must be rejected here rather than throwing, so one malformed
 * registration in a concatenated bundle doesn't abort the rest of that script.
 */
function isValidRowPlugin(plugin: RowPlugin | null | undefined): plugin is RowPlugin {
  if (typeof plugin !== 'object' || plugin === null) {
    console.error('Ignoring plugin registration: plugin must be an object, got', plugin);
    return false;
  }
  if (!isComponentLike(plugin.rowHeader)) {
    console.error(
      'Ignoring plugin registration: rowHeader must be a function component, got',
      plugin.rowHeader,
    );
    return false;
  }
  if (plugin.rowContent !== undefined && !isComponentLike(plugin.rowContent)) {
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
  plugin: RowPlugin | null | undefined,
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
  plugin: RowPlugin | null | undefined,
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

/**
 * Registers `stub` for a GVK for the duration of `body`, then always restores
 * whatever was registered there before - re-registering it if there was one,
 * or removing the stub entirely if there wasn't - even if `body` throws.
 *
 * Exists so a test that swaps in a stub plugin can't accidentally delete a
 * real prior registration (e.g. a built-in plugin) instead of restoring it:
 * that decision is made here, once, rather than left to each call site to
 * get right via its own `if (previous) { register } else { unregister }`.
 * Intended for tests; not a substitute for `resetPluginRegistry` when a test
 * needs a clean registry rather than a scoped swap.
 */
export async function withStubCommitStatusRowPlugin<T>(
  stub: RowPlugin,
  kind: string,
  body: () => T | Promise<T>,
  group: string = PROMOTER_GROUP,
  version: string = ANY_VERSION,
): Promise<T> {
  const key = registryKey(group, version, kind);
  const previous = plugins.get(key);
  registerCommitStatusRowPlugin(stub, kind, group, version);
  try {
    return await body();
  } finally {
    if (previous) {
      plugins.set(key, previous);
    } else {
      plugins.delete(key);
    }
  }
}
