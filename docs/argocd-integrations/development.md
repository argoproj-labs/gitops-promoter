# Building and Testing the Argo CD UI Extension

This page describes how to build the GitOps Promoter Argo CD UI extension locally and load it into a running Argo CD server for testing.

## Prerequisites

- Node.js and npm
- Argo CD running in-cluster (e.g. in the `argocd` namespace)
- `kubectl` configured to access the cluster

## Build the extension

From the repository root:

```bash
make build-extension
```

The built file is written to `ui/extension/dist/extension-promoter.js`.

## Load your build into Argo CD

The server serves extension scripts from `/tmp/extensions/` inside the `argocd-server` pod. Any file whose name matches `extension*.js` is concatenated and served at the `/extensions.js` endpoint. There is no server-side caching.

### If the extension init container is **not** installed

Copy your build into the pod and refresh the UI:

```bash
POD=$(kubectl get pods -n argocd -l app.kubernetes.io/name=argocd-server -o jsonpath='{.items[0].metadata.name}')
kubectl cp ui/extension/dist/extension-promoter.js argocd/$POD:/tmp/extensions/extension-gitops-promoter.js -n argocd -c argocd-server
```

Then hard-refresh the Argo CD UI (e.g. Cmd+Shift+R or Ctrl+Shift+R) so the browser loads the new script.

### If the extension init container **is** installed

The init container runs on every pod start and populates `/tmp/extensions/` from the release tarball. If you copy your file to `/tmp/extensions/` as well, the server will load both the release extension and your copy (two copies of the extension). The init container also writes files under a path owned by another user, so you cannot overwrite them from the running pod.

To test **only** your local build:

1. **Remove the init container** from the `argocd-server` deployment:

   ```bash
   kubectl patch deployment argocd-server -n argocd --type='json' -p='[{"op": "remove", "path": "/spec/template/spec/initContainers"}]'
   ```

2. **Wait for the rollout** so a new pod starts with an empty `/tmp/extensions/`:

   ```bash
   kubectl rollout status deployment/argocd-server -n argocd --timeout=120s
   ```

3. **Copy your build** into the pod:

   ```bash
   POD=$(kubectl get pods -n argocd -l app.kubernetes.io/name=argocd-server -o jsonpath='{.items[0].metadata.name}')
   kubectl cp ui/extension/dist/extension-promoter.js argocd/$POD:/tmp/extensions/extension-gitops-promoter.js -n argocd -c argocd-server
   ```

4. Hard-refresh the Argo CD UI to load the new extension.

When you are done testing, re-apply your extension patch (e.g. the one from the [Argo CD Integrations index](index.md#ui-extension)) and restart the deployment to restore the release extension.
