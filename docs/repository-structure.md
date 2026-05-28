# Repository Structure

This page explains how to structure your Git repository when using GitOps Promoter.

## Read/Write Interface: DRY Branch

The DRY branch (for example `main`) is the **only** branch that users should edit directly.

- Define all applications and all environments in this branch.
- Commit your desired config changes here.
- Let your hydrator and GitOps Promoter produce and promote environment-ready commits from this source of truth.

All other branches used by promotion are automatically generated and reconciled. They are part of the controller-driven
workflow, not the user authoring workflow.

This does **not** conflict with the "don't manually manage branch-per-environment content" best practice: users only
author in one DRY branch, while environment branches are machine-managed read interfaces.

## Read Interface: Environment Branches

Environment and proposed branches are read-only operational outputs. Use them to inspect what is deployed or about to be
deployed, but do not edit them manually.

### One branch per app/environment

In this mode, each app/environment pair gets its own active branch and proposed branch:

- Active: `environment/dev-my-app`
- Proposed: `environment/dev-my-app-next`

This is simple and explicit, and works well for small repos.

### One branch per environment (recommended for large monorepos)

In large monorepos, many app-specific active branches can become hard to navigate. A shared active branch per
environment reduces branch clutter and makes it easier to answer: "what is live in dev/test/prod right now?"

Use:

- `PromotionStrategy.spec.activePath` to scope each strategy to an app directory
- `ArgoCDCommitStatus.spec.key` (and `TimedCommitStatus.spec.key`) to give each app a distinct commit status key — required to avoid key collisions when multiple apps share the same active branch
- a hydrator that writes `<activePath>/hydrator.metadata` with the run's `drySha` and does not overwrite other
  app directories. A repository-root `hydrator.metadata` is optional in this mode (the Argo CD source hydrator
  writes one by default; GitOps Promoter only reads the path-scoped file when `activePath` is set)

In this pattern, `PromotionStrategy.spec.activePath`, `sourceHydrator.drySource.path`, and
`sourceHydrator.syncSource.path` should point to the same app directory so the hydrator writes and Argo CD reads from
the same location.

#### Constraints when multiple PromotionStrategies share an active branch

Two configuration constraints apply whenever more than one `PromotionStrategy` targets the same active branch.
Neither is enforced by API-level validation today, so treat them as authoring rules until a webhook check catches up.

1. **All-or-nothing on `activePath`.** Either every `PromotionStrategy` on the branch uses `activePath`, or none do.
   Mixing modes is silently unsafe: a default-mode PS resolves promotion conflicts with git's whole-tree `-s ours`
   strategy, which discards active's tree entirely on the merge result. The next promotion of that PS therefore
   wipes any other PS's app subtree off the active branch, even if its own hydrator was perfectly well-behaved
   inside its own directory.

2. **`activePath`s must be sibling-disjoint.** No `activePath` may be a prefix of another (`apps` and `apps/app-b`
   are nested), and no two `PromotionStrategy` resources on the same active branch may share an identical
   `activePath`.

   - Nesting is detected loudly by git itself at first hydrator push: the proposed-branch naming convention
     `<active>-next/<activePath>` puts the two branches in a parent/child relationship on `refs/heads/`, and git
     refuses to coexist with `fatal: cannot lock ref 'refs/heads/<...>': '<parent>' exists`. Even if git did allow
     it, the outer PS's path-scoped merge restores its whole `<activePath>` subtree from its own proposed branch,
     which would silently overwrite the inner PS's directory.
   - Duplicate `activePath`s are not detected by git: both `PromotionStrategy` resources would create distinct
     `ChangeTransferPolicy` resources but share a single proposed branch and race destructively on it. Give each
     PS a unique sibling path under a common parent (e.g. `apps/app-one`, `apps/app-two`).

Example (dev/test/prod, simple list generator):

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: payments
spec:
  gitRepositoryRef:
    name: platform-config
  activePath: apps/payments
  activeCommitStatuses:
    - key: argocd-health-payments
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: payments
spec:
  promotionStrategyRef:
    name: payments
  key: argocd-health-payments  # must be unique across all apps sharing the same active branch
  applicationSelector:
    matchLabels:
      app.kubernetes.io/name: payments
---
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: payments
spec:
  generators:
    - list:
        elements:
          - env: dev
            syncBranch: environment/dev
            hydrateToBranch: environment/dev-next/apps/payments
          - env: test
            syncBranch: environment/test
            hydrateToBranch: environment/test-next/apps/payments
          - env: prod
            syncBranch: environment/prod
            hydrateToBranch: environment/prod-next/apps/payments
  template:
    metadata:
      name: "payments-{{env}}"
      labels:
        app.kubernetes.io/name: payments
    spec:
      sourceHydrator:
        drySource:
          repoURL: https://github.com/example/platform-config
          targetRevision: HEAD
          path: apps/payments
        hydrateTo:
          targetBranch: "{{hydrateToBranch}}"
        syncSource:
          targetBranch: "{{syncBranch}}"
          path: apps/payments
      destination:
        server: https://kubernetes.default.svc
        namespace: payments-{{env}}
```
