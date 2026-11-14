# Upgrading

This page documents breaking changes and migration steps between releases.

## 0.50 — ControllerConfiguration needs `validatedCommitStatus` and `commitValidation`

> [!IMPORTANT]
> If you maintain your own `ControllerConfiguration`, add the two blocks below **before** upgrading. They are required
> whether or not you use the new kinds, as the block of every other controller is. Without them the controller
> rejects the configuration at startup.

0.50 adds two kinds, [ValidatedCommitStatus](crd-specs.md#validatedcommitstatus) and
[CommitValidation](crd-specs.md#commitvalidation), and a controller for each. Each controller reads its work queue
settings from the ControllerConfiguration:

```yaml
spec:
  validatedCommitStatus:
    workQueue:
      maxConcurrentReconciles: 10
      requeueDuration: "5m"
      rateLimiter:
        maxOf:
          - bucket:
              qps: 10
              bucket: 100
          - fastSlow:
              fastDelay: "1s"
              slowDelay: "5m"
              maxFastAttempts: 3
  commitValidation:
    workQueue:
      maxConcurrentReconciles: 10
      requeueDuration: "5m"
      rateLimiter:
        maxOf:
          - bucket:
              qps: 10
              bucket: 100
          - fastSlow:
              fastDelay: "1s"
              slowDelay: "5m"
              maxFastAttempts: 3
```

An install that uses the shipped ControllerConfiguration gets both with the upgrade.

Nothing else changes for existing PromotionStrategies. `DependentsSuccessfulCommitStatus` stays the default ordering
gate. `ValidatedCommitStatus` is an alternative you opt in to per PromotionStrategy: see
[Validated Commit Status Controller](gating-promotions/built-in-gates/validated-commit-status.md), and its
[Setup and migration](gating-promotions/built-in-gates/validated-commit-status.md#setup-and-migration) section before
switching a PromotionStrategy that is already promoting.

Also in this release:

- The controller's ClusterRole gains `validatedcommitstatuses` and `commitvalidations`. If you manage RBAC yourself,
  copy the rules from the release manifests.
- The webhook receiver now also reconciles a `ValidatedCommitStatus` when its dry branch or one of its environment
  tags is pushed. If your SCM webhook only sends branch pushes, add tag pushes to pick up tags moved by hand without
  waiting for the next poll.
- The webhook receiver read every Bitbucket Cloud push as a branch push. A tag push is now read as a tag push.

## 0.49 — Controller-only install bundles are removed

> [!IMPORTANT]
> `install-without-ui.yaml` and `install-without-ui-namespaced.yaml` are no longer published.
> Every release bundle includes the dashboard aggregation API. Update GitOps manifests, CI, and
> anything else that downloads those two assets before upgrading.

The cluster-wide default is `install-with-dashboard.yaml`. The controller-manager issues and rotates
the dashboard apiserver's serving certificate and writes the `APIService` `caBundle`. That bundle
does not need cert-manager.

| Previous bundle | Replacement |
| --- | --- |
| `install-without-ui.yaml` | `install-with-dashboard.yaml` (built-in certs), `install-with-dashboard-cert-manager.yaml`, or `install-with-dashboard-byo-cert.yaml` |
| `install-without-ui-namespaced.yaml` | `install-with-dashboard-cert-manager-namespaced.yaml` or `install-with-dashboard-byo-cert-namespaced.yaml` |

A namespaced install has no built-in cert bundle. Writing `APIService.spec.caBundle` requires the
`promoter-apiserver-cert-rotation` ClusterRole, and the namespaced install does not grant the
controller that role. Use the cert-manager bundle, or the bring-your-own-cert bundle and supply the
serving Secret and `caBundle` yourself.

Applying `install-with-dashboard.yaml` onto a controller-only install adds:

- the `promoter-apiserver` Deployment, Service, and ServiceAccount
- `APIService` `v1alpha1.view.promoter.argoproj.io`
- a `RoleBinding` for `extension-apiserver-authentication-reader` in `kube-system`
- the empty `promoter-apiserver-serving-cert` Secret, which the controller-manager fills when started with `--apiserver-cert-rotation`
- rotation RBAC: `update` on that Secret, plus `get`, `list`, and `watch` on APIServices and `update` on the dashboard APIService

Installs that already apply a cert-manager or bring-your-own-cert bundle, including the `-namespaced`
variants, keep those bundles. Those manifests are unchanged from 0.48.

The Helm chart is generated from `install-with-dashboard.yaml`, so a chart upgrade includes the
dashboard apiserver, the `APIService`, and the `kube-system` RoleBinding.

The shipped manifests leave `promoter-apiserver-serving-cert` `.data` and `APIService.spec.caBundle`
empty. The controller-manager owns both under the field manager `promoter-apiserver-cert-rotator`,
so a client-side or server-side apply leaves the generated values in place. A sync with
`Replace=true` overwrites them until the next rotation.

See [Installation](getting-started.md#installation) and
[Serving certs](advanced-usage/dashboard-apiserver.md#serving-certs).

## 0.49 — Rate limiters must name exactly one strategy

Every `spec.<controller>.workQueue.rateLimiter` in the ControllerConfiguration must now set exactly one of
`fastSlow`, `exponentialFailure`, `bucket` or `maxOf`, and every `maxOf` entry must set exactly one of the first three.
The CRD previously allowed an empty `rateLimiter: {}` or an empty `maxOf` entry, which the controller then rejected at
startup with `no valid rate limiter configuration found`. The API server now rejects such a configuration on write, so
the mistake surfaces at `kubectl apply` instead of as a crash loop.

The shipped ControllerConfiguration sets a strategy for every controller, so an install that uses it is unaffected. A
custom configuration with an empty rate limiter was already failing to start and needs a strategy added; there is no
in-code default, by design.


## 0.46 — WebRequestCommitStatus authentication Secrets must be labeled

> [!IMPORTANT]
> Every WebRequestCommitStatus that uses `httpRequest.authentication.basic`, `.bearer`, `.oauth2`, or `.tls` stops
> making requests after upgrading until the referenced Secret carries the label
> `promoter.argoproj.io/webrequestcommitstatus-access: allowed`. The gate reports `Ready=False` with a message naming the label.
> `authentication.scm` is unaffected.

The controller sends those credentials to the request URL the WebRequestCommitStatus author templates (and, for
`oauth2`, to `tokenURL`). Previously any Secret in the namespace could be referenced, so a user who could create a
WebRequestCommitStatus could read Secrets they had no RBAC for. The label is an opt-in that only a Secret writer can grant.

Migration: label each Secret referenced by a WebRequestCommitStatus `secretRef`:

```bash
kubectl -n <namespace> label secret <name> promoter.argoproj.io/webrequestcommitstatus-access=allowed
```

See [Opting Secrets in with a label](gating-promotions/built-in-gates/web-request-commit-status/index.md#opting-secrets-in-with-a-label).

## 0.41 — Promotion history moves to ChangeTransferPolicyHistory

Starting in 0.41.0, promotion history no longer lives on `ChangeTransferPolicy.status.history` or
`PromotionStrategy.status.environments[].history`; both fields are removed from the CRD schemas. History is now owned by
a dedicated [ChangeTransferPolicyHistory](crd-specs.md#changetransferpolicyhistory) resource, created per ChangeTransferPolicy by the
ChangeTransferPolicy controller and reconciled by its own controller. The dashboard and Argo CD extension read history from
the `changeTransferPolicyHistories` field of the `PromotionStrategyDetails` view bundle.

Migration notes:

- Apply the new CRDs and RBAC (`dist/install*.yaml` includes the `changetransferpolicyhistories` CRD, controller
  permissions, and apiserver read access). Updating the ChangeTransferPolicy and PromotionStrategy CRDs prunes the
  stored `history` values automatically.
- No data is lost: history was always reconstructed from Git (commit trailers and the
  `refs/notes/promoter.history` notes ref), and the new controller rebuilds those entries from the same
  sources.
- During a rolling upgrade, an old controller writing status containing `history` against the new CRD schema has the
  unknown field silently dropped by structural pruning; this is harmless.
- Any external consumers reading `status.history` from ChangeTransferPolicy or PromotionStrategy must switch to the
  ChangeTransferPolicyHistory resource.

## 0.39 — Promotion order on PromotionStrategy

> [!IMPORTANT]
> Your PromotionStrategies will stop reconciling after upgrading to 0.39.0+ until you apply manifest changes. This is a breaking change requiring downtime to address.
>
> **From 0.37 (or earlier):** PromotionStrategy used to auto-inject a hard-coded `promoter-previous-environment` check for linear order. That magic is gone. For each PromotionStrategy, create a `DependentsSuccessfulCommitStatus` (`spec.key` plus `spec.promotionStrategyRef` naming the PS) and add required `spec.orderCommitStatusRef` on the PS pointing at that gate. Do not put the ordering key in `proposedCommitStatuses` — the PS controller injects it. Linear pipelines omit `dependsOn`; custom DAGs set `dependsOn` on `PromotionStrategy.spec.environments[]`.
>
> **From 0.38:** You already have a `DependentsSuccessfulCommitStatus`. Copy any `DependentsSuccessfulCommitStatus.spec.environments[].dependsOn` onto the matching `PromotionStrategy.spec.environments[]`, delete `spec.environments` from the DSCS, add required `spec.orderCommitStatusRef` on the PS, and remove the ordering key (usually `dependents-successful`) from `proposedCommitStatuses`. Keep DSCS `spec.key` and `spec.promotionStrategyRef`.
>

This release moves the promotion dependency graph from `DependentsSuccessfulCommitStatus` onto
`PromotionStrategy` and wires the ordering gate through required `orderCommitStatusRef`.

### What changed

| 0.38 | 0.39+ |
| ---- | ----- |
| Custom graphs used `DependentsSuccessfulCommitStatus.spec.environments` / `dependsOn` | Set `dependsOn` on `PromotionStrategy.spec.environments[]` |
| Users declared `dependents-successful` in `proposedCommitStatuses` | Required `spec.orderCommitStatusRef`; the PS controller injects `spec.key` onto every CTP |
| PS discovered DSCS by reverse index | PS `Get`s the named DSCS; `promotionStrategyRef` must agree |

Linear pipelines still omit `dependsOn`; the DSCS controller infers dev → staging → prod from list order.

### Migrate from 0.38

For each `PromotionStrategy` / `DependentsSuccessfulCommitStatus` pair:

1. Copy any `DependentsSuccessfulCommitStatus.spec.environments[].dependsOn` onto the matching
   `PromotionStrategy.spec.environments[]`.
2. Delete `spec.environments` from the DSCS.
3. Add required `spec.orderCommitStatusRef` on the PS (`group`, `kind`, `name` of that DSCS).
4. Remove the ordering key (usually `dependents-successful`) from PS `proposedCommitStatuses`.
5. Keep DSCS `spec.key` and `spec.promotionStrategyRef` (must still name the PS).

**Before (0.38 DAG example):**

```yaml
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  proposedCommitStatuses:
    - key: dependents-successful
  environments:
    - branch: environment/dev
    - branch: environment/e2e
    - branch: environment/prod
---
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo-dag
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: demo-dag
  environments:
    - branch: environment/dev
    - branch: environment/e2e
      dependsOn:
        - environment/dev
    - branch: environment/prod
      dependsOn:
        - environment/e2e
```

**After (0.39+):**

```yaml
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsSuccessfulCommitStatus
    name: demo-dag
  environments:
    - branch: environment/dev
    - branch: environment/e2e
      dependsOn:
        - environment/dev
    - branch: environment/prod
      dependsOn:
        - environment/e2e
---
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo-dag
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: demo-dag
```

### Further reading

- [Architecture — Embedding vs. Decoupling Promotion Order](architecture.md#embedding-vs-decoupling-promotion-order)
- [Dependents Successful Commit Status](gating-promotions/built-in-gates/dependents-successful-commit-status.md)

## 0.38 — Promotion ordering gate (`DependentsSuccessfulCommitStatus`)

Release **0.38** extracts promotion ordering from the `PromotionStrategy` controller into a dedicated gate CR,
`DependentsSuccessfulCommitStatus`. This enables linear pipelines and custom dependency graphs (fan-out / fan-in) with
the same API. See [Architecture](architecture.md#embedding-vs-decoupling-promotion-order) for the design rationale.

### What changed

| Before (≤ 0.37) | After (0.38+) |
| --------------- | ------------- |
| PromotionStrategy auto-injected a `promoter-previous-environment` proposed commit status for linear ordering | Ordering is **not** injected; you must create a `DependentsSuccessfulCommitStatus` per `PromotionStrategy` |
| Ordering was implicit in `spec.environments` list order only | Linear order is still inferred from `spec.environments` when `DependentsSuccessfulCommitStatus.spec.environments` is omitted; custom graphs use `dependsOn` on the gate CR |
| No separate gate CR for ordering | `DependentsSuccessfulCommitStatus` writes one ordering `CommitStatus` per environment |

### Required migration steps

For **each** `PromotionStrategy`:

1. **Create a `DependentsSuccessfulCommitStatus`** that references the strategy (same namespace). For a standard linear
   pipeline, omit `spec.environments` — the controller infers dev → staging → prod from the PromotionStrategy's
   `spec.environments` order.

2. **Replace the ordering key** in `proposedCommitStatuses`:
   - Remove `promoter-previous-environment` (if present).
   - Add the gate's `spec.key` (commonly `dependents-successful`).

3. **Apply both resources** before or with the controller upgrade. After 0.38, a `PromotionStrategy` with no matching
   `DependentsSuccessfulCommitStatus` fails reconcile (`Ready=False`, `ReconciliationError`) so environments cannot
   promote without explicit ordering.

**Linear example** (before):

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
spec:
  proposedCommitStatuses:
    - key: promoter-previous-environment  # auto-injected by controller ≤ 0.37
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
```

**Linear example** (after):

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
spec:
  proposedCommitStatuses:
    - key: dependents-successful
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: demo
```

For **non-linear** graphs, set `spec.environments` and `dependsOn` on the `DependentsSuccessfulCommitStatus` (not on
`PromotionStrategy`). See
[Dependents Successful Commit Status](gating-promotions/built-in-gates/dependents-successful-commit-status.md#custom-dependency-graph).

### Declaring the gate key on the PromotionStrategy

The ordering `key` must appear in the **effective** `proposedCommitStatuses` for every environment whose
`ChangeTransferPolicy` should enforce ordering — that is, in global `proposedCommitStatuses` and/or in each
environment's `proposedCommitStatuses`, using the same merge rules as other gates.

Most setups declare the key once in global `proposedCommitStatuses` so every environment gates on it. As a safety
check, the PromotionStrategy controller fails reconcile when:

- No `DependentsSuccessfulCommitStatus` targets the strategy, or
- A gate exists but its `key` is missing from the effective proposed selectors for one or more environment branches.

This safety check is intended to be removed in v1.0; see [Roadmap](roadmap.md).

### Orphaned CommitStatuses

After migration, old `CommitStatus` objects labeled `promoter.argoproj.io/commit-status=promoter-previous-environment`
are no longer consumed. The `DependentsSuccessfulCommitStatus` controller creates new statuses for the configured key.

Once a `DependentsSuccessfulCommitStatus` reconciles successfully for a `PromotionStrategy`, the controller
automatically deletes legacy previous-environment `CommitStatus` resources owned by that strategy's
`ChangeTransferPolicy` objects. No manual cleanup is required after promotions are healthy on the new gate.

This automatic cleanup is intended to be removed in v1.0; see [Roadmap](roadmap.md).

### Multi-install deployments

Label each new `DependentsSuccessfulCommitStatus` with the same `promoter.argoproj.io/instance-id` as its
`PromotionStrategy`. Gate CRs propagate `instance-id` to their `CommitStatus` children; `PromotionStrategy` does not
create ordering CommitStatuses directly. See [Multiple Controller Installs](multi-install.md#write-path-label-propagation).

### Further reading

- [Gating Promotions — Environment Ordering](gating-promotions/index.md#environment-ordering)
- [Dependents Successful Commit Status](gating-promotions/built-in-gates/dependents-successful-commit-status.md)
- [Architecture — Embedding vs. Decoupling Promotion Order](architecture.md#embedding-vs-decoupling-promotion-order)
