# Upgrading

This page documents breaking changes and migration steps between releases.

## 0.38 — Promotion ordering gate (`DependentsSuccessfulCommitStatus`) {#038-promotion-ordering-gate}

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
You may delete orphaned previous-environment CommitStatuses once promotions are healthy on the new gate.

### Multi-install deployments

Label each new `DependentsSuccessfulCommitStatus` with the same `promoter.argoproj.io/instance-id` as its
`PromotionStrategy`. Gate CRs propagate `instance-id` to their `CommitStatus` children; `PromotionStrategy` does not
create ordering CommitStatuses directly. See [Multiple Controller Installs](multi-install.md#write-path-label-propagation).

### Further reading

- [Gating Promotions — Environment Ordering](gating-promotions/index.md#environment-ordering)
- [Dependents Successful Commit Status](gating-promotions/built-in-gates/dependents-successful-commit-status.md)
- [Architecture — Embedding vs. Decoupling Promotion Order](architecture.md#embedding-vs-decoupling-promotion-order)
