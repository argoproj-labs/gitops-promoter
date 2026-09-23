import { installPluginHostApi, assertSharedReactInstance } from '@shared/components/plugins';

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
    script.onload = () => {
      try {
        assertSharedReactInstance(hostReact);
      } catch (err) {
        console.error(err instanceof Error ? err.message : err);
      }
      resolve();
    };
    script.onerror = () => {
      console.error(`Failed to load plugin bundle from ${url}`);
      resolve();
    };
    document.body.appendChild(script);
  });
}
