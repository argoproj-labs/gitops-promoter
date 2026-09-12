# Dependents Successful Commit Status Controller

The Dependents Successful Commit Status controller gates promotions based on whether an environment's **dependent
environments** have taken the change being promoted and are [successful](../index.md#environment-success). For each
environment, it writes a proposed `CommitStatus` (the ordering gate) whose `phase` tells you whether promotion may proceed:

- **`success`** — every configured dependent environment has promoted the same dry commit this environment is promoting,
  and each dependent is successful for what is live on its active branch.
- **`pending`** — still waiting on one or more dependents (not yet promoted, or not yet successful).

The controller reads the referenced PromotionStrategy's environment status, evaluates the configured dependency
relationships, and updates one CommitStatus per environment.

> [!IMPORTANT]
> Create a `DependentsSuccessfulCommitStatus` for each PromotionStrategy you want to gate, and set required
> `spec.orderCommitStatusRef` on that PromotionStrategy. The PromotionStrategy controller injects `spec.key` onto every
> `ChangeTransferPolicy`; you do not declare the ordering key in `proposedCommitStatuses`. See
> [Wiring the gate into the PromotionStrategy](#wiring-the-gate-into-the-promotionstrategy) below.

## Linear default (no explicit graph)

For a standard linear pipeline (dev → staging → prod), omit `dependsOn` on every environment. The controller infers a
chain from the referenced PromotionStrategy's `spec.environments` order: the first environment is a root, and each
subsequent environment `dependsOn` the one before it.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
spec:
  gitRepositoryRef:
    name: demo
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsSuccessfulCommitStatus
    name: demo
  environments:
    - branch: environment/dev
    - branch: environment/staging
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

## Custom dependency graph

A diamond graph — `dev` fans out to `e2e` and `perf`, which fan back in to `prod`. Set `dependsOn` on the
PromotionStrategy environments:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  gitRepositoryRef:
    name: demo
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsSuccessfulCommitStatus
    name: demo-dag
  environments:
    - branch: environment/dev
    - branch: environment/e2e
      dependsOn:
        - environment/dev
    - branch: environment/perf
      dependsOn:
        - environment/dev
    - branch: environment/prod
      dependsOn:
        - environment/e2e
        - environment/perf
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo-dag
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: demo-dag
```

### `PromotionStrategy.spec.environments[].dependsOn`

Declares which upstream branches an environment waits on. **Optional** — when no environment declares `dependsOn`, the
controller infers a linear chain from list order. When any environment declares `dependsOn`, omitted `dependsOn` on
other entries means that environment is a root. The graph must be acyclic; cycles and references to unknown branches
are rejected.

### `spec.key`

`spec.key` is the gate name the PromotionStrategy controller injects onto every `ChangeTransferPolicy`'s
`proposedCommitStatuses`. A common value is `dependents-successful`.

### Commit Status URL Template

To set the SCM details URL on each per-environment gate `CommitStatus` (for example a link into the Promoter UI),
configure `spec.url.template`. The template uses [Go templates](https://pkg.go.dev/text/template) syntax and most
[Sprig](https://masterminds.github.io/sprig/) functions (excluding `env`, `expandenv`, and `getHostByName`) are
supported, plus [`urlQueryEscape`](https://pkg.go.dev/net/url#QueryEscape) for query parameters.

> [!IMPORTANT]
> The rendered URL must use a scheme of either `http` or `https`. When `url.template` is omitted, no URL is set on the
> child CommitStatus.

#### Template Variables

- `.Environment` — the environment branch name the URL is being rendered for
- `.DependentsSuccessfulCommitStatus` — the whole [DependentsSuccessfulCommitStatus](../../crd-specs.md#dependentssuccessfulcommitstatus) CR
- `.PromotionStrategy` — the referenced [PromotionStrategy](../../crd-specs.md#promotionstrategy)
- `.DependsOn` — the current environment's immediate upstream branches (one edge away), from
  `PromotionStrategy.spec.environments[].dependsOn` (or the inferred linear chain)
- `.DependsOnQuery` — `.DependsOn` encoded as repeated `env=` query parameters for Promoter UI deep links (for example
  `env=environment%2Fe2e&env=environment%2Fperf`). Empty for roots with no `dependsOn`. Append after `?` in the
  template; do not add a leading `?` yourself inside this field.

#### Template Options

Same `missingkey=...` options as other commit status URL templates:

- `missingkey=default` or `missingkey=invalid` — continue; missing map keys print as `<no value>`
- `missingkey=zero` — return the zero value for the map element type
- `missingkey=error` — fail the reconcile if a missing key is indexed

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo-dag
spec:
  url:
    template: ...
    options:
      - missingkey=error
```

#### Examples

Simple URL that includes the current environment:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: demo-dag
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: demo-dag
  url:
    template: "https://promoter.example.com/promotion-strategies/{{ .PromotionStrategy.Name }}?env={{ urlQueryEscape .Environment }}"
```

With matching `dependsOn` on the PromotionStrategy:

```yaml
spec:
  environments:
    - branch: environment/dev
    - branch: environment/staging
      dependsOn:
        - environment/dev
```

Highlight this environment's immediate `dependsOn` upstreams (useful for SCM "View details" deep links). Use
`.DependsOnQuery` so the template stays small; roots with an empty `dependsOn` omit the query string:

```yaml
url:
  template: |
    {{- $base := printf "https://promoter.example.com/promotion-strategies/%s" .PromotionStrategy.Name -}}
    {{- if .DependsOnQuery -}}{{ printf "%s?%s" $base .DependsOnQuery }}{{- else -}}{{ $base }}{{- end -}}
```

For a custom encoding (something other than repeated `env=`), use `.DependsOn` directly. For example, a
comma-separated `upstreams=` query:

```yaml
url:
  template: |
    {{- $base := printf "https://promoter.example.com/promotion-strategies/%s" .PromotionStrategy.Name -}}
    {{- if .DependsOn -}}
    {{ printf "%s?upstreams=%s" $base (urlQueryEscape (join "," .DependsOn)) }}
    {{- else -}}
    {{ $base }}
    {{- end -}}
```

## Wiring the gate into the PromotionStrategy

The DependentsSuccessfulCommitStatus produces the gate; the PromotionStrategy consumes it through required
`orderCommitStatusRef`. The PromotionStrategy controller injects `spec.key` onto every `ChangeTransferPolicy`'s
`proposedCommitStatuses` — do not declare the ordering key yourself.

`orderCommitStatusRef` uses a `group` / `kind` / `name` triple. Resolution uses a generic contract:
`spec.key` and `spec.promotionStrategyRef.name` at whatever API version the CRD is served on (from cluster
discovery). That applies to `DependentsSuccessfulCommitStatus` and to out-of-tree ordering gates alike. The Promoter
ServiceAccount needs RBAC `get` on the referenced CRD, and the gate controller must write the ordering `CommitStatus`
objects the CTP waits on.

Use `kind: DependentsSuccessfulCommitStatus` (the default) for the built-in ordering gate unless you operate a custom
ordering gate CR that also follows this contract.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  gitRepositoryRef:
    name: dag-example-apps
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsSuccessfulCommitStatus
    name: demo-dag
  environments:
    - branch: environment/dev
    - branch: environment/e2e
      dependsOn:
        - environment/dev
    - branch: environment/perf
      dependsOn:
        - environment/dev
    - branch: environment/prod
      dependsOn:
        - environment/e2e
        - environment/perf
```

> [!IMPORTANT]
> The PromotionStrategy controller fails its reconcile when `orderCommitStatusRef` points at a missing gate object,
> when the referenced `group`/`kind` is not a supported ordering gate, or when the gate's `promotionStrategyRef.name`
> does not match the owning PromotionStrategy.

## Status (`status.environments`)

Each environment branch in the dependency graph appears once under `status.environments[]` (`listMapKey=branch`). The
controller populates this list on every reconcile so operators and UI can see upstream health without drilling into the
PromotionStrategy.

| Field | Always present | Notes |
|-------|----------------|-------|
| `branch` | yes | Environment branch name |
| `activeCommitStatuses` | optional | Verbatim copy of `PromotionStrategy.status.environments[].active.commitStatuses` (`key`, `phase`, `description`, `url`). Omitted when empty. |
| `upstreams` | optional | Transitive ancestor closure: `[{branch, satisfied, reason}]`. Omitted when empty (for example on roots). `reason` is set when `satisfied` is false. |

Gate report fields (`phase`, `description`, `url`, `reportedSha`) mirror the child `CommitStatus` spec. While a
promotion is in flight (`active.dry.sha != proposed.dry.sha`), the controller re-evaluates and updates both the child
`CommitStatus` and the mirror fields. When there is no proposed change, the controller skips re-evaluation but still
copies the last child `CommitStatus` onto `status.environments[]` when one exists. Branches that have never been gated
omit those fields but still report `activeCommitStatuses` and `upstreams`.

`upstreams[].satisfied` is `true` when that ancestor has promoted and is healthy for this environment's target dry SHA
(same evaluation as the gate). The gate's own pass/fail checks **direct** `PromotionStrategy.spec.environments[].dependsOn`
only; each direct upstream's `satisfied` value was computed with full no-op recursion.

### Consumer workflow when pending

1. Read the gated environment's `description` (matches the SCM commit status).
2. Scan `upstreams` for `satisfied: false`.
3. For each unsatisfied upstream, inspect that branch's `activeCommitStatuses` (per-gate `description` / `url`).
4. For hydrator or promotion SHA detail, read `PromotionStrategy.status.environments`.

Gate controller authors: see [Commit Status Controller Best Practices](../../contributing/developing-a-commitstatus.md#gate-statusenvironments-standard).

### Example

```yaml
status:
  environments:
    - branch: environment/dev
      activeCommitStatuses:
        - key: argocd-health
          phase: success
    - branch: environment/prod
      activeCommitStatuses:
        - key: argocd-health
          phase: success
      upstreams:
        - branch: environment/dev
          satisfied: true
        - branch: environment/staging
          satisfied: false
          reason: Waiting for "environment/staging" to be promoted
      phase: pending
      description: Waiting for "environment/staging" to be promoted
      url: https://promoter.example.com/ps/demo-dag?env=environment%2Fstaging
      reportedSha: abc123def4567890abcdef1234567890abcdef12
```
