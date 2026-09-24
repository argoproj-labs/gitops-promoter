import { installPluginHostApi, assertSharedReactInstance } from '@shared/components/plugins';

// A stalled /plugins.js request (rather than a clean load or error) would
// otherwise hang the dashboard's first render forever, since script.onload
// and script.onerror are the only two events loadPluginBundle waits on.
const PLUGIN_LOAD_TIMEOUT_MS = 5000;

/**
 * Installs the plugin host API and loads the external plugin bundle script,
 * resolving once the script has run (or failed to load) so the caller can
 * delay its first render until every plugin bundle has had a chance to
 * register.
 *
 * `script.src` (rather than fetching text and inlining it) lets the browser's
 * own HTTP cache handle revalidation against the `/plugins.js` route's ETag,
 * and preserves stack traces/sourcemapping for plugin code.
 *
 * Dashboard-only: the ArgoCD UI extension gets its plugin code concatenated
 * into its own build output instead of fetching a script at runtime.
 */
export function loadPluginBundle(hostReact: unknown, url = '/plugins.js'): Promise<void> {
  installPluginHostApi();

  if (typeof document === 'undefined') {
    return Promise.resolve();
  }

  return new Promise<void>((resolve) => {
    const script = document.createElement('script');
    script.src = url;

    let settled = false;
    const timer = setTimeout(() => {
      if (settled) {
        return;
      }
      settled = true;
      script.onload = null;
      script.onerror = null;
      script.remove();
      resolve();
    }, PLUGIN_LOAD_TIMEOUT_MS);

    script.onload = () => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      try {
        assertSharedReactInstance(hostReact);
      } catch {
        // deliberately swallowed: mismatch is non-fatal, first render must still proceed
      }
      resolve();
    };
    script.onerror = () => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      resolve();
    };
    document.body.appendChild(script);
  });
}
