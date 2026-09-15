/**
 * Verifies that a loaded plugin bundle resolved `window.React` to the exact
 * React instance the host published, rather than bundling its own copy.
 *
 * A plugin built with `externals: {react: 'React'}` reads `window.React` as
 * its module-scope code executes when the `<script>` tag loads. If that global
 * is missing, or a different object than the host's own React, the plugin ends
 * up running hooks against a different React internals than the host's
 * component tree uses — the classic "invalid hook call" failure, which gives
 * no indication that a duplicate React copy is the actual cause. This check
 * turns that into a clear, immediate error at load time instead.
 */
export function assertSharedReactInstance(hostReact: unknown): void {
  const globalReact = typeof window === 'undefined' ? undefined : window.React;

  if (globalReact === undefined) {
    throw new Error(
      'window.React is missing after loading a plugin bundle: the host has not published its ' +
        'React instance yet, or is running on a version that does not. Plugin bundles that ' +
        "externalize `react` require window.React to be published before they load.",
    );
  }

  if (globalReact !== hostReact) {
    throw new Error(
      'A plugin bundle brought its own copy of React instead of using the shared instance: ' +
        "check that the plugin's build externalizes `react` (e.g. webpack `externals: " +
        '{react: \'React\'}`) and uses the classic (non-automatic) JSX transform, so it reads ' +
        'window.React rather than bundling a private copy.',
    );
  }
}
