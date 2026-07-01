# Multiple Controller Installs

GitOps Promoter can run more than one controller install in the same cluster. Each install reconciles only the Promoter custom resources that belong to it, so teams can share a cluster without one install observing or mutating another's PromotionStrategies, gates, or CommitStatuses.

Partitioning is **fully independent**: gating, orphan cleanup, and fan-out lists only see resources in this install's partition.

## When to use multiple installs

Use separate installs when you need hard isolation between Promoter deployments on one cluster—for example:

- Different teams each run their own Promoter release with separate RBAC and configuration
- Blue/green or canary controller upgrades where old and new installs coexist briefly
- Environments where one cluster hosts multiple logically separate Promoter "tenants"

For namespace-scoped access control among PromotionStrategy users in a **single** install, see [Configuring Multi-Tenancy](advanced-usage/multi-tenancy.md). Instance ID partitioning is orthogonal: it scopes which install reconciles which CRs, not which namespaces users may write to.

## Configuration

Configure `ControllerConfiguration.spec.instanceID` in the controller's install namespace. The field is **optional**; omit it entirely for the default install.

**Multi-install** — set a non-empty value:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: promoter-controller-configuration
  namespace: gitops-promoter
spec:
  instanceID: wave-0
  # … other controller settings unchanged
```

**Default install** — omit `instanceID` (do not set it to `""`):

```yaml
spec:
  promotionStrategy:
    # …
```

| `spec.instanceID` | Behavior |
| ----------------- | -------- |
| **Unset (nil)** | **Default install** — only Promoter CRs **without** `promoter.argoproj.io/instance-id` enter the cache. |
| **Non-empty string** | **Multi-install** — only CRs labeled `promoter.argoproj.io/instance-id=<value>` exactly. |

There is **no match-all mode**. Labeled and unlabeled resources are never reconciled by the same install.

The value must be a valid Kubernetes label value (min length 1, max 63 characters; alphanumeric, `.`, `_`, `-`).

**Changing `instanceID` requires a controller restart.** The value is read once at startup before the manager cache is built.

## How partitioning works

### Read path: informer cache filtering

At startup the controller reads `instanceID` from `ControllerConfiguration` (via a direct API read, not the informer cache) and configures `cache.ByObject` label selectors for every reconciled Promoter CRD:

- `PromotionStrategy`, `ChangeTransferPolicy`, `CommitStatus`, `PullRequest`
- `ScmProvider`, `ClusterScmProvider`, `GitRepository`
- `GitCommitStatus`, `TimedCommitStatus`, `WebRequestCommitStatus`, `ArgoCDCommitStatus`
- `RevertCommit`

`ControllerConfiguration` itself is **not** filtered—the install must always read its own configuration.

When `instanceID` is unset, the cache selector requires the instance-id label to **not exist**. When set, the selector requires an **exact match**.

All `Get` and `List` calls through the manager's cached client are automatically scoped to this install's partition. Map handlers, gating lookups, and orphan cleanup inherit that scope without per-controller list filters.

### Write path: label propagation

Multi-install resources must carry the matching label. Controllers propagate `promoter.argoproj.io/instance-id` from parent to child at creation time:

| Parent | Children that inherit the label |
| ------ | -------------------------------- |
| `PromotionStrategy` | `ChangeTransferPolicy`, previous-environment `CommitStatus` |
| `ChangeTransferPolicy` | `PullRequest` |
| Gate CRs (`ArgoCDCommitStatus`, `TimedCommitStatus`, etc.) | `CommitStatus` |

Label a root `PromotionStrategy` (or gate CR created alongside it) with the install's instance ID before the controller will reconcile it:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
  namespace: team-a
  labels:
    promoter.argoproj.io/instance-id: wave-0
spec:
  # …
```

Default-install resources must **not** carry `promoter.argoproj.io/instance-id`. If a parent lacks the label in multi-install mode, children created without it will not enter the cache and gates will silently fail.

### Reconcile behavior for foreign objects

Watches on unfiltered types (for example Argo CD `Application`) or stale queue entries may still enqueue work for names this install does not own. A `Get` against the filtered cache returns `NotFound`, and existing reconcilers treat that as a no-op.

### Argo CD Applications

Argo CD `Application` watches remain **unfiltered**—Applications do not carry Promoter instance labels. When an Application event triggers a lookup of `ArgoCDCommitStatus` objects, the cached `List` returns only this install's ACS resources. Two installs with overlapping Application selectors do not cross-reconcile each other's gates.

## Gating in multi-install mode

In the default install (`instanceID` unset), gating only sees unlabeled `CommitStatus` objects in the cache. See [CommitStatus Tenancy](advanced-usage/multi-tenancy.md#commitstatus-tenancy) for how cross-namespace references work within one partition.

When `instanceID` is set, gating is **scoped to that install**. The ChangeTransferPolicy controller lists `CommitStatus` objects through the filtered cache, so CommitStatuses from other installs never block or satisfy gates for this install.

## Migration

### Default install → multi-install

1. **Label existing resources** — Add `promoter.argoproj.io/instance-id: <your-id>` to every Promoter CR the install should manage (start with `PromotionStrategy` resources; reconcile will propagate to children on the next pass).
2. **Set `spec.instanceID`** on `ControllerConfiguration` to the same value.
3. **Restart the controller** so the filtered cache is built.

Unlabeled resources become **invisible** to the multi-install controller. Resources labeled for another instance ID are also invisible.

### Multi-install → default install

1. **Remove `promoter.argoproj.io/instance-id`** from all Promoter CRs the default install should manage.
2. **Remove `spec.instanceID`** from `ControllerConfiguration` (omit the field; do not set `""`).
3. **Restart the controller**.

Labeled resources become invisible to the default install until the label is removed.

## Operations

- **Metrics** — Resource counts reflect only objects in this install's cache partition.
- **Debugging** — See [Labels](debugging/labels.md#instance-id-multi-install) for the instance-id label reference and kubectl examples.
- **Contributors** — Custom gate controllers must propagate `InstanceIDLabel` on created `CommitStatus` objects; see [Developing a CommitStatus](contributing/developing-a-commitstatus.md).

## Related documentation

- [Configuring Multi-Tenancy](advanced-usage/multi-tenancy.md) — namespace-based tenancy within one install
- [Labels](debugging/labels.md) — full label reference including `instance-id`
- [Gating Promotions](gating-promotions/index.md) — how CommitStatuses drive promotion
