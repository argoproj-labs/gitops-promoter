# Developing UI Plugins

Commit status rows in both the standalone dashboard and the Argo CD UI extension can be replaced by externally authored plugins. This page covers the plugin contract, how to build a bundle outside this repository, and how to load it into each surface for testing.

A plugin renders one commit status kind (e.g. `TimedCommitStatus`) with custom UI instead of the default name/description/link row. It does not require a CRD change: registration is by group/version/kind (GVK), optionally refined by an annotation on the commit status manager, read off it at render time.

## The plugin contract

Defined in [`ui/shared/src/components/plugins/types.ts`](https://github.com/argoproj-labs/gitops-promoter/blob/main/ui/shared/src/components/plugins/types.ts):

```ts
interface CommitStatusContext {
  check: Check;
  manager: CommitStatusManager;
}

interface RowPlugin {
  rowHeader: React.FC<CommitStatusContext>;
  rowContent?: React.FC<CommitStatusContext>;
}
```

- `rowHeader` renders in place of the default row (name, description, state, optional link).
- `rowContent`, if provided, renders in the history view's detail drawer and drives that row's expand/collapse affordance. Omit it for a plain, non-expandable row.
- `check` is the projected view of the commit status; `manager` is the full manager CR (e.g. the `TimedCommitStatus` object), including its `status` subresource.

`check` (defined in [`ui/shared/src/types/promotion.ts`](https://github.com/argoproj-labs/gitops-promoter/blob/main/ui/shared/src/types/promotion.ts)) also carries data from the surrounding `PromotionStrategy`:

```ts
interface Check {
  name: string;
  status: string;
  description?: string;
  url?: string;
  branch: string;
  kind?: string;
  apiVersion?: string;
  manager?: CommitStatusManager;
  promotionStrategy?: PromotionStrategy;
  environment?: Environment;
  activeDrySha?: string;
  activeHydratedSha?: string;
  proposedDrySha?: string;
  proposedHydratedSha?: string;
}
```

This contract is not published as an npm package. Copy the interfaces you need into your own plugin's source; the shapes are small and stable enough that this shouldn't drift.

## Registering a plugin

A plugin bundle registers itself against the shared registry ([`ui/shared/src/components/plugins/registry.ts`](https://github.com/argoproj-labs/gitops-promoter/blob/main/ui/shared/src/components/plugins/registry.ts)) via a host API installed on `window`:

```ts
window.promoterPluginsAPI?.registerCommitStatusRowPlugin(
  myPlugin,
  "TimedCommitStatus",
);
```

- `registerCommitStatusRowPlugin(plugin, kind, group?, version?)` claims every resource of that kind. `group` defaults to `promoter.argoproj.io`; `version` defaults to matching any version, so a plugin authored against `v1alpha1` keeps rendering if the CRD later moves to `v1beta1`. To claim one specific resource instead of every resource of a kind, register by annotation — see [Claiming a specific commit status instance](#claiming-a-specific-commit-status-instance) below.
- **Later registrations win.** Built-in plugins register first, at module load, so an externally loaded plugin registering the same kind overrides the built-in row.
- **Register before the app's first render.** The registry is read once per row, not subscribed to. A plugin that registers after a row has already rendered isn't picked up until something else re-renders that row. Both surfaces guarantee registration happens before mount — the dashboard awaits the plugin script before rendering; the extension runs its plugin code as part of the same script that installs the host API, in file order.

**On the dashboard**, `window.promoterPluginsAPI` is installed before `/plugins.js` loads, so a plain `?.` guard is fine.

**On the Argo CD extension, it depends on which path you're using:**

- **Built into the extension at build time**: your plugin's code is appended to the extension's own compiled output, after the code that installs the host API. A plain `?.` guard is safe here too.
- **Delivered at runtime via an init container**: your plugin and the promoter's extension bundle are separate files, concatenated live by Argo CD's server in whatever order a directory walk over `/tmp/extensions/` returns ([`serveExtensions`](https://github.com/argoproj/argo-cd/blob/master/server/server.go), `filepath.Walk`, re-run on every request). A `?.` guard can lose this race and silently no-op. Poll instead — see [loaded at runtime](#argo-cd-ui-extension-loaded-at-runtime-no-rebuild) below.

### Claiming a specific commit status instance

`registerCommitStatusRowPlugin` claims every commit status resource of a given kind. To claim one specific resource instead — say, one particular `WebRequestCommitStatus` among several — register by annotation:

```ts
window.promoterPluginsAPI?.registerCommitStatusRowPluginByAnnotation(
  myPlugin,
  "WebRequestCommitStatus",
  "example.com/plugin",
  "my-plugin",
);
```

and put the matching annotation on the commit status manager resource:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  annotations:
    example.com/plugin: my-plugin
```

- The annotation refines a GVK match, it doesn't replace one — a resource of a different kind never matches, regardless of annotations.
- An annotation match wins over a plain GVK match for the same resource.
- If more than one annotation matches, which one wins is unspecified. Don't register conflicting annotations for the same GVK.
- `group` and `version` are optional trailing arguments, same defaults as `registerCommitStatusRowPlugin`.

## React sharing and version constraints

Both UI surfaces publish `React` as `window.React` before loading plugin code. A plugin bundle must **externalize `react`** rather than bundle its own copy — two React instances on the same page break hooks with an "invalid hook call" error that doesn't hint at the real cause.

The extension surface can run on **React 16** (Argo CD before 3.5) or React 19 (3.5+), depending on the operator's Argo CD version. A plugin shipping in the extension must:

- Use the **classic JSX transform** (`React.createElement`), not the automatic runtime. The automatic runtime needs `window.ReactJSXRuntime`, which doesn't exist before Argo CD 3.5, and pulls in a second copy of React when it's missing.
- Restrict hooks to the **React 16.8** surface: `useState`, `useEffect`, `useRef`, `useMemo`, `useCallback`, `useContext`, `useReducer`. Avoid `useId`, `useSyncExternalStore`, `useTransition`, `useDeferredValue`, `useOptimistic`, `useActionState`, `useFormStatus`, `react-dom/client`, and `use()` — these only fail at runtime, on older Argo CD.

The dashboard always runs React 19, so a dashboard-only plugin doesn't need this restriction. A plugin meant to load unmodified on both surfaces does, since it's the same bundle either way.

## Building a bundle

A plugin bundle is a plain script-tag-loadable `.js` file, `library: {type: 'window'}` in webpack terms — the same shape Argo CD's own UI extensions use:

```js
// webpack.config.js
module.exports = {
  entry: "./src/index.tsx",
  output: {
    filename: "plugin-my-plugin.js",
    path: path.resolve(__dirname, "dist"),
    library: { type: "window" },
  },
  externals: {
    react: "React", // react-dom isn't needed: a row plugin never mounts its own root
  },
  module: {
    rules: [{ test: /\.tsx?$/, use: "ts-loader", exclude: /node_modules/ }],
  },
  mode: "production",
};
```

```json
// tsconfig.json
{
  "compilerOptions": {
    "jsx": "react"
  }
}
```

`"jsx": "react"` selects the classic transform.

The output filename must start with `plugin` and end in `.js` (e.g. `plugin-my-plugin.js`). Both surfaces' build tooling and the dashboard's runtime loader filter on that convention.

## Loading a plugin for testing

There are four ways to load a plugin. The first three touch promoter code and all read `plugin*.js` files: each is syntax-checked before concatenation (a parse error is skipped and logged rather than taking down the whole bundle) and wrapped in its own `try/catch` when served, so a runtime throw in one plugin doesn't take down another. The fourth path — an init container into a running Argo CD deployment — is entirely Argo CD's own concatenation; the promoter doesn't validate anything there.

### Dashboard, at runtime (no rebuild)

Point the dashboard at a directory containing your built bundle:

```bash
go run ./cmd dashboard --kubeconfig ~/.kube/config --plugins-dir /path/to/your/plugin/dist
```

`/plugins.js` reads this directory once at startup and caches the result for the life of the process. Dropping in, removing, or replacing a bundle has no effect until the dashboard restarts. `--plugins-dir` defaults to `/tmp/plugins`.

Two things to watch for:

- **Symlinks aren't followed.** A symlinked `plugin*.js` entry is skipped and logged. This bites you with Kubernetes ConfigMap or projected volumes, which mount files as symlinks into a hidden `..data/` directory — mounting a plugin that way gives you an empty `/plugins.js`. Copy the bundle into a real directory instead (e.g. via an init container).
- **A directory, or an unreadable file, named `plugin*.js` aborts dashboard startup** instead of being skipped and logged like malformed JS content is. Keep that filename pattern reserved for actual plugin bundles.

### Dashboard, bundled at build time

Copy your bundle into `ui/plugins/` before building the dashboard:

```bash
cp /path/to/your/plugin/dist/plugin-my-plugin.js ui/plugins/
make build-dashboard
```

`ui/dashboard`'s `build:embed` script copies `plugin*.js` files from `ui/plugins/` into the build output, which is then embedded into the promoter binary via `//go:embed`. A plugin added this way ships with the binary and needs no `--plugins-dir`. Build-time and runtime plugins can coexist: `/plugins.js` serves both, with a runtime plugin of the same name overriding a build-time one.

### Argo CD UI extension, bundled at build time

Copy your bundle into `ui/plugins/` before building the extension:

```bash
cp /path/to/your/plugin/dist/plugin-my-plugin.js ui/plugins/
make build-extension
```

The extension's webpack build concatenates your plugin bundle onto its own compiled output into one self-contained `ui/extension/dist/extension-promoter.js`. The extension never fetches `/plugins.js` at runtime, so this works even when the Argo CD UI's origin can't reach the promoter's — the normal case in a real deployment. See [Building and Testing the Argo CD UI Extension](developing-the-argocd-extension.md) for loading the built file into a running Argo CD server.

### Argo CD UI extension, loaded at runtime (no rebuild)

A plugin can also be delivered straight into a running Argo CD deployment, without rebuilding the extension, by piggybacking on the same `/tmp/extensions/` convention `argocd-extension-installer` uses for the extension itself (see [Integrating with Argo CD](../integrating-with-argocd/index.md#ui-extension)). No Argo CD code changes, no runtime connection back to the promoter — `argocd-server` already concatenates every `extension*.js` file under `/tmp/extensions/` into `/extensions.js`.

Add one extra init container per plugin, mounting the same shared `extensions` volume, each writing its bundle into its own subdirectory so filenames can't collide:

```yaml
initContainers:
  - name: extension-gitops-promoter
    image: quay.io/argoprojlabs/argocd-extension-installer:v0.0.9@sha256:d2b43c18ac1401f579f6d27878f45e253d1e3f30287471ae74e6a4315ceb0611
    env:
      - name: EXTENSION_NAME
        value: gitops-promoter
      - name: EXTENSION_URL
        value: https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.38.1/gitops-promoter-argocd-extension.tar.gz
      - name: EXTENSION_CHECKSUM_URL
        value: https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.38.1/gitops-promoter_0.38.1_checksums.txt
    volumeMounts:
      - name: extensions
        mountPath: /tmp/extensions/
  - name: extension-plugin-my-plugin
    image: quay.io/argoprojlabs/argocd-extension-installer:v0.0.9@sha256:d2b43c18ac1401f579f6d27878f45e253d1e3f30287471ae74e6a4315ceb0611
    env:
      - name: EXTENSION_NAME
        value: plugin-my-plugin
      - name: EXTENSION_URL
        value: https://example.com/releases/my-plugin.tar.gz
      - name: EXTENSION_CHECKSUM_URL
        value: https://example.com/releases/my-plugin_checksums.txt
    volumeMounts:
      - name: extensions
        mountPath: /tmp/extensions/
containers:
  - name: argocd-server
    volumeMounts:
      - name: extensions
        mountPath: /tmp/extensions/
volumes:
  - name: extensions
    emptyDir: {}
```

Two things to get right:

- **Build the release tarball the way `argocd-extension-installer` expects any extension**: a top-level `resources/` directory, containing a subdirectory unique to the plugin, containing the built bundle file. The installer does a plain `cp -Rf`, so a subdirectory name clash with another plugin (or with `gitops-promoter`) silently overwrites files. The bundle file itself must match Argo CD's `extension*.js` pattern — e.g. `extension-plugin-my-plugin.js` — a different convention from the `plugin*.js` used by the dashboard paths, since Argo CD's server does the matching here, not the promoter's.
- **Poll for `window.promoterPluginsAPI` instead of a `?.`-guarded call.** `argocd-server` re-walks `/tmp/extensions/` fresh on every request with no caching, so load order isn't something to rely on (Argo CD's own installer does name its files `extension-0-*` to sort first, but that only has to survive one write, not every page load against a live filesystem walk):

  ```ts
  function registerWhenReady(
    plugin: RowPlugin,
    kind: string,
    attempts = 0,
  ): void {
    if (window.promoterPluginsAPI) {
      window.promoterPluginsAPI.registerCommitStatusRowPlugin(plugin, kind);
      return;
    }
    if (attempts >= 50) {
      console.error(
        `promoterPluginsAPI not available after ${attempts} attempts; giving up`,
      );
      return;
    }
    setTimeout(() => registerWhenReady(plugin, kind, attempts + 1), 100);
  }

  registerWhenReady(myPlugin, "TimedCommitStatus");
  ```

  This is only needed for this delivery path — a plugin built into the extension at build time, or loaded into the dashboard, can use a plain `?.` guard.

This delivery mechanism is layered on top of the existing build. It needs no changes to `ui/extension`'s webpack config or `concat-plugins.mjs`.

## Shared plugins directory (`ui/plugins/`)

`ui/plugins/` isn't tracked in git and doesn't exist in a fresh clone — create it yourself and place your built bundle there. Both `make build-dashboard` and `make build-extension` tolerate the directory being absent, and read from it when present, so one bundle placed there ships in both surfaces.
