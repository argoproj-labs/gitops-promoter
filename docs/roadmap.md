# Roadmap

## Planned features

### Promotion strategy and pipelines

- **DAG-style promotion strategies** — Model promotion flows beyond a linear environment chain, including environment dependencies and parallel branch targets within a step ([#1364](https://github.com/argoproj-labs/gitops-promoter/issues/1364)).

### Monorepo and branch management

- **[Shared active branch across PromotionStrategies](https://github.com/argoproj-labs/gitops-promoter/issues/1336)** — Use one live branch per environment when multiple promotion pipelines share the same monorepo branches.

### Commit status and gating

- **Deployment window gates** — Gate promotions on allowed time-of-day or calendar windows.
- **[Self-registering commit status controllers](https://github.com/argoproj-labs/gitops-promoter/issues/1154)** — Register commit status keys with Promoter without listing them on every PromotionStrategy.

### SCM providers and integrations

- **Additional SCM providers** — Integrate providers beyond those supported today.
- **[Bitbucket Server and Data Center](https://github.com/argoproj-labs/gitops-promoter/issues/1243)** — Support on-prem Bitbucket for repositories, pull requests, and commit statuses.
- **[SCM webhooks for pull request changes](https://github.com/argoproj-labs/gitops-promoter/issues/360)** — Reconcile promotion state when the SCM signals PR updates; extend to multiple provider types ([#222](https://github.com/argoproj-labs/gitops-promoter/issues/222)).
- **[Verified GitHub webhook signatures](https://github.com/argoproj-labs/gitops-promoter/issues/1285)** — Validate `X-Hub-Signature-256` using constant-time comparison.

### Operations, scaling, and observability

- **Multiple instances per cluster** — Run separate Promoter deployments for scale, blast-radius isolation, or tenancy.
- **[Namespaced operation mode](https://github.com/argoproj-labs/gitops-promoter/issues/310)** — Restrict watches and permissions to configured namespaces.
- **[DORA metrics](https://github.com/argoproj-labs/gitops-promoter/issues/574)** — Export deployment frequency, lead time, and related delivery metrics.
- **[SLSA release provenance](https://github.com/argoproj-labs/gitops-promoter/issues/1445)** — Publish SLSA attestations for release binaries and images.

## v1.0 expected breaking changes

The project is still **experimental**; v1.0 will mark a stabilized API. The items below are expected or possible before then.

### `spec.key` may become required (planned for v1.0)

Today, **ArgoCDCommitStatus** and **TimedCommitStatus** expose an optional `spec.key` with CRD defaults (`argocd-health` and `timer`). **WebRequestCommitStatus** and **GitCommitStatus** already require `spec.key`.

For v1.0, we may make `spec.key` **required** on ArgoCDCommitStatus and TimedCommitStatus so all built-in gate CRs share the same API shape. The deprecated `CommitStatusKey()` helpers on those spec types (empty-key fallback for pre-upgrade CRDs) will be removed; controllers will use `spec.Key` directly ([#1465](https://github.com/argoproj-labs/gitops-promoter/issues/1465)).

We recommend setting `spec.key` explicitly on ArgoCDCommitStatus and TimedCommitStatus now (use `argocd-health` or `timer` when those are your gate names). That keeps manifests aligned with PromotionStrategy keys and avoids churn if the field becomes required in v1.0:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: argocd-health
  promotionStrategyRef:
    name: webservice-tier-1
  applicationSelector:
    matchLabels:
      app: webservice-tier-1
```

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: timer
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      duration: 1h
```

Use the same key in `PromotionStrategy` `activeCommitStatuses` for ArgoCDCommitStatus, or in `activeCommitStatuses` / `proposedCommitStatuses` for TimedCommitStatus as appropriate.

See also: [Argo CD Commit Status](commit-status-controllers/argocd.md), [Timed Commit Status](commit-status-controllers/timed.md), and [Development Best Practices](commit-status-controllers/development-best-practices.md).

### ArgoCDCommitStatus legacy CommitStatus cleanup (planned for v1.0)

Remove `cleanupLegacyOrphanedCommitStatusesWithoutParentLabel` once clusters are past the Argo CD CommitStatus naming and parent-gate label migration ([#1460](https://github.com/argoproj-labs/gitops-promoter/issues/1460)).
