# GitOps Promoter

[![codecov](https://codecov.io/gh/argoproj-labs/gitops-promoter/graph/badge.svg?token=Nbye3NDioO)](https://codecov.io/gh/argoproj-labs/gitops-promoter)

GitOps Promoter automates environment promotion for config managed via GitOps, enabling drift-free deployments through automated Pull Requests.

![Video of the GitOps Promoter UI as a change is promoted through three environments](https://github.com/user-attachments/assets/5860cc7a-56e6-4003-b1fc-b33e4d69d411)

## Overview

GitOps Promoter takes a unique approach to environment promotion by operating on hydrated, environment-specific manifests rather than modifying your DRY (Don't Repeat Yourself) source files. When you make a change to your config, GitOps Promoter automatically opens Pull Requests for each environment. As promotion gates pass, PRs are automatically merged, moving your changes through environments safely and transparently.

## Key Features

* **Drift-free promotion** - All promotion state is visible in git branches and PRs, eliminating hidden state
* **Robust promotion gating** - Use commit status checks, branch protection rules, and custom controllers to gate promotions
* **Complete SCM integration** - Leverages native git and SCM tooling
* **No automated DRY branch changes** - Never touches user-facing config files, avoiding fragile automated modifications
* **Argo CD integration** - Built-in health status monitoring, UI extension, and deep links
* **Web UI & CLI** - Visual dashboard for monitoring promotion status across environments
* **Flexible gating options** - Built-in controllers for health checks and soak time, plus custom controller support

## How It Works

GitOps Promoter uses a simple but powerful workflow:

1. **You push once** to a DRY branch containing config for all environments
2. **A hydrator** (like [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/)) transforms and pushes environment-specific config to staging branches (`environment/dev-next`, `environment/staging-next`, etc.)
3. **GitOps Promoter** automatically opens/updates Pull Requests from staging branches to live branches
4. **Promotion gates** (commit status checks, branch protection) determine when PRs auto-merge
5. **Your GitOps tool** (Argo CD, Flux, etc.) syncs the merged changes to your clusters

### Architecture

When a commit is made to the DRY branch, the hydrator pushes hydrated manifests to environment staging branches. GitOps Promoter manages PRs from these staging branches to the corresponding live branches, merging them as gates pass.

![GitOps Promoter Architecture](docs/assets/architecture.png)

For a detailed architecture explanation, see [Architecture Documentation](docs/architecture.md).

## Quick Start

### Prerequisites

* Kubernetes cluster (1.21+)
* kubectl CLI
* SCM provider: GitHub, GitHub Enterprise, GitLab, or Forgejo/Codeberg
* A hydrator system (e.g., [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/))

### Installation

Install GitOps Promoter with kubectl:

```bash
kubectl apply -f https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.18.2/install.yaml
```

### Configuration

1. **Create an SCM credential secret** with your GitHub App private key, GitLab token, or Forgejo token:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-scm-secret
  namespace: default
type: Opaque
stringData:
  githubAppPrivateKey: |
    -----BEGIN RSA PRIVATE KEY-----
    ...
    -----END RSA PRIVATE KEY-----
```

2. **Create an ScmProvider** to configure access to your SCM:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: my-scm-provider
  namespace: default
spec:
  secretRef:
    name: my-scm-secret
  github:
    appID: "123456"
    installationID: "789012"
```

3. **Create a GitRepository** referencing your repo:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: my-repo
  namespace: default
spec:
  github:
    owner: my-org
    name: my-repo
  scmProviderRef:
    name: my-scm-provider
```

4. **Create a PromotionStrategy** to define your environments and gates:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app-promotion
  namespace: default
spec:
  gitRepositoryRef:
    name: my-repo
  activeCommitStatuses:
    - key: argocd-health
  environments:
    - branch: environment/dev
    - branch: environment/staging
      activeCommitStatuses:
        - key: timer  # soak time requirement
    - branch: environment/production
      autoMerge: false  # require manual approval
```

**Important**: Your hydrator must push to branches with `-next` suffix (e.g., `environment/dev-next`) while your `PromotionStrategy` references the live branches (e.g., `environment/dev`).

For complete setup instructions including GitHub App permissions, webhooks, and SCM-specific configuration, see the [Getting Started Guide](https://gitops-promoter.readthedocs.io/en/latest/getting-started/).

## Key Concepts

* **DRY Branch** - Git branch containing config for all environments (e.g., `main`)
* **Staging Branches** - Environment-specific branches with hydrated config awaiting promotion (e.g., `environment/dev-next`)
* **Live Branches** - Environment-specific branches with config currently deployed (e.g., `environment/dev`)
* **Promotion PRs** - Automated PRs from staging to live branches, one per environment
* **Hydrator** - Tool that transforms DRY config into environment-specific hydrated config
* **Commit Status** - Kubernetes resource representing a check that gates promotion
* **Active Commit Status** - Check that must pass on the live environment before promoting to next environment
* **Proposed Commit Status** - Check that must pass on staged changes before merging to live

## Promotion Gating

GitOps Promoter supports flexible promotion gating through:

### Built-in Controllers

* **[ArgoCD Health Status](docs/commit-status-controllers/argocd.md)** - Gates promotion based on Argo CD Application health
* **[Timed Commit Status](docs/commit-status-controllers/timed.md)** - Enforces soak/bake time requirements

### Branch Protection Rules

Use native SCM branch protection (required reviewers, status checks, etc.)

### Custom Controllers

Write your own Kubernetes controllers to create `CommitStatus` resources based on any criteria (security scans, performance tests, deployment freezes, etc.)

See [Gating Promotions](https://gitops-promoter.readthedocs.io/en/latest/gating-promotions/) for detailed examples.

## Web UI & CLI

GitOps Promoter includes a web dashboard for visualizing promotion status:

```bash
# Download the CLI from releases page
# https://github.com/argoproj-labs/gitops-promoter/releases

gitops-promoter dashboard
# Opens UI at http://localhost:8080

# Use custom port or kubeconfig context
gitops-promoter dashboard --port 8081 --kubecontext my-cluster
```

## SCM Support

GitOps Promoter supports multiple SCM providers:

* **GitHub** - Including GitHub Enterprise (via GitHub App)
* **GitLab** - Self-hosted and GitLab.com (via Access Token)
* **Forgejo** - Including Codeberg (via Access Token or Basic Auth)

We welcome contributions to add support for additional providers!

## Argo CD Integration

GitOps Promoter provides first-class Argo CD integration:

* **[UI Extension](docs/argocd-integrations.md#ui-extension)** - Custom resource views in Argo CD UI
* **[Deep Links](docs/argocd-integrations.md#deep-links)** - Direct links from Argo CD to PRs
* **[Commit Status Labels](docs/argocd-integrations.md#commit-status-keys-in-resource-tree)** - Show status keys in resource tree
* **[Health Monitoring](docs/commit-status-controllers/argocd.md)** - Automatic health-based promotion gating

## Documentation

* [Full Documentation](https://gitops-promoter.readthedocs.io/)
* [Getting Started](https://gitops-promoter.readthedocs.io/en/latest/getting-started/)
* [Architecture](docs/architecture.md)
* [CRD Specifications](docs/crd-specs.md)
* [Gating Promotions](docs/gating-promotions.md)
* [Multi-Tenancy](docs/multi-tenancy.md)
* [Tool Comparison](docs/tool-comparison.md)
* [FAQs](docs/faqs.md)
* [Monitoring](docs/monitoring/)

## Videos & Presentations

* [Space Age GitOps: The Rise of the Humble Pull Request](https://www.youtube.com/watch?v=p5EPKY3vM-E) - Core concepts and philosophy
* [Space Age GitOps: Lifting off with Argo Promotions](https://www.youtube.com/watch?v=2JmLCqM1nTM) - Live demo
* [No More Pipelines: Reconciling Environment Promotion Via Commit Statuses](https://www.youtube.com/watch?v=Usi38ly1pe0) - Deep dive on promotion gating

## Example

Here's a complete example showing a promotion strategy with multiple environments and gates:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: example-promotion-strategy
  namespace: default
spec:
  gitRepositoryRef:
    name: example-git-repo
  activeCommitStatuses:
    - key: argocd-health  # All environments must be healthy
  environments:
    - branch: environment/dev
      # Dev auto-merges once health check passes
    
    - branch: environment/staging
      activeCommitStatuses:
        - key: timer  # Staging requires 30min soak time
      proposedCommitStatuses:
        - key: security-scan  # Must pass security scan before merging
    
    - branch: environment/production
      autoMerge: false  # Production requires manual approval
      activeCommitStatuses:
        - key: performance-test  # Must pass performance tests
      proposedCommitStatuses:
        - key: deployment-freeze  # Check for deployment freeze windows
```

## Community & Contributing

* **Issues**: [GitHub Issues](https://github.com/argoproj-labs/gitops-promoter/issues)
* **Discussions**: [GitHub Discussions](https://github.com/argoproj-labs/gitops-promoter/discussions)
* **Slack**: [#gitops-promoter on CNCF Slack](https://argoproj.github.io/community/join-slack/)

We welcome contributions! Whether it's:
* Adding support for new SCM providers
* Building custom commit status controllers
* Improving documentation
* Reporting bugs or suggesting features

## Status

GitOps Promoter is currently in **alpha** status as part of Argoproj Labs. While it's being used in production environments, the API may change. Please use with appropriate caution and testing.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.

## Who's Using GitOps Promoter?

See [USERS.md](USERS.md) for organizations using GitOps Promoter in production.
