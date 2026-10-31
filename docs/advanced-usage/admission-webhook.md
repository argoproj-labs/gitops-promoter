# Admission Webhook

The controller can serve a validating admission webhook for
[`KubernetesResourceCommitStatus`](../gating-promotions/built-in-gates/kubernetes-resource-commit-status.md). It
checks a gate's template and expressions against the created kind's OpenAPI schema when the gate is created or
updated, so a mistake is rejected by `kubectl apply` (or shown by Argo CD as a sync error) instead of surfacing
later as `Ready=False` on the gate.

Nothing is evaluated at admission time, so the webhook needs no `PromotionStrategy`, makes no changes, and the
same checks run again in the controller. Without the webhook the gate is still validated; the result just lands
in the gate's status on the first reconcile.

## What is checked

- `spec.template` must carry a literal `apiVersion` and `kind`, the kind must be one of the
  [configured resources](../gating-promotions/built-in-gates/kubernetes-resource-commit-status.md#controller-configuration),
  and the cluster must serve it.
- Every `${ }` expression must compile against its variables. A misspelled variable or a misspelled field of a typed
  variable (`Environment.propsed`) is an error.
- A standalone expression, or a literal, must have the type of the field it populates: `parallelism: ${ Sha }`
  and `parallelism: "2"` are both rejected for a `batch/v1` Job. Text with embedded expressions is only allowed in
  string fields.
- Template fields must exist in the kind's schema. The API server would silently drop `spec.paralellism`; the
  webhook rejects it. Objects that preserve unknown fields, embedded resources, and maps accept any key.
- `spec.phase.expression` must return a boolean or an object, and `spec.description` / `spec.url` must compile.

Template fields the controller ignores (`metadata.name`, `metadata.namespace`, `status`, metadata other than
labels and annotations) produce admission warnings, which `kubectl` prints.

A rejection names the field and the expression:

```text
The KubernetesResourceCommitStatus "smoke-test" is invalid:
spec.template.spec.parallelism: Invalid value: "${ Sha }": expression "Sha" returns string but the field expects int
```

An update that leaves `spec` unchanged (labels, finalizers, ownership) is accepted without consulting the cluster.
Deletes are never rejected.

## Installing it

The webhook is part of the `install-with-dashboard-*` bundles, which already carry a certificate strategy for the
dashboard aggregation API. The controller-only `install-without-ui*` bundles do not include it, because the
webhook needs a serving certificate and those bundles have no way to provide one.

The pieces are:

| Component | Purpose |
|---|---|
| `--enable-admission-webhooks` on the controller | Starts the webhook server on port 9443 (`--admission-webhook-port`) and registers the validator |
| `Service promoter-webhook-service` | Routes the API server to the controller's webhook port |
| `ValidatingWebhookConfiguration promoter-validating-webhook-configuration` | Tells the API server to call the webhook for `KubernetesResourceCommitStatus` create and update |
| `Secret promoter-webhook-server-cert` | `tls.crt` / `tls.key` mounted at `/tmp/k8s-webhook-server/serving-certs` (`--admission-webhook-cert-dir`) |

`config/webhook/base` holds the Service and the generated `ValidatingWebhookConfiguration` (self-contained, with
the `promoter-` prefix and `promoter-system` namespace, like `config/apiserver/base`). Two cert overlays wrap it,
and the `config/webhook/manager-patch` component turns the webhook on in the controller Deployment.

### cert-manager

`config/webhook/certs-cert-manager` adds a self-signed `Issuer` and a `Certificate` that produce the Secret, and
annotates the `ValidatingWebhookConfiguration` with `cert-manager.io/inject-ca-from` so ca-injector keeps the
`caBundle` current, including on rotation. From a checkout:

```yaml
# kustomization.yaml
resources:
  - github.com/argoproj-labs/gitops-promoter/config/default
  - github.com/argoproj-labs/gitops-promoter/config/webhook/certs-cert-manager
components:
  - github.com/argoproj-labs/gitops-promoter/config/webhook/manager-patch
```

### Bring your own cert

`config/webhook/certs-manual` is the base unchanged; you supply the Secret and the `caBundle`:

```bash
make webhook-certs      # or hack/gen-webhook-certs.sh
kubectl -n promoter-system rollout restart deploy/promoter-controller-manager
```

The script generates a self-signed CA and a serving cert with SANs
`promoter-webhook-service.promoter-system.svc[.cluster.local]`, creates the `promoter-webhook-server-cert`
Secret, and patches the CA into every webhook's `clientConfig.caBundle`. The equivalent manual steps mirror the
[dashboard apiserver ones](dashboard-apiserver.md#path-a-manual-scripted-no-cert-manager) with those names.

### Namespaced installs

The `*-namespaced` bundles add a `namespaceSelector` so the webhook only sees gates in `promoter-system`
(`config/webhook/namespaced`). That controller reconciles only its own namespace; gates elsewhere belong to other
installs and are validated by theirs.

### Several controllers in one cluster

`ValidatingWebhookConfiguration` is cluster-scoped, so each install needs its own name for it, its Service, and its
Secret, just as each needs its own namespace and `namePrefix`. Each webhook validates only against its controller's
configured kinds, so an install that does not reconcile a gate should not validate it either: give every install's
webhook a `namespaceSelector` (or an `objectSelector` on the instance-id label) that matches the gates it owns.

## Failure behavior

The webhook uses `failurePolicy: Fail` with a ten second timeout. If the controller is down or its certificate is
wrong, creating or updating a `KubernetesResourceCommitStatus` fails with a message naming the webhook; every other
kind, including the gates' own `CommitStatus` objects, is unaffected. Status updates bypass the webhook.

Schema resolution goes through API discovery. A kind's schema is cached for a minute, so a CRD change can take up
to a minute to be reflected; a failed lookup is retried after five seconds, so a gate applied right after its CRD
is accepted on retry (Argo CD and `kubectl apply` of a directory both retry).

To turn the webhook off, delete the `ValidatingWebhookConfiguration`. The controller keeps serving the endpoint,
which is harmless, and keeps validating gates in their status.
