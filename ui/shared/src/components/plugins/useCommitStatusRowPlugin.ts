import { useEffect, useState } from 'react';
import { getCommitStatusRowPlugin, subscribeToPluginRegistry } from './registry';
import type { RowPlugin } from './types';

/**
 * Returns the row plugin for a commit status GVK, re-rendering if the matching
 * plugin changes.
 *
 * Plugin bundles are fetched at runtime and may register after this component
 * has mounted, so the registry is read on every registry change rather than
 * once. The initial read happens during render so a plugin that registered
 * before mount is picked up without an extra pass.
 */
export function useCommitStatusRowPlugin(
  kind: string | undefined,
  apiVersion?: string,
): RowPlugin | undefined {
  const [plugin, setPlugin] = useState(() => getCommitStatusRowPlugin(kind, apiVersion));

  useEffect(() => {
    const read = () => setPlugin(getCommitStatusRowPlugin(kind, apiVersion));
    // Re-read on subscribe: a bundle may have registered between the initial
    // render and this effect.
    read();
    return subscribeToPluginRegistry(read);
  }, [kind, apiVersion]);

  return plugin;
}
