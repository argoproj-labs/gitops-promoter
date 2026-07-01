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

**Default install** — leave `spec.instanceID` unset (do not set it to `""`).

| `spec.instanceID` | Behavior |
| ----------------- | -------- |
| **Unset (nil)** | **Default install** — only Promoter CRs **without** `promoter.argoproj.io/instance-id` enter the cache. |
| **Non-empty string** | **Multi-install** — only CRs labeled `promoter.argoproj.io/instance-id=<value>` exactly. |

There is **no match-all mode**. Labeled and unlabeled resources are never reconciled by the same install.

The value must be a valid Kubernetes label value (min length 1, max 63 characters; alphanumeric, `.`, `_`, `-`).

**Changing `instanceID` rebuilds the informer cache partition.** The value is read once at startup before the manager cache is built. In the default single-replica install, the controller detects `spec.instanceID` drift and exits so Kubernetes restarts the pod with the new partition. With **multiple replicas and leader election**, only the leader observes the change and restarts itself; follower pods keep the old partition until you roll the deployment (for example `kubectl rollout restart deployment/<controller>`).

## How partitioning works

### Read path: informer cache filtering

At startup the controller reads `instanceID` from `ControllerConfiguration` (via a direct API read, not the informer cache) and configures `cache.ByObject` label selectors for every reconciled Promoter CRD and for `Secret` objects:

- `PromotionStrategy`, `ChangeTransferPolicy`, `CommitStatus`, `PullRequest`
- `ScmProvider`, `ClusterScmProvider`, `GitRepository`
- `GitCommitStatus`, `TimedCommitStatus`, `WebRequestCommitStatus`, `ArgoCDCommitStatus`
- `RevertCommit`
- `Secret` (SCM credentials, HTTP auth, kubeconfig, and other secrets fetched through the manager client)

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

**Secrets are not auto-labeled.** Label every `Secret` this install reads (for example `ScmProvider.spec.secretRef`, `WebRequestCommitStatus` HTTP auth `secretRef`, and multicluster kubeconfig secrets in the controller namespace) with the same `promoter.argoproj.io/instance-id` value before or during migration. Unlabeled secrets are invisible to a multi-install controller; labeled secrets are invisible to the default install.

### Reconcile behavior for foreign objects

Watches on unfiltered types (for example Argo CD `Application`) or stale queue entries may still enqueue work for names this install does not own. A `Get` against the filtered cache returns `NotFound`, and existing reconcilers treat that as a no-op.

### Argo CD Applications

Argo CD `Application` watches remain **unfiltered**—Applications do not carry Promoter instance labels. When an Application event triggers a lookup of `ArgoCDCommitStatus` objects, the cached `List` returns only this install's ACS resources. Two installs with overlapping Application selectors do not cross-reconcile each other's gates.

## Gating in multi-install mode

In the default install (`instanceID` unset), gating only sees unlabeled `CommitStatus` objects in the cache. See [CommitStatus Tenancy](advanced-usage/multi-tenancy.md#commitstatus-tenancy) for how cross-namespace references work within one partition.

When `instanceID` is set, gating is **scoped to that install**. The ChangeTransferPolicy controller lists `CommitStatus` objects through the filtered cache, so CommitStatuses from other installs never block or satisfy gates for this install.

## Migration runbook

Follow these principles whenever you add, change, or remove `promoter.argoproj.io/instance-id` or `ControllerConfiguration.spec.instanceID`:

### Principles

- **One concern per change** — change `metadata.labels[promoter.argoproj.io/instance-id]` in a separate apply from any `spec` edits. Combining both in one request is discouraged: orphan cleanup can miss stale children if topology shrinks during the migration window.
- **Coordinate across resources** — label all roots (`PromotionStrategy`, gate CRs, and any other Promoter CRs this install owns) with the **same** value before restart. Do not relabel a `PromotionStrategy` while shrinking a gate's `spec` in parallel.
- **Install config is separate** — set `ControllerConfiguration.spec.instanceID` in its own step. Single-replica installs restart automatically; HA installs need a rolling restart of all controller pods afterward.
- **Cross-CR coordination is runbook-only** — the API does not enforce ordering across multiple resources; follow the steps below deliberately.

### Default install → multi-install

1. **Label roots only** — add `promoter.argoproj.io/instance-id: <your-id>` to every Promoter CR this install should manage (`PromotionStrategy`, `TimedCommitStatus`, `GitCommitStatus`, `WebRequestCommitStatus`, `ArgoCDCommitStatus`, and others as needed). Label referenced `Secret` objects (SCM, HTTP auth, kubeconfig) with the same value. Use metadata-only patches.
2. **Expect the gap** — the currently running default install stops reconciling relabeled parents immediately (they leave its informer cache). **Children are not relabeled until after restart** on the new partition. This is expected, not a failure.
3. **Set `spec.instanceID`** on `ControllerConfiguration` to the same value (non-empty). The controller pod restarts automatically in a single-replica install.
4. **Wait for propagation** — confirm children carry the label and labeled resources report `status.instanceID` (see [Verification](#verification) below).
6. **Edit spec only after propagation** — environment removal, selector changes, Argo CD selector tightening, and similar topology edits.

Unlabeled resources become **invisible** to the multi-install controller. Resources labeled for another instance ID are also invisible.

### Multi-install → default install

1. **Remove `promoter.argoproj.io/instance-id`** from all Promoter CRs the default install should manage (metadata-only patches). Remove the label from referenced `Secret` objects as well.
2. **Remove `spec.instanceID`** from `ControllerConfiguration` (omit the field; do not set `""`). The controller pod restarts automatically in a single-replica install.
3. **Wait for propagation** — children should have the label removed and `status.instanceID` cleared on labeled resources.
4. **Edit spec only after propagation**.

Labeled resources become invisible to the default install until the label is removed.

### Verification

#### `metadata.generation` and `status.observedGeneration`

**Label changes do not affect `metadata.generation`.** For Promoter CRDs (which have a `/status` subresource), Kubernetes increments `.metadata.generation` only when **`.spec`** changes. A metadata-only patch that adds, removes, or changes `promoter.argoproj.io/instance-id` leaves `generation` unchanged.

**Do not use `status.observedGeneration` as the primary migration signal.** It is stamped to match `metadata.generation` on each successful status write and exists to detect stale status after **spec** changes. Because label-only edits do not bump `generation`, `observedGeneration == generation` may already have been true before relabeling and does not prove children were relabeled.

Built-in controllers use `GenerationChangedPredicate` on roots such as `PromotionStrategy`, so a label-only update does not enqueue a reconcile on the **currently running** install. Propagation happens on the **first reconcile after restart** (informer resync), not because `generation` changed.

#### `status.instanceID`

| Field | Meaning |
| ----- | ------- |
| `metadata.labels[promoter.argoproj.io/instance-id]` | Operator intent — which install should own this object |
| `status.instanceID` | Mirrors `metadata.labels[promoter.argoproj.io/instance-id]` on each reconcile attempt by this install's controller (set on every Promoter CR type), including when `Ready=False` |
| Child label checks | Ground truth for the cluster — catches drift, orphans, or hand-edited children |

`status.instanceID` matching the metadata label means this install's controller is actively reconciling that resource (including failed attempts). It does **not** guarantee there are no stale orphans or that every Promoter CR in the namespace is labeled.

#### What to check

| Check | Meaning |
| ----- | ------- |
| Child `metadata.labels[promoter.argoproj.io/instance-id]` | Primary success criterion — CTPs, gate `CommitStatus`es, PRs match install ID |
| Referenced `Secret` labels | SCM, HTTP auth, and kubeconfig secrets match install ID |
| `status.instanceID` on labeled Promoter CRs | Active reconcile mirrored the metadata label into status |
| `status.conditions[Ready=True]` on PS / gates | Healthy reconcile after restart |
| Gating behavior | CTP commit status phases not stuck at pending for gate keys |
| No stray unlabeled Promoter CRs in scope | Objects that should be in the partition are not missing the label |

### kubectl examples

**Label-only patch on a root** (separate apply from any spec edit):

```bash
kubectl label promotionstrategy my-app -n team-a \
  promoter.argoproj.io/instance-id=wave-0 --overwrite
```

**Set install partition** (single-replica installs restart automatically; roll the deployment in HA):

```bash
kubectl patch controllerconfiguration promoter-controller-configuration -n gitops-promoter \
  --type=merge -p '{"spec":{"instanceID":"wave-0"}}'
```

**Rolling restart after instanceID change in HA** (all replicas must restart to pick up the new partition):

```bash
kubectl rollout restart deployment/<controller-deployment> -n gitops-promoter
```

**Confirm roots are labeled**:

```bash
kubectl get promotionstrategy,timedcommitstatus,gitcommitstatus,webrequestcommitstatus,argocdcommitstatus \
  -n team-a -l promoter.argoproj.io/instance-id=wave-0
```

**Parent acknowledges propagation**:

```bash
kubectl get promotionstrategy my-app -n team-a \
  -o jsonpath='{.metadata.labels.promoter\.argoproj\.io/instance-id}{" -> "}{.status.instanceID}{"\n"}'

kubectl wait promotionstrategy/my-app -n team-a \
  --for=jsonpath='{.status.instanceID}'=wave-0 --timeout=120s
```

**Confirm children inherited the label**:

```bash
# CTPs for a strategy
kubectl get changetransferpolicy -n team-a \
  -l promoter.argoproj.io/promotion-strategy=my-app,promoter.argoproj.io/instance-id=wave-0

# Gate CommitStatuses for a timed gate
kubectl get commitstatus -n team-a \
  -l promoter.argoproj.io/timed-commit-status=my-timer,promoter.argoproj.io/instance-id=wave-0
```

**Find Promoter CRs still missing the label** (should be empty in scope after propagation):

```bash
kubectl get promotionstrategy,changetransferpolicy,commitstatus,pullrequest \
  -n team-a -l '!promoter.argoproj.io/instance-id'
```

**Ready condition after restart** (secondary health signal):

```bash
kubectl get promotionstrategy my-app -n team-a \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}{"\n"}'
```

**ControllerConfiguration matches labels**:

```bash
kubectl get controllerconfiguration promoter-controller-configuration -n gitops-promoter \
  -o jsonpath='{.spec.instanceID}{"\n"}'
```

**Do not rely on generation for migration** — this output is often unchanged after label-only patches:

```bash
kubectl get promotionstrategy my-app -n team-a \
  -o jsonpath='{.metadata.generation}{" "}{.status.observedGeneration}{"\n"}'
```

See also [Labels — Instance ID](debugging/labels.md#instance-id-multi-install) for broader label debugging.

### Avoid during migration

- Same apply: `instance-id` label + any `spec` change on a root CR (discouraged — can strand orphans if topology shrinks before children relabel).
- Topology shrink (remove environments, drop selector keys, tighten Argo CD selectors) before propagation completes.
- Setting `instanceID: ""` on `ControllerConfiguration` — omit the field for the default install.
- Hand-editing `instance-id` on child objects (`ChangeTransferPolicy`, `CommitStatus`, `PullRequest`).
- Changing `ControllerConfiguration.spec.instanceID` in HA without rolling all controller pods — followers keep the old cache partition and reconcile the wrong resources on failover.

### Troubleshooting

- Gates pending / "Waiting for status to be reported" — `CommitStatus` label mismatch with `ControllerConfiguration.spec.instanceID`.
- Promotion stuck after migration — confirm gate `CommitStatus` and CTP labels match the install partition.
- `status.instanceID` empty while metadata label is set — controller has not reconciled since restart; check that the install partition matches the label.
- Stranded orphans — topology shrink coincided with label migration; delete orphans manually by name.

## Operations

- **Metrics** — Resource counts reflect only objects in this install's cache partition.
- **Debugging** — See [Labels](debugging/labels.md#instance-id-multi-install) for the instance-id label reference and kubectl examples.
- **Contributors** — Custom gate controllers must propagate `InstanceIDLabel` on created `CommitStatus` objects; see [Developing a CommitStatus](contributing/developing-a-commitstatus.md).

## Related documentation

- [Configuring Multi-Tenancy](advanced-usage/multi-tenancy.md) — namespace-based tenancy within one install
- [Labels](debugging/labels.md) — full label reference including `instance-id`
- [Gating Promotions](gating-promotions/index.md) — how CommitStatuses drive promotion
