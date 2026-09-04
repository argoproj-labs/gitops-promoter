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
> The gate is not created or injected automatically. You must create a `DependentsSuccessfulCommitStatus` for each
> PromotionStrategy you want to gate, and add its `key` to that PromotionStrategy's effective `proposedCommitStatuses`
> (globally or per environment). See [Wiring the gate into the PromotionStrategy](#wiring-the-gate-into-the-promotionstrategy) below.

## Linear default (no graph)

For a standard linear pipeline (dev → staging → prod), omit `spec.environments`. The controller infers a chain from the
referenced PromotionStrategy's `spec.environments` order: the first environment is a root, and each subsequent
environment `dependsOn` the one before it.

```yaml
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

A diamond graph — `dev` fans out to `e2e` and `perf`, which fan back in to `prod`:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
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
    - branch: environment/perf
      dependsOn:
        - environment/dev
    - branch: environment/prod
      dependsOn:
        - environment/e2e
        - environment/perf
```

### `spec.environments`

Declares which environments each branch depends on. **Optional** — when omitted or empty, the controller infers a
linear chain from the PromotionStrategy's `spec.environments` order. When set, each entry names an environment `branch`
and the upstream `dependsOn` branches it waits on. An entry with no `dependsOn` is a root (for example `dev` below).
The set of `branch` values must exactly match the referenced PromotionStrategy's `environments`. The graph must be
acyclic; cycles and references to unknown branches are rejected.

### `spec.key`

`spec.key` is the gate name your PromotionStrategy checks in `proposedCommitStatuses`. It is required and must match a
key declared in that PromotionStrategy's `proposedCommitStatuses`, so the gate this controller produces is actually
enforced. A common value is `dependents-successful`.

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
- `.DependsOn` — the current environment's immediate upstream branches (one edge away), from `spec.environments[].dependsOn`
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
  environments:
    - branch: environment/dev
    - branch: environment/staging
      dependsOn:
        - environment/dev
  url:
    template: "https://promoter.example.com/promotion-strategies/{{ .PromotionStrategy.Name }}?env={{ urlQueryEscape .Environment }}"
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

The DependentsSuccessfulCommitStatus only *produces* the gate; the PromotionStrategy must *consume* it. Add the same
`key` to the PromotionStrategy's `proposedCommitStatuses` so every environment gates on it. Declaring the key globally
is the usual pattern; you may also add it per environment when only some branches should enforce ordering.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  proposedCommitStatuses:
    - key: dependents-successful  # same as DependentsSuccessfulCommitStatus.spec.key
  environments:
    - branch: environment/dev
    - branch: environment/e2e
    - branch: environment/perf
    - branch: environment/prod
  gitRepositoryRef:
    name: dag-example-apps
```

> [!IMPORTANT]
> As a safety check, the PromotionStrategy controller fails its reconcile when no
> `DependentsSuccessfulCommitStatus` targets the PromotionStrategy, or when a gate references the PromotionStrategy but
> its `key` is missing from the effective `proposedCommitStatuses` for one or more environment branches (global plus
> per-environment selectors, matching what each `ChangeTransferPolicy` enforces). This safety check is intended to be
> removed in v1.0; see [Roadmap](../../roadmap.md).
