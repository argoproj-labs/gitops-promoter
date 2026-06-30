# Multiple Controller Installs

GitOps Promoter can run more than one controller install in the same cluster. Each install reconciles only the Promoter custom resources that belong to it, so teams can share a cluster without one install observing or mutating another's PromotionStrategies, gates, or CommitStatuses.

Partitioning is **fully independent**: gating, orphan cleanup, and fan-out lists only see resources with the same instance ID as the controller.

## When to use multiple installs

Use separate installs when you need hard isolation between Promoter deployments on one cluster—for example:

- Different teams each run their own Promoter release with separate RBAC and configuration
- Blue/green or canary controller upgrades where old and new installs coexist briefly
- Environments where one cluster hosts multiple logically separate Promoter "tenants"

For namespace-scoped access control among PromotionStrategy users in a **single** install, see [Configuring Multi-Tenancy](advanced-usage/multi-tenancy.md). Instance ID partitioning is orthogonal: it scopes which install reconciles which CRs, not which namespaces users may write to.

## Configuration

Set `spec.instanceID` on the `ControllerConfiguration` resource in the controller's install namespace:

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

| Value | Behavior |
| ----- | -------- |
| `""` (empty) | **Single-install mode** — same as today. No cache filtering; the controller reconciles all Promoter CRs in scope. |
| Non-empty string | **Multi-install mode** — the controller only caches and reconciles Promoter CRs labeled `promoter.argoproj.io/instance-id=<value>`. |

The value must be a valid Kubernetes label value (max 63 characters; alphanumeric, `.`, `_`, `-`).

**Changing `instanceID` requires a controller restart.** The value is read once at startup before the manager cache is built.

## How partitioning works

### Read path: informer cache filtering

At startup the controller reads `instanceID` from `ControllerConfiguration` (via a direct API read, not the informer cache) and configures `cache.ByObject` label selectors for every reconciled Promoter CRD:

- `PromotionStrategy`, `ChangeTransferPolicy`, `CommitStatus`, `PullRequest`
- `ScmProvider`, `ClusterScmProvider`, `GitRepository`
- `GitCommitStatus`, `TimedCommitStatus`, `WebRequestCommitStatus`, `ArgoCDCommitStatus`
- `RevertCommit`

`ControllerConfiguration` itself is **not** filtered—the install must always read its own configuration.

When `instanceID` is empty, no `ByObject` filters are applied.

All `Get` and `List` calls through the manager's cached client are automatically scoped to this install's resources. Map handlers, gating lookups, and orphan cleanup inherit that scope without per-controller list filters.

### Write path: label propagation

Resources only enter a filtered cache if they carry the matching label. Controllers propagate `promoter.argoproj.io/instance-id` from parent to child at creation time:

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

If a parent lacks the label, children created without it will not appear in the filtered cache and gates will silently fail to affect promotion.

### Reconcile behavior for foreign objects

Watches on unfiltered types (for example Argo CD `Application`) or stale queue entries may still enqueue work for names this install does not own. A `Get` against the filtered cache returns `NotFound`, and existing reconcilers treat that as a no-op.

### Argo CD Applications

Argo CD `Application` watches remain **unfiltered**—Applications do not carry Promoter instance labels. When an Application event triggers a lookup of `ArgoCDCommitStatus` objects, the cached `List` returns only this install's ACS resources. Two installs with overlapping Application selectors do not cross-reconcile each other's gates.

## Gating in multi-install mode

In single-install mode (`instanceID` empty), `CommitStatus` references in a `PromotionStrategy` are effectively cluster-scoped for gating: any matching `CommitStatus` key on the cluster can affect promotion. See [CommitStatus Tenancy](advanced-usage/multi-tenancy.md#commitstatus-tenancy).

When `instanceID` is set, gating is **scoped to that install**. The ChangeTransferPolicy controller lists `CommitStatus` objects through the filtered cache, so CommitStatuses from other installs never block or satisfy gates for this install.

## Migration from single-install

Moving an existing deployment to multi-install mode is stricter with cache filtering:

1. **Label existing resources first** — Add `promoter.argoproj.io/instance-id: <your-id>` to every Promoter CR the install should manage (start with `PromotionStrategy` resources; reconcile will propagate to children on the next pass, but labeling roots avoids a gap).
2. **Set `spec.instanceID`** on `ControllerConfiguration` to the same value.
3. **Restart the controller** so the filtered cache is built.

Unlabeled resources become **invisible** to a filtered install—they are not deleted, but this controller will not reconcile them until labeled.

## Operations

- **Metrics** — Resource count metrics reflect only objects in this install's cache when filtering is enabled.
- **Debugging** — See [Labels](debugging/labels.md#instance-id-multi-install) for the instance-id label reference and kubectl examples.
- **Contributors** — Custom gate controllers must propagate `InstanceIDLabel` on created `CommitStatus` objects; see [Developing a CommitStatus](contributing/developing-a-commitstatus.md).

## Related documentation

- [Configuring Multi-Tenancy](advanced-usage/multi-tenancy.md) — namespace-based tenancy within one install
- [Labels](debugging/labels.md) — full label reference including `instance-id`
- [Gating Promotions](gating-promotions/index.md) — how CommitStatuses drive promotion
