# GitOps Promoter Argo CD Extension

## App view icon

The extension injects the GitOps Promoter logo as the app view tab icon via a `<style>` tag when it loads. No extra configuration is needed.

## Prerequisites

- Node.js
- npm
- Argo CD v3.3 (earlier versions will work but may be buggy)

## Build the Extension

### Option 1: Build as part of the main project (Recommended)

```bash
make build-extension
```

### Option 2: Build standalone

```bash
cd ui/extension
npm install
npm run build
```

The built extension file will be generated as `dist/extension-promoter.js`.

## Using the Extension in ArgoCD

Extensions should be delivered as a javascript file in the argocd-server Pods placed in the `/tmp/extensions` directory and starts with `extension` prefix.

### Quick Deploy Example:

```bash
# Build the extension (from root directory)
make build-extension

# Copy to ArgoCD server pod
kubectl cp ui/extension/dist/extension-promoter.js argocd-server-xxx:/tmp/extensions/extension-gitops-promoter.js -n argocd

# Restart ArgoCD server and refresh ArgoCD
kubectl rollout restart deployment/argocd-server -n argocd
```

### Installing from Release Asset:

```bash
# Download the extension
wget https://github.com/argoproj-labs/gitops-promoter/releases/latest/download/gitops-promoter-argocd-extension.tar.gz

# Extract the extension
tar -xzf gitops-promoter-argocd-extension.tar.gz

# Copy to ArgoCD server pod
kubectl cp dist/extension-promoter.js argocd-server-xxx:/tmp/extensions/extension-gitops-promoter.js -n argocd

# Restart ArgoCD server
kubectl rollout restart deployment/argocd-server -n argocd
```

For detailed deployment instructions, see the [ArgoCD Extension Documentation](https://argo-cd.readthedocs.io/en/stable/developer-guide/extensions/ui-extensions/).

## Development

For local development, you can run:

```bash
cd ui/extension
npm run dev
```

**Note**: The extension is NOT embedded in the main Docker container.

### Mock data

Append `?mock=true` to the Argo CD application URL to render a stable, built-in set of `PromotionStrategy` fixtures instead of fetching data from the Argo CD API. This is useful for building the extension against specific states (in-flight promotions, history, PR states) without live resources, and the fixtures are also importable from `@shared/fixtures/mockData` for unit tests.

Mock mode is only compiled into development builds. Use `npm run dev` or `npm run dev-local` (which pass `--mode development`); the production `npm run build` / `make build-extension` gate it out via `process.env.NODE_ENV`, so it is dead-code-eliminated and never ships in the released bundle.
