# GitOps Promoter ArgoCD Extension

## Prerequisites
- Node.js
- npm

## Build the Extension
```bash
cd ui/extension
npm install
npm run build
```

The built extension file will be generated as `dist/extension.js`.

## Using the Extension in ArgoCD

Extensions should be delivered as a javascript file in the argocd-server Pods placed in the `/tmp/extensions` directory and starts with `extension` prefix (matches to `^extension(.*)\.js$` regex).

### Deployment Approaches:

1. **Direct File Copy**: Copy the built extension file directly to the ArgoCD server pod using `kubectl cp`
2. **ConfigMap**: Store the extension in a ConfigMap and mount it into the ArgoCD server pods

For detailed deployment instructions, see the [ArgoCD Extension Documentation](https://argo-cd.readthedocs.io/en/stable/developer-guide/extensions/ui-extensions/).

### Quick Deploy Example:

```bash
# Build the extension
cd ui/extension
npm run build

# Copy to ArgoCD server pod
kubectl -n argocd exec -it argocd-server-xxx -- mkdir -p /tmp/extensions/gitops-promoter
kubectl cp dist/extension.js argocd-server-xxx:/tmp/extensions/gitops-promoter/extension-gitops-promoter.js -n argocd

# Restart ArgoCD server
kubectl rollout restart deployment/argocd-server -n argocd
```

## Development
For local development, you can run:
```bash
npm run dev
```