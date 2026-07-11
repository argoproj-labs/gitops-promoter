# DAG Commit Status Controller

The DAG Commit Status controller gates promotions based on a **dependency graph** between
environments. Each environment declares the upstream environments it `dependsOn`, and an
environment becomes eligible for promotion only once **all** of its upstreams have promoted the
same dry commit and are healthy. This generalizes the usual linear pipeline (dev → staging → prod)
to arbitrary directed acyclic graphs, so you can express fan-out and fan-in (for example
`dev → {e2e, perf} → prod`).

The controller reads the referenced PromotionStrategy's environment status, evaluates the graph,
and writes a per-environment `CommitStatus` (the gate) that reports whether that environment's
upstream dependencies are satisfied.

> [!IMPORTANT]
> The gate is not created or injected automatically. You must create a DAGCommitStatus (or a
> [PreviousEnvironmentCommitStatus](previous-environment-commit-status.md), which generates one for
> the linear case) for each PromotionStrategy you want to gate, and add its `key` to that
> PromotionStrategy's global `proposedCommitStatuses`. See [Wiring the gate into the
> PromotionStrategy](#wiring-the-gate-into-the-promotionstrategy) below.

## Example Configuration

A diamond graph — `dev` fans out to `e2e` and `perf`, which fan back in to `prod`:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: DAGCommitStatus
metadata:
  name: demo-dag
spec:
  key: promoter-dag
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

Declares the promotion dependency graph. Each entry names an environment `branch` and the upstream
`dependsOn` branches it waits on. An entry with no `dependsOn` is a graph root (for example `dev`
above). Every `branch` must match a branch declared in the referenced PromotionStrategy's
`environments`. The graph must be acyclic; cycles and references to unknown branches are rejected.

### `spec.key`

`spec.key` is the gate name your PromotionStrategy checks in `proposedCommitStatuses`. It is
required and must match a key declared in that PromotionStrategy's `proposedCommitStatuses`, so the
gate this controller produces is actually enforced. A common value is `promoter-dag`.

## Wiring the gate into the PromotionStrategy

The DAGCommitStatus only *produces* the gate; the PromotionStrategy must *consume* it. Add the same
`key` to the PromotionStrategy's global `proposedCommitStatuses` so every environment gates on it:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-dag
spec:
  proposedCommitStatuses:
    - key: promoter-dag  # same as DAGCommitStatus.spec.key
  environments:
    - branch: environment/dev
    - branch: environment/e2e
    - branch: environment/perf
    - branch: environment/prod
  gitRepositoryRef:
    name: dag-example-apps
```

> [!IMPORTANT]
> As a safety check, the PromotionStrategy controller fails its reconcile if a DAGCommitStatus
> references the PromotionStrategy but its `key` is not present in the PromotionStrategy's
> `proposedCommitStatuses` — otherwise the gate it produces would never be enforced. This safety
> check is intended to be removed in v1.0; see [Roadmap](../../roadmap.md).
