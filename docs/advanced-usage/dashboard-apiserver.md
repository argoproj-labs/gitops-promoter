# Dashboard Aggregation API

The dashboard is backed by a Kubernetes **aggregation layer**: an extension
apiserver that serves a single, read-only, server-computed resource that bundles a
`PromotionStrategy` together with everything related to it.

- **Group / Version / Kind:** `view.promoter.argoproj.io/v1alpha1`, `PromotionStrategyDetails`
- **Scope:** namespaced; the name of a `PromotionStrategyDetails` always equals the
  name of the `PromotionStrategy` it describes (1:1 mapping).
- **Backing store:** none. The resource is *virtual* - it is computed on demand from
  a read-only controller-runtime cache and is never persisted to etcd.

Each bundle contains the `PromotionStrategy`, its `ChangeTransferPolicy`,
`PullRequest`, and `CommitStatus` children, the four commit-status manager kinds
(`ArgoCDCommitStatus`, `GitCommitStatus`, `TimedCommitStatus`,
`WebRequestCommitStatus`), and the git config (`GitRepository` plus its
`ScmProvider` or `ClusterScmProvider`).

> [!WARNING]
> *Secrets are never included*
>
> The bundle resolves the SCM provider but **never** reads or includes the
> credentials `Secret` it references.

The dashboard process watches `PromotionStrategyDetails` and forwards each bundle to
the browser over Server-Sent Events (SSE). SSE does not flow through the
kube-aggregator proxy.

## In-cluster dashboard deployment

In addition to running the dashboard locally via CLI, you can deploy it as an in-cluster
Deployment + Service so that teams can access a shared, always-available dashboard through
existing ingress infrastructure:

```bash
kubectl apply -k config/dashboard
```

This deploys:

| Resource | Purpose |
| --- | --- |
| `ServiceAccount promoter-dashboard` | dedicated identity for the dashboard pod |
| `ClusterRole / ClusterRoleBinding promoter-dashboard` | read-only access to `PromotionStrategyDetails` and `PromotionStrategies` |
| `Deployment promoter-dashboard` | runs `gitops-promoter dashboard --port=8080` |
| `Service promoter-dashboard` | `80 -> 8080` |

Add your own Ingress, Gateway API HTTPRoute, or other routing and authentication
resources on top of the Service. The dashboard requires the APIService (below) to be
deployed and healthy.

## What gets deployed (APIService)

`config/apiserver` ships a self-contained kustomize base (under
`config/apiserver/base`) plus cert overlays:

| Resource | Purpose |
| --- | --- |
| `Deployment promoter-apiserver` | runs `gitops-promoter apiserver --secure-port=6443 ...` |
| `Service promoter-apiserver` | `443 -> 6443` |
| `APIService v1alpha1.view.promoter.argoproj.io` | registers the group with the kube-aggregator |
| `ServiceAccount promoter-apiserver` + RBAC | read all promoter CRDs; `system:auth-delegator`; `extension-apiserver-authentication-reader` in `kube-system` |

The base is intentionally **not** folded into `config/default`: the default overlay's
`namespace: promoter-system` transformer would relocate the
`extension-apiserver-authentication-reader` RoleBinding out of `kube-system`, which breaks
the apiserver's delegated authentication. (A RoleBinding resolves its `roleRef` in its own
namespace, and that Role — granting read on the `extension-apiserver-authentication`
ConfigMap — only exists in `kube-system`.) The overlay's `namePrefix` is harmless to the
`APIService` name itself, since kustomize leaves `APIService`/`CRD` names alone. Install the
controller first (`config/default`), then apply one of the cert overlays below.

For installs without a checkout of this repo, each release also publishes two flattened,
image-pinned **combined** bundles that contain the controller *and* this apiserver in one
file — `install-with-dashboard-cert-manager.yaml` (cert-manager issues and rotates the
serving cert) and `install-with-dashboard-byo-cert.yaml` (no cert-manager; you supply the
serving cert + `caBundle`). These are the preferred install — apply one of them instead of the
controller-only `install-without-ui.yaml`. See the
[Getting Started](../getting-started.md#install-the-dashboard-api) guide. They are
built from the `config/apiserver/release-combined-cert-manager` and
`config/apiserver/release-combined-byo-cert` overlays respectively, which union
`config/default` with a cert overlay (no extra namespace transformer, so the `kube-system`
RoleBinding is preserved).

## Serving certs

The apiserver needs a TLS serving cert, and the `APIService` needs the matching CA
in its `caBundle` (or `insecureSkipTLSVerify`). Three interchangeable paths are
supported; none is enabled by default.

### Path A - manual / scripted (no cert-manager)

```bash
kubectl apply -k config/apiserver/certs-manual
make apiserver-certs          # creates the serving-cert Secret and patches the APIService caBundle
kubectl -n promoter-system rollout restart deploy/promoter-apiserver
```

`hack/gen-apiserver-certs.sh` generates a self-signed CA and a serving cert with
SANs `promoter-apiserver.promoter-system.svc[.cluster.local]`, creates the
`promoter-apiserver-serving-cert` Secret, and base64-patches the CA into the
`APIService.spec.caBundle`.

For air-gapped environments without the script, the equivalent manual steps are:

```bash
# 1. CA
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 3650 \
  -subj "/CN=promoter-apiserver-ca"
# 2. Serving cert (SAN must match the in-cluster Service DNS name)
openssl req -newkey rsa:2048 -nodes -keyout tls.key -out tls.csr \
  -subj "/CN=promoter-apiserver.promoter-system.svc"
printf 'subjectAltName=DNS:promoter-apiserver.promoter-system.svc,DNS:promoter-apiserver.promoter-system.svc.cluster.local\nextendedKeyUsage=serverAuth\n' > san.ext
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out tls.crt -days 365 -extfile san.ext
# 3. Secret
kubectl -n promoter-system create secret tls promoter-apiserver-serving-cert --cert tls.crt --key tls.key
# 4. caBundle
kubectl patch apiservice v1alpha1.view.promoter.argoproj.io --type merge \
  -p "{\"spec\":{\"caBundle\":\"$(base64 < ca.crt | tr -d '\n')\"}}"
```

### Path B - cert-manager (opt-in)

```bash
kubectl apply -k config/apiserver/certs-cert-manager
```

A self-signed `Issuer` + `Certificate` produce the `promoter-apiserver-serving-cert`
Secret, and the `cert-manager.io/inject-ca-from` annotation makes cert-manager's
ca-injector keep `APIService.spec.caBundle` in sync automatically.

### Dev fallback - skip TLS verification

```bash
kubectl apply -k config/apiserver/dev-insecure
make apiserver-certs   # the apiserver still needs a serving-cert Secret to start
```

This sets `insecureSkipTLSVerify: true` on the `APIService`. **Not production-safe.**

## Cert rotation

- **cert-manager (Path B):** rotation is automatic; ca-injector re-syncs the
  `caBundle` when the `Certificate` is renewed.
- **Manual (Path A):** re-run `make apiserver-certs` and restart the apiserver
  Deployment. The script regenerates the CA + serving cert and re-patches the
  `caBundle`.

## Verifying the install

```bash
kubectl api-resources | grep promotionstrategydetails
kubectl get apiservice v1alpha1.view.promoter.argoproj.io   # should report Available=True
kubectl get promotionstrategydetails -A
kubectl get promotionstrategydetails <name> -n <ns> -o yaml       # bundle, no secrets
kubectl get promotionstrategydetails -A --watch                   # mutate a child; observe MODIFIED
```

If the `APIService` reports `Available=False` with an `x509`/TLS error, the
`caBundle` does not match the apiserver's serving cert - re-run the cert step for
your chosen path.
