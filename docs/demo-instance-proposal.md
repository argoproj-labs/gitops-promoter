# Demo Instance Proposal

This document proposes the design and implementation plan for a publicly accessible demo instance of GitOps Promoter.

## Goals

The demo instance has the following goals:

1. **Showcase the product.** Give prospective users a live, interactive look at GitOps Promoter and its Argo CD
   extension UI without requiring any local setup.
2. **Dogfood our own tools.** The demo should consume GitOps Promoter the same way a real user would: using the Helm
   chart, committing to a real repository, and relying on all the built-in automation.
3. **Demonstrate breadth of features.** The demo should show off as many capabilities as practical: the Argo CD
   extension, Argo CD commit-status controllers, timed commit-status controllers, git-commit commit-status controllers,
   and the promotion gating system.
4. **Stay up to date automatically.** GitOps Promoter and Argo CD should be updated automatically via Renovate or
   Dependabot, with no manual maintenance required.
5. **Run continuously.** A synthetic promotion cycle should fire at least every ten minutes so the UI is never stale.
6. **Be read-only for the public.** Cluster and Git write access must be limited to automation; the public can only
   observe.

---

## Desired Features

### 1. Publicly Accessible Argo CD UI with Extension

A real Argo CD instance (not a mocked or embedded viewer) should run alongside GitOps Promoter. The Argo CD web UI
should be reachable over HTTPS at a stable domain. The
[GitOps Promoter Argo CD extension](https://github.com/argoproj-labs/gitops-promoter/tree/main/ui/extension) should be
installed so visitors can see the promotion status directly inside the Argo CD application view.

Access controls:
- A read-only guest user should be enabled by default so the UI can be browsed without credentials.
- No Argo CD actions (sync, delete, etc.) should be available to the guest user.

### 2. Live Promotion Cycle

An automated agent (a Kubernetes CronJob) should commit a synthetic change to the DRY branch of the demo Git repository
every ten minutes. GitOps Promoter then opens promotion PRs, runs the configured commit-status checks, and merges them
environment by environment. The result is a continuously moving promotion pipeline that visitors can observe in
real-time.

### 3. Multi-Environment Promotion

The demo repository should define at least three environments, for example `dev`, `staging`, and `production`. Each
environment has its own Staging branch and Live branch. Visitors can see PRs opened and merged in sequence.

### 4. Commit Status Managers

The demo should exercise as many built-in commit-status controllers as possible:

| Commit Status Controller | Purpose in Demo |
|---|---|
| **Argo CD** | Gates promotion on the Argo CD application reaching `Healthy + Synced` in the previous environment. |
| **Timed** | Enforces a mandatory wait (e.g., five minutes) between environment promotions. |
| **Git Commit** | Verifies that specific files are present in the hydrated commit before allowing promotion. |

### 5. Helm Chart Deployment

Both GitOps Promoter and Argo CD should be installed via their respective Helm charts. Chart values should be stored in
the infrastructure repository (see below) so that all configuration is version-controlled and auditable.

### 6. Automated Dependency Updates

[Renovate](https://docs.renovatebot.com/) should be configured to watch the infrastructure repository and
automatically open pull requests when new versions of the following are released:

- GitOps Promoter Helm chart
- Argo CD Helm chart
- Any container images pinned in the CronJob or other workloads

Renovate PRs should be automatically merged once all status checks pass (using Renovate's `automerge` feature) to keep
the instance fully hands-off.

### 7. Infrastructure as Code

All cluster resources (Namespaces, Argo CD Applications, Argo CD AppProjects, GitOps Promoter CRs, RBAC, Ingresses,
etc.) should be defined declaratively. Where possible, Argo CD itself should manage these resources using the
[App of Apps](https://argo-cd.readthedocs.io/en/stable/operator-manual/cluster-bootstrapping/) pattern, so that the
cluster self-heals if resources drift.

---

## Implementation Plan

### Phase 1 — Infrastructure Repository

Create a new public GitHub repository (suggested name: `gitops-promoter-demo`) under the `argoproj-labs` organisation.
This repository contains:

```
gitops-promoter-demo/
├── apps/                    # Argo CD Application manifests (App of Apps)
│   ├── root-app.yaml        # Root Application pointing at this directory
│   ├── argocd.yaml          # Argo CD self-managed Application
│   ├── gitops-promoter.yaml # GitOps Promoter Application
│   ├── demo-config.yaml     # Demo workloads & GitOps Promoter CRs
│   └── cronjob.yaml         # Synthetic commit CronJob Application
├── charts/                  # Helm values overrides
│   ├── argocd/
│   │   └── values.yaml
│   └── gitops-promoter/
│       └── values.yaml
├── manifests/               # Plain Kubernetes manifests not managed by Helm
│   ├── namespaces.yaml
│   ├── rbac.yaml
│   └── cronjob/
│       └── cronjob.yaml
├── promoter-config/         # GitOps Promoter CRDs (GitRepository, PromotionStrategy, etc.)
│   ├── scm-provider.yaml
│   ├── git-repository.yaml
│   ├── promotion-strategy.yaml
│   └── commit-statuses/
│       ├── argocd-commit-status.yaml
│       ├── timed-commit-status.yaml
│       └── git-commit-status.yaml
├── demo-dry/                # (or a separate repo) DRY branch source configs
│   └── app/
│       └── Chart.yaml       # Trivial Helm chart representing the demo app
├── renovate.json5
└── README.md
```

The `demo-dry` contents can live in a dedicated branch (`main`) of a separate repository
(`gitops-promoter-demo-config`) so that the DRY branch, staging branches, and live branches are all in one place,
keeping the cluster config separate from the workload config.

### Phase 2 — Kubernetes Cluster

Provision a small, cost-efficient Kubernetes cluster. Suggested options:

| Provider | Recommended Tier |
|---|---|
| AWS EKS | `t3.medium` × 2 nodes |
| GCP GKE Autopilot | — |
| Hetzner Cloud + k3s | `CX21` × 3 nodes (lowest cost) |

Cluster provisioning should be automated with Terraform or Pulumi, stored in the infrastructure repository under an
`infra/` directory. This makes the cluster fully re-creatable if it needs to be torn down.

A wildcard TLS certificate (e.g., via cert-manager + Let's Encrypt) should be issued so both the Argo CD UI and any
demo webhook receivers get HTTPS automatically.

### Phase 3 — Bootstrap Argo CD

Install Argo CD into the cluster using its Helm chart:

```bash
helm upgrade --install argocd argo/argo-cd \
  --namespace argocd --create-namespace \
  -f charts/argocd/values.yaml
```

Key values to set in `charts/argocd/values.yaml`:

```yaml
server:
  extraArgs:
    - --insecure   # TLS handled by ingress
  ingress:
    enabled: true
    hostname: demo.gitops-promoter.io
    ingressClassName: nginx
    tls: true
configs:
  params:
    server.disable.auth: false
  rbac:
    policy.default: role:readonly  # public gets read-only
  cm:
    extension.config: |
      extensions:
        - name: gitops-promoter
          backend:
            services:
              - url: http://gitops-promoter-ui.gitops-promoter.svc.cluster.local
```

Install the GitOps Promoter Argo CD extension. The extension sidecar or backend service should be deployed alongside
Argo CD.

### Phase 4 — Install GitOps Promoter

Create the Argo CD Application that manages GitOps Promoter via its Helm chart:

```yaml
# apps/gitops-promoter.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitops-promoter
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://argoproj-labs.github.io/gitops-promoter
    chart: gitops-promoter
    targetRevision: "0.22.6"   # kept up to date by Renovate
    helm:
      valueFiles:
        - $values/charts/gitops-promoter/values.yaml
  destination:
    server: https://kubernetes.default.svc
    namespace: gitops-promoter
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

Key values in `charts/gitops-promoter/values.yaml`:

```yaml
controllerConfiguration:
  promotionStrategyRequeueDuration: 10s
  changeTransferPolicyRequeueDuration: 10s
webhookReceiver:
  ingress:
    enabled: true
    hostname: promoter-webhook.gitops-promoter.io
```

Store SCM credentials (GitHub App private key, App ID, installation ID) in a Kubernetes Secret created out-of-band
(e.g., via Sealed Secrets or External Secrets Operator) so they are never committed in plaintext.

### Phase 5 — Demo Workload Repository

Create `gitops-promoter-demo-config` as a second repository. This repository uses a simple Helm chart as its DRY
source:

```
gitops-promoter-demo-config/  (main branch — DRY)
└── app/
    ├── Chart.yaml
    ├── values.yaml          # contains e.g. image.tag: "1.0.0"
    └── templates/
        └── deployment.yaml  # trivial nginx or similar
```

Branches in this repository:

| Branch | Purpose |
|---|---|
| `main` | DRY branch — source of truth for all environments |
| `env/dev/next` | Dev staging branch (hydrated by Argo CD Source Hydrator) |
| `env/dev/live` | Dev live branch (Argo CD syncs from here) |
| `env/staging/next` | Staging staging branch |
| `env/staging/live` | Staging live branch |
| `env/production/next` | Production staging branch |
| `env/production/live` | Production live branch |

The [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/) is configured for
each environment to produce hydrated commits from `main` → `env/*/next`.

### Phase 6 — GitOps Promoter Configuration

Define the core GitOps Promoter CRDs in `promoter-config/`:

**`git-repository.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: demo-config
  namespace: gitops-promoter
spec:
  github:
    appID: "<app-id>"
    installationID: "<installation-id>"
    owner: argoproj-labs
    name: gitops-promoter-demo-config
  secretRef:
    name: github-app-credentials
```

**`promotion-strategy.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
  namespace: gitops-promoter
spec:
  gitRepositoryRef:
    name: demo-config
  environments:
    - branch: env/dev/live
      autoMerge: true
    - branch: env/staging/live
      autoMerge: true
      activeCommitStatuses:
        - key: argocd-dev-healthy
        - key: timed-5m
    - branch: env/production/live
      autoMerge: true
      activeCommitStatuses:
        - key: argocd-staging-healthy
        - key: timed-5m
        - key: git-commit-check
```

**`commit-statuses/argocd-commit-status.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: argocd-dev-healthy
  namespace: gitops-promoter
spec:
  promotionStrategyRef:
    name: demo
  environmentBranch: env/dev/live
  argocdApp:
    name: demo-dev
    namespace: argocd
```

**`commit-statuses/timed-commit-status.yaml`** — uses the built-in `TimedCommitStatus` to require a five-minute soak
before promoting to the next environment.

**`commit-statuses/git-commit-status.yaml`** — uses the built-in `GitCommitStatus` to verify that the hydrator placed
the expected `hydrator.metadata` file in the hydrated commit.

### Phase 7 — Synthetic Commit CronJob

A Kubernetes CronJob runs every ten minutes and makes a trivial change to the DRY branch to trigger a new promotion
cycle:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: demo-promoter
  namespace: gitops-promoter
spec:
  schedule: "*/10 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: demo-promoter
          containers:
            - name: git
              image: alpine/git:2.45.2   # kept up to date by Renovate
              command:
                - /bin/sh
                - -c
                - |
                  git clone https://x-access-token:$(GITHUB_TOKEN)@github.com/argoproj-labs/gitops-promoter-demo-config /work
                  cd /work
                  git config user.email "demo-bot@gitops-promoter.io"
                  git config user.name "Demo Bot"
                  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > timestamp.txt
                  git add timestamp.txt
                  git commit -m "chore: automated demo cycle $(date -u +%Y-%m-%dT%H:%M:%SZ)"
                  git push origin main
              env:
                - name: GITHUB_TOKEN
                  valueFrom:
                    secretKeyRef:
                      name: github-app-credentials
                      key: token
          restartPolicy: OnFailure
```

> [!NOTE]
> The CronJob uses a short-lived GitHub App installation token (generated by a small init container or a token
> vending sidecar) rather than a long-lived personal access token.

### Phase 8 — Renovate Configuration

Add `renovate.json5` to the infrastructure repository:

```json5
{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": ["config:recommended"],
  "automerge": true,
  "automergeType": "pr",
  "packageRules": [
    {
      "matchManagers": ["helmv3"],
      "matchPackageNames": ["gitops-promoter", "argo-cd"],
      "automerge": true
    },
    {
      "matchManagers": ["kubernetes"],
      "matchPackageNames": ["alpine/git"],
      "automerge": true
    }
  ]
}
```

Enable the [Renovate GitHub App](https://github.com/apps/renovate) on the infrastructure repository.

### Phase 9 — Webhook Integration

Configure a GitHub App webhook pointing at the GitOps Promoter webhook receiver Ingress
(`https://promoter-webhook.gitops-promoter.io/`). This ensures promotion PRs are opened within seconds of a new
hydrated commit arriving, rather than waiting for the next reconcile loop.

### Phase 10 — Monitoring and Alerting

Deploy the GitOps Promoter Prometheus metrics endpoint and configure a lightweight alerting rule (e.g., via the
Prometheus Alertmanager or a free tier of Grafana Cloud) to page the maintainers if:

- The cluster becomes unreachable.
- No promotion cycle completes within 30 minutes (indicating the CronJob or hydrator is broken).
- Argo CD application health degrades.

---

## Security Considerations

| Risk | Mitigation |
|---|---|
| Public users performing destructive Argo CD actions | `role:readonly` is set as the Argo CD default policy; no write permissions are granted to unauthenticated users. |
| Exposed Git credentials in CronJob | Credentials are stored in Kubernetes Secrets (managed via Sealed Secrets or External Secrets Operator) and never committed to the repository. |
| Cluster over-privilege | The CronJob ServiceAccount is limited to the `gitops-promoter` namespace with minimal RBAC. |
| Unintended public writes to demo config repo | The GitHub App is scoped to the demo config repository only, and only the CronJob automation uses it for commits. |

---

## Cost Estimate

Running the demo on a small Hetzner Cloud k3s cluster is estimated at approximately **€15–30 per month** for three
`CX21` nodes (2 vCPU, 4 GB RAM each). Larger managed Kubernetes options (EKS, GKE) will cost more but reduce
operational overhead.

---

## Summary of Repositories

| Repository | Purpose |
|---|---|
| `argoproj-labs/gitops-promoter-demo` | Infrastructure: cluster IaC, Argo CD apps, Helm values, GitOps Promoter CRs, Renovate config |
| `argoproj-labs/gitops-promoter-demo-config` | Workload config: DRY branch (`main`), staging branches, live branches |

---

## Open Questions

1. **Domain name.** Should the demo live at `demo.gitops-promoter.io` or under the `argoproj-labs` DNS? Who controls
   DNS for the project?
2. **Cloud provider.** Should we prefer a provider that existing maintainers already have credits or accounts with?
3. **GitHub App ownership.** Should the GitHub App be owned by the `argoproj-labs` organisation or a dedicated bot
   account?
4. **Secrets management backend.** Is there a preferred tool for secrets in `argoproj-labs` clusters (Sealed Secrets,
   External Secrets, Vault)?
5. **Access for maintainers.** How many maintainers need cluster admin access, and how will credentials be distributed?
