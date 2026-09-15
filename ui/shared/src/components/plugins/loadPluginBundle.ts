import { installPluginHostApi } from './hostApi';
import { assertSharedReactInstance } from './reactSharing';

/**
 * Installs the plugin host API and loads the external plugin bundle script.
 *
 * `script.src` (rather than fetching text and inlining it) lets the browser's
 * own HTTP cache handle revalidation against the `/plugins.js` route's ETag,
 * and preserves stack traces/sourcemapping for plugin code. The script is
 * loaded without `async`/`defer` ordering guarantees relative to the host's
 * own render: `useCommitStatusRowPlugin` subscribes to registry changes, so a
 * plugin that registers after the host has already rendered is picked up on
 * its own, and loading does not need to block or precede the main app.
 */
export function loadPluginBundle(hostReact: unknown, url = '/plugins.js'): void {
  installPluginHostApi();

  if (typeof document === 'undefined') {
    return;
  }

  const script = document.createElement('script');
  script.src = url;
  script.onload = () => {
    try {
      assertSharedReactInstance(hostReact);
    } catch (err) {
      console.error(err instanceof Error ? err.message : err);
    }
  };
  script.onerror = () => {
    console.error(`Failed to load plugin bundle from ${url}`);
  };
  document.body.appendChild(script);
}
