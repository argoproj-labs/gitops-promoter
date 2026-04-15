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
- `ArgoCDCommitStatus.spec.commitStatusKey` to isolate commit status keys per app
- a hydrator that writes metadata at `<activePath>/hydrator.metadata` and does not overwrite other app directories

In this pattern, `PromotionStrategy.spec.activePath` and `ApplicationSet.spec.template.spec.source.path` should point to
the same app directory so the hydrator writes and Argo CD reads from the same location.

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
  commitStatusKey: argocd-health-payments
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
            branch: environment/dev
          - env: test
            branch: environment/test
          - env: prod
            branch: environment/prod
  template:
    metadata:
      name: "payments-{{env}}"
      labels:
        app.kubernetes.io/name: payments
    spec:
      source:
        repoURL: https://github.com/example/platform-config
        targetRevision: "{{branch}}"
        path: apps/payments
      destination:
        server: https://kubernetes.default.svc
        namespace: payments-{{env}}
```
