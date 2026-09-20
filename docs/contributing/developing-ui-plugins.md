# Developing UI Plugins

Commit status rows in both the standalone dashboard and the Argo CD UI extension can be
replaced by externally authored plugins. This page covers the plugin contract, how to build
a bundle outside this repository, and how to load it into each surface for testing.

A plugin renders one commit status kind (e.g. `TimedCommitStatus`) with custom UI instead of
the default name/description/link row. It does not require a CRD change: registration is by
group/version/kind (GVK), read off the commit status manager at render time.

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
- `rowContent`, if provided, renders in the history view's detail drawer and drives that row's
  expand/collapse affordance. Omit it for a plain, non-expandable row.
- `check` is the projected view of the commit status; `manager` is the full manager CR
  (e.g. the `TimedCommitStatus` object), including its `status` subresource.

This contract is not published as an npm package. Copy the interfaces you need into your own
plugin's source — see [`gitops-promoter-example-plugin`](#example-plugin) for the pattern. There
is no dependency-version skew to manage: the shapes are small and stable, and copying avoids
requiring plugin authors to consume this repo's build tooling.

## Registering a plugin

A plugin bundle registers itself against the shared registry
([`ui/shared/src/components/plugins/registry.ts`](https://github.com/argoproj-labs/gitops-promoter/blob/main/ui/shared/src/components/plugins/registry.ts))
via a host API installed on `window`:

```ts
window.promoterPluginsAPI?.registerCommitStatusRowPlugin(myPlugin, 'TimedCommitStatus');
```

- `registerCommitStatusRowPlugin(plugin, kind, group?, version?)` — `group` defaults to
  `promoter.argoproj.io`; `version` defaults to matching any version, so a plugin authored
  against `v1alpha1` keeps rendering if the CRD later moves to `v1beta1`.
- **Later registrations win.** Built-in plugins register first, at module load, so an
  externally loaded plugin registering the same kind overrides the built-in row.
- The registry is event-emitting: a plugin that registers after the host has already
  rendered is still picked up, since consumers subscribe to registry changes rather than
  reading it once.

**On the dashboard surface**, `window.promoterPluginsAPI` is installed by the dashboard's own
app bundle, which always executes before `/plugins.js` is loaded — a plain `?.` guard is
sufficient there.

**On the Argo CD extension surface, do not assume `window.promoterPluginsAPI` already exists
when your plugin's top-level code runs — poll for it instead.** Both `extension-promoter.js`
(which installs the API) and any plugin bundle are just separate files that Argo CD's own
server concatenates live, per request, into the single script the browser loads as
`/extensions.js`. Their relative order comes from a lexical directory walk over
`/tmp/extensions/`
([`serveExtensions`](https://github.com/argoproj/argo-cd/blob/master/server/server.go),
`filepath.Walk`, re-run on every request with no caching) — not a guarantee that the promoter's
own registration code has already run by the time your file executes. A `?.`-guarded call that
happens to run first silently no-ops, with no error surfaced anywhere. Poll instead:

```ts
function registerWhenReady(plugin: RowPlugin, kind: string, attempts = 0): void {
  if (window.promoterPluginsAPI) {
    window.promoterPluginsAPI.registerCommitStatusRowPlugin(plugin, kind);
    return;
  }
  if (attempts >= 50) {
    console.error(`promoterPluginsAPI not available after ${attempts} attempts; giving up`);
    return;
  }
  setTimeout(() => registerWhenReady(plugin, kind, attempts + 1), 100);
}

registerWhenReady(myPlugin, 'TimedCommitStatus');
```

This works unchanged on the dashboard too (the API is already there, so the first check
succeeds immediately), so it's the pattern to use regardless of which surface you're targeting.

## React sharing and version constraints

Both UI surfaces publish `React` as `window.React` before loading plugin code, and a plugin
bundle must **externalize `react`** rather than bundling its own copy — two React instances in
the same page breaks hooks with an "invalid hook call" error that gives no indication a
duplicate React is the cause.

The Argo CD UI extension surface can run on **React 16** (Argo CD before 3.5) or React 19
(3.5+), depending on the operator's Argo CD version. A plugin bundle that ships in the
extension must therefore:

- Use the **classic JSX transform** (`React.createElement`), not the automatic runtime. The
  automatic runtime imports from `react/jsx-runtime`, which depends on a global
  (`window.ReactJSXRuntime`) that does not exist before Argo CD 3.5, and pulls in a second
  copy of React transitively when it is missing.
- Restrict hooks to what's available in **React 16.8**: `useState`, `useEffect`, `useRef`,
  `useMemo`, `useCallback`, `useContext`, `useReducer`. Do not use `useId`,
  `useSyncExternalStore`, `useTransition`, `useDeferredValue`, `useOptimistic`,
  `useActionState`, `useFormStatus`, `react-dom/client`, or the `use()` hook — these fail only
  at runtime, only on older Argo CD, which is the worst place to discover it.

The dashboard surface always runs React 19, so a plugin that only ever loads in the dashboard
does not need this restriction — but a plugin intended to load unmodified in both surfaces
does, since it's the same bundle either way.

## Building a bundle

A plugin bundle is a plain script-tag-loadable `.js` file, `library: {type: 'window'}` in
webpack terms — the same shape Argo CD's own UI extensions use. There is no bundler-specific
requirement beyond externalizing React and using the classic JSX transform:

```js
// webpack.config.js
module.exports = {
  entry: './src/index.tsx',
  output: {
    filename: 'plugin-my-plugin.js',
    path: path.resolve(__dirname, 'dist'),
    library: { type: 'window' },
  },
  externals: {
    react: 'React', // react-dom is not needed: a row plugin never mounts its own root
  },
  module: {
    rules: [{ test: /\.tsx?$/, use: 'ts-loader', exclude: /node_modules/ }],
  },
  mode: 'production',
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

`"jsx": "react"` is what selects the classic transform.

The output filename must start with `plugin` and end in `.js` (e.g. `plugin-my-plugin.js`) —
both surfaces' build tooling and the dashboard's runtime loader filter on that convention.

## Example plugin

[`gitops-promoter-example-plugin`](https://github.com/jwinters01/gitops-promoter-example-plugin)
is an alternate `TimedCommitStatus` row, built entirely outside this repository, that
overrides the built-in `TimedCommitStatus` plugin shipped in
[`ui/shared/src/components/plugins/TimedCommitStatus/`](https://github.com/argoproj-labs/gitops-promoter/blob/main/ui/shared/src/components/plugins/TimedCommitStatus/TimedCommitStatus.tsx).
It implements both `rowHeader` and `rowContent`, and is a working reference for the webpack
config, `tsconfig.json`, and registration call described above.

## Loading a plugin for testing

All three loading paths read from `.js` files matching `plugin*.js`. Each file is wrapped in
its own `try/catch` when served, so one broken plugin bundle doesn't take down another or the
host page.

### Dashboard, at runtime (no rebuild)

Point the dashboard at a directory containing your built bundle:

```bash
go run ./cmd dashboard --kubeconfig ~/.kube/config --plugins-dir /path/to/your/plugin/dist
```

The dashboard's `/plugins.js` route reads this directory on every request — drop a new bundle
in, remove one, or replace one, and the change is visible on the next page load with no
restart. This is the same mechanism an init container or sidecar uses to add plugins to a
running deployment without rebuilding the dashboard image. `--plugins-dir` defaults to
`/tmp/plugins`.

### Dashboard, bundled at build time

Copy your bundle into `ui/plugins/` before building the dashboard:

```bash
cp /path/to/your/plugin/dist/plugin-my-plugin.js ui/plugins/
make build-dashboard
```

`ui/dashboard`'s `build:embed` script copies everything in `ui/plugins/` into the dashboard's
build output, which is then embedded into the promoter binary via `//go:embed`. A plugin added
this way ships with the binary and needs no `--plugins-dir` at runtime. Build-time-bundled and
runtime-loaded plugins can coexist: `/plugins.js` serves both, with a runtime plugin of the
same name loading after (and therefore overriding) a build-time one.

### Argo CD UI extension, bundled at build time

Copy your bundle into `ui/plugins/` before building the extension:

```bash
cp /path/to/your/plugin/dist/plugin-my-plugin.js ui/plugins/
make build-extension
```

The extension's webpack build concatenates your plugin bundle onto its own compiled output,
producing a single self-contained `ui/extension/dist/extension-promoter.js`. Nothing in this
file reaches back to the promoter's webserver at runtime — the extension never fetches
`/plugins.js` — so it works even when the Argo CD UI's origin cannot reach the promoter's
origin, which is the normal case in a real deployment. See
[Building and Testing the Argo CD UI Extension](developing-the-argocd-extension.md) for how to
load the built file into a running Argo CD server.

### Argo CD UI extension, loaded at runtime (no rebuild)

A plugin can also be delivered straight into a running Argo CD deployment, without rebuilding
the extension, by piggybacking on the same `/tmp/extensions/` convention
`argocd-extension-installer` already uses for the extension itself
(see [Integrating with Argo CD](../integrating-with-argocd/index.md#ui-extension)). This
requires no Argo CD code changes and no runtime connection from the extension back to the
promoter's webserver — Argo CD's own server (`argocd-server`) already concatenates every file
matching `extension*.js` under `/tmp/extensions/` into the `/extensions.js` response it serves,
on every request, regardless of which init container wrote it.

Add one extra init container per plugin, mounting the same shared `extensions` volume, each
installing its bundle into its own subdirectory so filenames can't collide:

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

Two things this depends on that are easy to get wrong:

- **The plugin's release tarball must be built the same way `argocd-extension-installer`
  expects for any extension** — a top-level `resources/` directory in the tarball, containing a
  subdirectory unique to the plugin, containing the built bundle file. `argocd-extension-installer`
  does a plain `cp -Rf`, so a subdirectory name clash with another plugin (or with
  `gitops-promoter`) silently overwrites files with no error. The bundle file itself must still
  be named to match Argo CD's `extension*.js` pattern, e.g. `extension-plugin-my-plugin.js` —
  this is a different filename convention from the `plugin*.js` pattern used by the dashboard's
  build-time-embed and `--plugins-dir` paths, since it's Argo CD's server doing the matching
  here, not the promoter's.
- **The plugin must use the polling registration pattern from
  [Registering a plugin](#registering-a-plugin), not a `?.`-guarded call.** Because
  `argocd-server` re-walks `/tmp/extensions/` fresh on every request with no caching, the order
  in which `extension-promoter.js` and each plugin's file end up concatenated is not something
  to build a naming convention on alone (Argo CD's own tooling does use a naming convention for
  this — `argocd-extension-installer`'s `EXTENSION_JS_VARS` files are literally named
  `extension-0-*` to sort first — but that only has to survive one write, not every page load
  against a live filesystem walk). Polling for `window.promoterPluginsAPI` is what actually
  makes load order irrelevant.

This is a deployment-time delivery mechanism layered on top of the existing build — it needs no
changes to `ui/extension`'s webpack config or `concat-plugins.mjs`, which continue to serve the
separate, unrelated build-time-embed use case described above.

## Shared plugins directory (`ui/plugins/`)

`ui/plugins/` is not tracked in git and does not exist in a fresh clone — an external plugin
author creates it and places their built bundle there manually. Both `make build-dashboard`
and `make build-extension` tolerate the directory being absent, and read from it when present,
so one bundle placed there ships in both surfaces without either build needing runtime access
to the other.
