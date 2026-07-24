# Gating Promotions

Most environment promotion strategies will involve enforcing some kind of "gates" between promotions.

> **Multiple controller installs:** When more than one Promoter controller runs in the same cluster, label gate CRs, `PromotionStrategy` resources, and user-created roots (`GitRepository`, `ScmProvider`, `ClusterScmProvider`) with `promoter.argoproj.io/instance-id` so each install only reconciles its own gates and CommitStatuses. See [Multiple Controller Installs](../multi-install.md).

GitOps promoter uses the [PromotionStrategy API](../crd-specs.md#promotionstrategy) to configure checks that must pass
between environments. It uses the [CommitStatus API](../crd-specs.md#commitstatus) to understand the state of the checks.

A "proposed commit status" is a check which must be passing on a proposed change before it can be merged. To set a 
CommitStatus to be used as a proposed commit status, set the `spec.sha` field to the commit hash of the proposed change
in the proposed (`-next`) environment branch.

An "active commit status" is a check which must be passing on an active (already merged) change before the change can be
merged for the next environment. To set a CommitStatus to be used as an active commit status, set the `spec.sha` field 
to the commit hash of the active change in the live environment branch.

Promotion ordering (which environments may promote relative to others) is also expressed as a proposed commit
status. It is **not** injected automatically: you must create a
[PreviousEnvironmentCommitStatus](built-in-gates/previous-environment-commit-status.md) (linear pipelines) or a
[DAGCommitStatus](built-in-gates/dag-commit-status.md) (arbitrary graphs) and declare its `key` in the
PromotionStrategy's global `proposedCommitStatuses`. Without an ordering gate, the PromotionStrategy controller fails
its reconcile so environments cannot promote out of order by accident.

## Example

The following example demonstrates how to configure a PromotionStrategy to use CommitStatuses for both a proposed and
an active commit status check.

```yaml
kind: PromotionStrategy
metadata:
  name: demo
spec:
  proposedCommitStatuses:
    - key: promoter-previous-environment # ordering gate; must match PreviousEnvironmentCommitStatus.spec.key
  activeCommitStatuses:
    - key: healthy
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
      proposedCommitStatuses:
        - key: deployment-freeze
---
kind: PreviousEnvironmentCommitStatus
metadata:
  name: demo
spec:
  key: promoter-previous-environment
  promotionStrategyRef:
    name: demo
```

In this example, the PromotionStrategy has three environments: `environment/dev`, `environment/test`, and `environment/prod`.
All environments have a `healthy` active commit status check and the linear ordering gate
`promoter-previous-environment`. The `environment/prod` environment has an additional `deployment-freeze` proposed
commit status check.

Suppose the environment branches have been hydrated from the `main` branch and that the branches have the following
commit SHAs:

| Branch                  | SHA      |
|-------------------------|----------|
| `main`                  | `b5d8f7` |
| `environment/dev`       | `a1b2c3` |
| `environment/dev-next`  | `d4e5f6` |
| `environment/test`      | `a7b8c9` |
| `environment/test-next` | `d0e1f2` |
| `environment/prod`      | `a3b4c5` |
| `environment/prod-next` | `d6e7f8` |

For a change to be promoted through all environments, the following CommitStatuses must exist:

```yaml
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a1b2c3  # environment/dev
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a7b8c9  # environment/test
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a3b4c5  # environment/prod
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: promoter-previous-environment
spec:
  sha: d0e1f2  # environment/test-next
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: promoter-previous-environment
spec:
  sha: d6e7f8  # environment/prod-next
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: deployment-freeze
spec:
  sha: d6e7f8  # environment/prod-next
  phase: success
```

Note that all the active commit statuses have SHAs corresponding to the active environment branches, and the proposed
commit statuses (including the ordering gate) have SHAs corresponding to the proposed (`-next`) environment branches.

Any tool wanting to gate an active commit status must create and update CommitStatuses with the appropriate SHAs for 
the respective environments' live environment branches.

Any tool wanting to gate a proposed commit status must create and update CommitStatuses with the appropriate SHAs for
the respective environments' proposed (`-next`) environment branches.

### How Environment Ordering Works (Implementation Details)

The PromotionStrategy controller creates a ChangeTransferPolicy for each environment and copies the PromotionStrategy's
declared `activeCommitStatuses` / `proposedCommitStatuses` (global plus per-environment) onto that CTP. It does **not**
inject an ordering key or create ordering CommitStatuses itself.

The ChangeTransferPolicy controller does not "look back" at previous environments. Ordering is enforced like any other
proposed commit status: the CTP waits for a CommitStatus whose `promoter.argoproj.io/commit-status` label matches the
ordering key and whose `spec.sha` matches the proposed hydrated SHA.

Those ordering CommitStatuses are written by the [DAGCommitStatus](built-in-gates/dag-commit-status.md) controller.
[PreviousEnvironmentCommitStatus](built-in-gates/previous-environment-commit-status.md) is a thin adapter that generates
a chain-shaped DAGCommitStatus from the PromotionStrategy's environment list (in spec order). The DAG controller
evaluates upstream environments (same dry commit promoted and healthy) and sets `phase` accordingly.

So for the above example, the `environment/test` ChangeTransferPolicy CR looks like this:

```yaml
kind: ChangeTransferPolicy
spec:
  sourceBranch: environment/test-next
  targetBranch: environment/test
  activeCommitStatuses:
    # The controller will monitor this CommitStatus for the active commit SHA, but it will not enforce it. The status 
    # will be stored on the 
    - key: healthy
  proposedCommitStatuses:
    - key: promoter-previous-environment
```

When the previous environment (`environment/dev`) has promoted the same dry commit and is healthy, the ordering
CommitStatus for test looks like this:

```yaml
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: promoter-previous-environment
spec:
  sha: d0e1f2  # environment/test-next
  phase: success
```

The SHA is the proposed hydrated SHA of the environment being gated (`environment/test-next`). Phase is `success`
when the previous environment has promoted the same dry commit and is healthy. In this linear example,
PreviousEnvironmentCommitStatus produces that CommitStatus via the DAGCommitStatus it generates.

#### Previous Environment CommitStatus URL

Since the previous environment CommitStatus aggregates the active commit status checks of the previous environment, it
is nontrivial to determine what URL to use for the aggregate CommitStatus.

For now, the previous environment CommitStatus will only be set if there is only one active commit status. Its URL will
be set to the URL of the previous environment's active commit status. If there are multiple active commit statuses, no
URL will be set. This behavior may change in the future.

## Built-In Gates

GitOps Promoter ships built-in gate controllers that create and manage `CommitStatus` resources automatically. Each gate has a matching CRD kind (for example `ArgoCDCommitStatus`); configure them in your cluster and reference their `spec.key` in `PromotionStrategy`.

### Environment Ordering

Promotion ordering is required for every PromotionStrategy. Use one of:

- [PreviousEnvironmentCommitStatus](built-in-gates/previous-environment-commit-status.md) — linear pipelines
  (dev → staging → prod). Generates a chain-shaped [DAGCommitStatus](built-in-gates/dag-commit-status.md).
- [DAGCommitStatus](built-in-gates/dag-commit-status.md) — arbitrary directed acyclic graphs.

Declare the gate `key` in the PromotionStrategy's global `proposedCommitStatuses`. See those pages for wiring details.

### Argo CD Health Status

The [ArgoCDCommitStatus](built-in-gates/argocd-commit-status.md) controller monitors Argo CD Applications and creates CommitStatus resources based on application health. This enables gating promotions based on whether applications are healthy in their current environment.

Key features:

- Monitors Argo CD Applications with specific labels
- `spec.key` (default `argocd-health` when omitted; set explicitly, including the default value)
- Reports application health status (Healthy, Progressing, Degraded, etc.)

### Time-Based Gating

The [TimedCommitStatus](built-in-gates/timed-commit-status.md) controller implements "soak time" or "bake time" requirements, ensuring changes run in lower environments for a minimum duration before being promoted.

Key features:

- Monitors how long commits have been running in each environment
- `spec.key` (default `timer` when omitted; set explicitly, including the default value)
- Reports pending until the required duration is met
- Prevents promotions when there are pending changes in lower environments

### Scheduled Gating

The [ScheduledCommitStatus](built-in-gates/scheduled-commit-status.md) controller gates promotions based on cron-based time windows. It evaluates recurring allow and exclude windows to determine whether promotions are permitted at the current time, enabling business-hours-only rollouts, maintenance windows, and deployment freezes.

Key features:

- Cron-based allow/exclude windows with durations
- `spec.key` (required)
- Global windows shared across all listed environments, with per-environment overrides
- Per-environment timezone support (IANA names)
- Precise requeuing at window transition times (no polling)
- Exclusion-only mode (allow everything except during blackout periods)

### Web Request (HTTP) Validation

The [WebRequestCommitStatus](built-in-gates/web-request-commit-status/index.md) controller gates promotions on external HTTP/HTTPS APIs. It calls configurable endpoints, evaluates the response with expressions, and creates CommitStatus resources so the SCM shows success or pending.

Key features:

- **Polling or trigger mode:** Poll at an interval or only when a trigger expression fires (e.g. when SHA changes)
- **Validation expression:** Uses the [expr](https://github.com/expr-lang/expr) language; `true` means validation passed (CommitStatus phase success), `false` means pending
- **Optional response expression:** Extract a subset of the HTTP response into `ResponseOutput` for use in the next trigger evaluation and in description/URL templates
- **TriggerOutput:** Trigger when.output expression can return extra fields that are stored and available on the next run and in templates
- **SuccessOutput:** Success when.output expression can return extra fields that are stored and available on the next run in trigger, success expressions, and templates
- **Shared expr (`when.variables`):** Optional map expression whose result is available as **`Variables`** to `when.expression` and `when.output.expression` on the same `when` block (trigger and success); see [Web Request Commit Status](built-in-gates/web-request-commit-status/index.md#shared-trigger-and-success-expr-whenvariables)
- **Templated URL, headers, body:** Go templates with `Branch`, `Phase`, `PromotionStrategy`, `WebRequestCommitStatus`, `TriggerOutput`, `ResponseOutput`, `SuccessOutput`, namespace metadata, etc.
- **Authentication:** Basic, Bearer, OAuth2, or mutual TLS via Secrets
- **reportOn:** Report on the proposed commit (default) or the active (deployed) commit

### Custom Controllers

You can also create your own controllers that manage CommitStatus resources. Any system that can create Kubernetes resources can participate in the gating logic by creating CommitStatus resources with the appropriate SHAs and phases.
