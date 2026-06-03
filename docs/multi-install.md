# Multi-Install Deployments

GitOps Promoter can be deployed as multiple independent installs on a single Kubernetes cluster, with each install scoped
to a distinct subset of resources. This is useful when you want to partition reconciliation by team, environment tier,
release cadence, or another logical grouping — without each install racing to reconcile every PromotionStrategy on the
cluster.

The mechanism is a single label-key contract: each Promoter install is configured with an `instanceID` value on its
`ControllerConfiguration` resource and only reconciles resources carrying a matching `promoter.argoproj.io/instance-id`
label.

When `instanceID` is left as the empty string (the default), the controller reconciles every resource in scope — exactly
as it does for a single-install deployment. No existing setup needs to change.

**Important**: This is distinct from [namespace-based tenancy](multi-tenancy.md), which isolates resources between teams
sharing a single Promoter install. Multi-install runs multiple Promoter controllers side-by-side and partitions across
them via the label. The two can be combined: each install can still use namespace-based tenancy internally for its own
subset of resources.

## When to use multi-install

Consider running multiple Promoter installs when:

* You want stronger isolation between groups of PromotionStrategies than namespace-based tenancy alone provides.
* You want to roll out controller upgrades to a subset of resources first (e.g., one group at a time).
* You want to run different controller versions or configurations side-by-side.
* Different groups of PromotionStrategies need different cluster-level SCM provider configurations.

If a single Promoter install with [namespace-based tenancy](multi-tenancy.md) meets your needs, prefer that — it is
simpler to operate.

## How it works

Each Promoter reconciler watches CRs across the cluster. With `instanceID: A` set on the install's
`ControllerConfiguration`, every reconciler runs the incoming watch event through a label-selector predicate before
queuing the reconcile:

* If the event's resource has the label `promoter.argoproj.io/instance-id=A`, the event is admitted.
* Otherwise, the event is dropped before any work is queued.

This applies uniformly to:

* The reconciler's primary watched CR type
* Related types the reconciler watches
* The webhook receiver's lookup of ChangeTransferPolicies for an incoming SCM webhook
* Resources created by the reconciler — child resources automatically inherit the label from their parent, so the
  partitioning is preserved at every level of the resource graph

When `instanceID` is the empty string, the predicate is a pass-through: every event is admitted, exactly as in a
single-install deployment.

## Configuring a Promoter install

Each Promoter install ships with exactly one `ControllerConfiguration` resource named
`promoter-controller-configuration` in the install's namespace. Set the `instanceID` field on that resource:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: promoter-controller-configuration
  namespace: promoter-install-a
spec:
  instanceID: A
  # ...other controller configuration fields
```

The controller reads `instanceID` once during startup. If you change the value, restart the controller for the new value
to take effect.

Each install runs its own webhook receiver on its own Service, so SCM webhooks routed per install only enqueue work for
their own resources.

## Labeling resources

Apply the `promoter.argoproj.io/instance-id` label on the top-level resources you author:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-service
  namespace: my-team
  labels:
    promoter.argoproj.io/instance-id: A
spec:
  ...
```

The same label key is used on:

* `PromotionStrategy`
* `ScmProvider` and `ClusterScmProvider`
* `GitRepository`
* `CommitStatus` and its sub-variants (`GitCommitStatus`, `WebRequestCommitStatus`, `TimedCommitStatus`,
  `ArgoCDCommitStatus`)

You only need to apply the label on resources you create directly. Resources created by Promoter reconcilers
(`ChangeTransferPolicy`, `PullRequest`, child `CommitStatus` resources) automatically inherit the label from their
parent on creation, so partitioning is preserved as the resource graph grows.

Note: `ControllerConfiguration` is the source of truth for its install's `instanceID` — do not label it. The controller
fetches its own configuration by name and namespace, not by label selector.

## ClusterScmProvider in multi-install setups

Because `ClusterScmProvider` is cluster-scoped, Kubernetes requires its name to be unique across the cluster. You cannot
have two `ClusterScmProvider` resources both named `github-prod`, even if they would be partitioned to different installs
via the label. In a multi-install setup you have two options:

1. **Use namespaced `ScmProvider` (recommended).** Each install's namespace can host its own `ScmProvider` of the same
   name. Kubernetes treats the same name in different namespaces as distinct resources, so `GitRepository` resources in
   each install's namespaces resolve to the right SCM configuration without name collisions.

2. **Use `ClusterScmProvider` with install-specific names.** If you need cluster-scoped SCM definitions, give each
   install its own uniquely-named `ClusterScmProvider`:

    ```yaml
    apiVersion: promoter.argoproj.io/v1alpha1
    kind: ClusterScmProvider
    metadata:
      name: github-prod-A
      labels:
        promoter.argoproj.io/instance-id: A
    spec:
      ...
    ---
    apiVersion: promoter.argoproj.io/v1alpha1
    kind: ClusterScmProvider
    metadata:
      name: github-prod-B
      labels:
        promoter.argoproj.io/instance-id: B
    spec:
      ...
    ```

    Each install's `GitRepository` resources then reference the install-specific name in `spec.scmProviderRef.name`.

For most multi-install deployments, namespaced `ScmProvider` is the simpler choice.

## Backwards compatibility

A controller whose `ControllerConfiguration` has `instanceID: ""` (the default) reconciles every resource in scope,
regardless of label. This applies at every gate the feature adds — the controller predicate, the label propagation to
child resources, the webhook receiver's lookup, and the in-process notification between reconcilers all short-circuit to
"no filter" when the instance ID is empty.

This means:

* Upgrading to a Promoter version with this feature does not require any change to existing single-install deployments.
* If you choose to keep one install with `instanceID: ""` alongside others, that install will reconcile every resource
  on the cluster — including resources labeled for the other installs. This is usually not what you want; prefer setting
  a non-empty `instanceID` on every install in a multi-install setup.

## Migrating an existing single install to multi-install

If you already have a single Promoter install reconciling a set of resources and want to split it into multiple installs:

1. Add the `promoter.argoproj.io/instance-id` label to each `PromotionStrategy`, `ScmProvider`, `GitRepository`, and
   top-level `CommitStatus` resource you want partitioned. Children inherit the label on their next reconcile, so they
   do not need to be labeled manually.
2. Deploy the additional Promoter installs in separate namespaces, each with its own `ControllerConfiguration` and
   `instanceID` value.
3. Either set a non-empty `instanceID` on the original install's `ControllerConfiguration` to scope it to its remaining
   subset (and restart its controller), or take the install down.

**Important**: Once an install has a non-empty `instanceID`, resources without the matching label become invisible to
that install. If you set `instanceID` on an existing install before labeling its resources, those resources will stop
reconciling on the next controller restart. Label first, then set `instanceID` and restart.
