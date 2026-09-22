import { useEffect, useRef, useState } from 'react';
import { getCommitStatusRowPlugin, subscribeToPluginRegistry } from './registry';
import type { RowPlugin } from './types';

/**
 * Returns the row plugin for a commit status, re-rendering if the matching
 * plugin changes.
 *
 * Plugin bundles are fetched at runtime and may register after this component
 * has mounted, so the registry is read on every registry change rather than
 * once. The initial read happens during render so a plugin that registered
 * before mount is picked up without an extra pass.
 *
 * `annotations` comes in as a fresh object on every render of the caller, so
 * it is read through a ref rather than placed in the effect's dependency
 * array; a `JSON.stringify` of it is used as the dependency instead, keeping
 * the effect (and the registry read it triggers) from re-running when the
 * annotation contents haven't actually changed.
 */
export function useCommitStatusRowPlugin(
  kind: string | undefined,
  apiVersion?: string,
  annotations?: Record<string, string>,
): RowPlugin | undefined {
  const annotationsRef = useRef(annotations);
  annotationsRef.current = annotations;
  const annotationsKey = annotations ? JSON.stringify(annotations) : undefined;

  const [plugin, setPlugin] = useState(() =>
    getCommitStatusRowPlugin(kind, apiVersion, annotationsRef.current),
  );

  useEffect(() => {
    const read = () => setPlugin(getCommitStatusRowPlugin(kind, apiVersion, annotationsRef.current));
    // Re-read on subscribe: a bundle may have registered between the initial
    // render and this effect.
    read();
    return subscribeToPluginRegistry(read);
  }, [kind, apiVersion, annotationsKey]);

  return plugin;
}
