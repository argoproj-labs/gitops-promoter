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
`PullRequest`, and `CommitStatus` children, the commit-status manager kinds
(`ArgoCDCommitStatus`, `DependentsSuccessfulCommitStatus`, `GitCommitStatus`,
`ScheduledCommitStatus`, `TimedCommitStatus`, `ValidatedCommitStatus`,
`WebRequestCommitStatus`), the `CommitValidation`s of its environments, and the
git config (`GitRepository` plus its `ScmProvider` or `ClusterScmProvider`).

> [!WARNING]
> *Secrets are never included*
>
> The bundle resolves the SCM provider but **never** reads or includes the
> credentials `Secret` it references.

The dashboard process watches `PromotionStrategyDetails` and forwards each bundle to
the browser over Server-Sent Events (SSE). SSE does not flow through the
kube-aggregator proxy.

## What gets deployed

`config/apiserver` ships a self-contained kustomize base (under
`config/apiserver/base`) plus cert overlays:

| Resource | Purpose |
| --- | --- |
| `Deployment promoter-apiserver` | runs `gitops-promoter apiserver --secure-port=6443 ...` |
| `Service promoter-apiserver` | `443 -> 6443` |
| `APIService v1alpha1.view.promoter.argoproj.io` | registers the group with the kube-aggregator |
| `ServiceAccount promoter-apiserver` + RBAC | read all promoter CRDs and the `ControllerConfiguration`; `system:auth-delegator`; `extension-apiserver-authentication-reader` in `kube-system` |

The base is intentionally **not** folded into `config/default`: the default overlay's
`namespace: promoter-system` transformer would relocate the
`extension-apiserver-authentication-reader` RoleBinding out of `kube-system`, which breaks
the apiserver's delegated authentication. (A RoleBinding resolves its `roleRef` in its own
namespace, and that Role — granting read on the `extension-apiserver-authentication`
ConfigMap — only exists in `kube-system`.) The overlay's `namePrefix` is harmless to the
`APIService` name itself, since kustomize leaves `APIService`/`CRD` names alone. Install the
controller first (`config/default`), then apply one of the cert overlays below.

For installs without a checkout of this repo, each release also publishes flattened,
image-pinned **combined** bundles that contain the controller *and* this apiserver in one
file:

| Bundle | Serving cert | Built from |
|---|---|---|
| `install-with-dashboard.yaml` (default) | the controller-manager's built-in cert-controller issues, rotates and injects it | `config/apiserver/release-combined-builtin` |
| `install-with-dashboard-cert-manager.yaml` | cert-manager issues and rotates it | `config/apiserver/release-combined-cert-manager` |
| `install-with-dashboard-byo-cert.yaml` | you supply the Secret + `caBundle` | `config/apiserver/release-combined-byo-cert` |

The cert-manager and bring-your-own bundles each have a `-namespaced` variant. There is no
namespaced built-in cert bundle: injecting `caBundle` requires the `promoter-apiserver-cert-rotation`
`ClusterRole` on the controller, and the namespaced install does not grant it. Every published
bundle includes the dashboard API; there is no controller-only bundle. See the [Getting Started](../getting-started.md#installation) guide. Each overlay
unions `config/default` (or `config/namespaced`) with a cert overlay (no extra namespace transformer, so the
`kube-system` RoleBinding is preserved).

## Serving certs

The apiserver needs a TLS serving cert, and the `APIService` needs the matching CA
in its `caBundle` (or `insecureSkipTLSVerify`). Three interchangeable paths are
supported; the built-in cert-controller is the default used by `install-with-dashboard.yaml`.

### Default - built-in cert-controller (no cert-manager)

This is what `install-with-dashboard.yaml` uses. It has two halves:

- `config/apiserver/certs-builtin` ships the (empty) `promoter-apiserver-serving-cert`
  Secret. The apiserver waits for the mounted cert files before it starts.
- The `config/apiserver/certs-builtin-controller` component runs the **controller-manager** with
  `--apiserver-cert-rotation` and grants it the extra RBAC. `release-combined-builtin` includes
  both. The namespaced install does not.

With `--apiserver-cert-rotation`, the controller-manager runs
[open-policy-agent/cert-controller](https://github.com/open-policy-agent/cert-controller) on its
elected leader. It:

1. generates a self-signed CA and a serving cert with SANs
   `promoter-apiserver.promoter-system[.svc[.cluster.local]]` and writes them into the
   `promoter-apiserver-serving-cert` Secret;
2. injects the CA into `APIService.spec.caBundle`;
3. keeps rotating the CA and serving cert before they expire.

The apiserver mounts the Secret at `/serving-certs`. Whenever `--tls-cert-file` and
`--tls-private-key-file` are set, it waits for kubelet to project those files before it
starts serving, and returns immediately when they are already there. It reloads the cert
files when they change, so rotation needs no restart.

RBAC:

- **Apiserver:** no Secret access at all; it only mounts the Secret.
- **Controller-manager:** it already reads Secrets in its namespace. The component adds
  `update` on the `promoter-apiserver-serving-cert` Secret as a `Role` in the install namespace,
  plus the `promoter-apiserver-cert-rotation` `ClusterRole`: `get`, `list`, and `watch` on
  APIServices and `update` on `v1alpha1.view.promoter.argoproj.io`. `APIService` is cluster-scoped,
  so that update cannot be granted with a namespace `Role`. This `ClusterRole` ships only in the
  cluster-wide `install-with-dashboard.yaml` bundle. Namespaced installs use cert-manager or a
  cert you supply, and do not include it.

Related controller-manager flags (defaults match the shipped manifests):

| Flag | Default |
|---|---|
| `--apiserver-cert-rotation` | `false` (set to `true` by the component) |
| `--apiserver-cert-rotation-secret-name` | `promoter-apiserver-serving-cert` |
| `--apiserver-cert-rotation-service-name` | `promoter-apiserver` |
| `--apiserver-cert-rotation-apiservice-name` | `v1alpha1.view.promoter.argoproj.io` |

The Secret and the Service are assumed to be in the controller-manager's namespace.

If you apply `config/apiserver/certs-builtin` on its own, also run the
controller-manager with `--apiserver-cert-rotation` and apply
`config/apiserver/certs-builtin-controller/rbac.yaml`.

> [!NOTE]
> GitOps tools such as Argo CD need no `ignoreDifferences` for this. The shipped manifests
> never set the `promoter-apiserver-serving-cert` Secret's `.data` or the `APIService`'s
> `.spec.caBundle`; the controller-manager writes them at runtime under its own field manager,
> `promoter-apiserver-cert-rotator`. Re-applying the manifests (client-side or server-side
> apply) leaves them untouched. Avoid `Replace=true` syncs on these two objects, and don't add
> those fields to your own copy of the manifests: either would overwrite the generated values
> until the rotator writes them again.

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

- **Built-in cert-controller (cluster-wide default):** rotation is automatic; the controller-manager
  regenerates the serving cert (and the CA when needed) before expiry, updates the Secret and
  re-injects the `caBundle`, and the apiserver reloads the new cert without a restart. Namespaced
  installs do not use this path.
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
