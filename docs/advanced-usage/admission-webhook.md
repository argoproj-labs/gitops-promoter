# Admission Webhook

The controller serves a validating admission webhook for
[`KubernetesResourceCommitStatus`](../gating-promotions/built-in-gates/kubernetes-resource-commit-status.md). It
checks a gate's template and expressions against the created kind's OpenAPI schema when the gate is created or
updated, so a mistake is rejected by `kubectl apply` (or shown by Argo CD as a sync error) instead of surfacing
later as `Ready=False` on the gate.

Nothing is evaluated at admission time, so the webhook needs no `PromotionStrategy` and makes no changes. The
controller runs the same checks on every reconcile, so a gate stored while the webhook was off reports the same
errors in its status and creates nothing.

## What is checked

- `spec.template` must carry a literal `apiVersion` and `kind`, the kind must be one of the
  [configured resources](../gating-promotions/built-in-gates/kubernetes-resource-commit-status.md#controller-configuration),
  and the cluster must serve it.
- Every `${ }` expression must compile against its variables. A misspelled variable or a misspelled field of a typed
  variable (`Environment.propsed`) is an error.
- A standalone expression, or a literal, must have the type of the field it populates: `parallelism: ${ Sha }`
  and `parallelism: "2"` are both rejected for a `batch/v1` Job. Text with embedded expressions is only allowed in
  string fields.
- Literals must satisfy the rest of their field's schema: enum, format, minimum and maximum, length, pattern, and
  item count.
- Template fields must exist in the kind's schema. The API server would silently drop `spec.paralellism`; the
  webhook rejects it and suggests `parallelism`. Objects that preserve unknown fields, embedded resources, and maps
  accept any key.
- Objects written out in the template must hold their required fields, such as a container's `name`. Fields inside
  an expression's result and under `metadata` are not checked, and for CRD kinds a field with a schema default is
  not required.
- For CRD kinds, the CRD's CEL rules are evaluated over the literal values, except rules that could read an
  expression's value.
- `spec.phase.expression` must return a boolean or an object, and `spec.description` / `spec.url` must compile.

Template fields the controller ignores (`metadata.name`, `metadata.namespace`, `status`, metadata other than
labels and annotations) produce admission warnings, which `kubectl` prints.

A rejection names the field:

```text
The KubernetesResourceCommitStatus "smoke-test" is invalid:
* spec.template.spec.parallelism: Invalid value: "${ Sha }": expression "Sha" returns string but the field expects int
* spec.template.spec.template.spec.containers[0].name: Required value
```

An update that leaves `spec` unchanged (labels, finalizers, ownership) is accepted without consulting the cluster.
Deletes are never rejected.

## Installation

The webhook is part of every install bundle. The controller generates its own serving certificate, rotates it, and
injects the CA into the webhook configuration (using
[cert-controller](https://github.com/open-policy-agent/cert-controller), like the dashboard apiserver's
[built-in certificates](dashboard-apiserver.md)), so it needs neither cert-manager nor a manual certificate.

| Object | Purpose |
|---|---|
| `ValidatingWebhookConfiguration promoter-validating-webhook-configuration` | Tells the API server to call the webhook for `KubernetesResourceCommitStatus` create and update. The controller keeps its `caBundle` current. |
| `Service promoter-webhook-service` | Routes the API server to the controller's webhook port, 9443 |
| `Secret promoter-webhook-server-cert` | Shipped empty. The controller fills it with the CA and serving certificate and rotates them; it is mounted at `/tmp/k8s-webhook-server/serving-certs` (`--webhook-cert-dir`) |
| `Role promoter-webhook-cert-role` | `get` and `update` on that one Secret |
| `ClusterRole promoter-webhook-configuration-role` | `get`, `list`, and `watch` on ValidatingWebhookConfigurations, and `update` on `promoter-validating-webhook-configuration` only |

The ClusterRole is separate from the manager role because webhook configurations are cluster-scoped, so it stays a
ClusterRole in the `*-namespaced` bundles too.

> [!NOTE]
> GitOps tools such as Argo CD need no `ignoreDifferences` for this. The shipped manifests never set the
> `promoter-webhook-server-cert` Secret's `.data` or the webhook configuration's `caBundle`; the controller writes
> them at runtime under its own field manager, `promoter-webhook-cert-rotator`. Re-applying the manifests
> (client-side or server-side apply) leaves them untouched. Avoid `Replace=true` syncs on these two objects, and
> don't add those fields to your own copy of the manifests: either would overwrite the generated values until the
> rotator writes them again.

On first install, the webhook starts once the kubelet has synced the generated certificate into the controller pod,
which can take up to a minute. Until then the controller's readiness check fails, and creating or updating a gate
fails with a message naming the webhook.

The controller flags that configure it:

| Flag | Default |
|---|---|
| `--admission-webhook` | `true`. Set it to `false` only when the ValidatingWebhookConfiguration is not installed, such as when running the controller from a development machine (`make run` does). |
| `--webhook-cert-dir` | `/tmp/k8s-webhook-server/serving-certs` |
| `--webhook-cert-secret` | `promoter-webhook-server-cert` |
| `--webhook-service` | `promoter-webhook-service` |
| `--webhook-configuration` | `promoter-validating-webhook-configuration` |

### Namespaced installs

The `*-namespaced` bundles add a `namespaceSelector` so the webhook only sees gates in `promoter-system`
(`config/webhook/namespaced`). That controller reconciles only its own namespace; gates elsewhere belong to other
installs and are validated by theirs.

### Several controllers in one cluster

`ValidatingWebhookConfiguration` is cluster-scoped, so each install needs its own name for it, its Service, and its
Secret (pass the matching `--webhook-*` flags), just as each needs its own namespace and `namePrefix`. Each webhook
validates only against its controller's configured kinds, so an install that does not reconcile a gate should not
validate it either: give every install's webhook a `namespaceSelector` (or an `objectSelector` on the instance-id
label) that matches the gates it owns.

## Failure behavior

The webhook uses `failurePolicy: Fail` with a ten second timeout. If no controller replica is ready, creating or
updating a `KubernetesResourceCommitStatus` fails with a message naming the webhook; every other kind, including the
gates' own `CommitStatus` objects, is unaffected. Status updates bypass the webhook.

Schema resolution goes through API discovery (`/openapi/v3`, readable through `system:discovery`). A kind's schema is
cached for a minute. When a gate fails validation against a cached schema that is at least ten seconds old, the
schema is fetched again and the gate re-checked, so a gate that uses a field its CRD just gained is not rejected
until the cache expires. A failed lookup is retried after five seconds, so a gate applied right after its CRD is
accepted on retry (Argo CD and `kubectl apply` of a directory both retry).

To turn the webhook off, delete the `ValidatingWebhookConfiguration` and pass `--admission-webhook=false`. The
controller keeps validating gates in their status.
