[![codecov](https://codecov.io/gh/argoproj-labs/gitops-promoter/graph/badge.svg?token=Nbye3NDioO)](https://codecov.io/gh/argoproj-labs/gitops-promoter)

# GitOps Promoter

GitOps Promoter facilitates environment promotion for config managed via GitOps.

![Video of the GitOps Promoter UI as a change is promoted through three environments](https://github.com/user-attachments/assets/5860cc7a-56e6-4003-b1fc-b33e4d69d411)

## Key Features

* Drift-free promotion process
* Robust promotion gating system
* Complete integration with git and SCM tooling
* No fragile automated changes to user-facing files

The main ideas behind the project are explained in ["Space Age GitOps: The Rise of the Humble Pull Request"](https://www.youtube.com/watch?v=p5EPKY3vM-E).

A live demo is presented in ["Space Age GitOps: Lifting off with Argo Promotions"](https://www.youtube.com/watch?v=2JmLCqM1nTM).

The promotion gating system is detailed in ["No More Pipelines: Reconciling Environment Promotion Via Commit Statuses"](https://www.youtube.com/watch?v=Usi38ly1pe0).

## Example

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: example-promotion-strategy
spec:
  gitRepositoryRef:
    name: example-git-repo
  activeCommitStatuses:
    - key: argocd-app-health
  proposedCommitStatuses:
    - key: security-scan
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
      autoMerge: false
      activeCommitStatuses:
      - key: performance-test
      proposedCommitStatuses:
      - key: deployment-freeze
```

## Getting Started

The project is currently experimental, please use with caution. See the 
[docs site](https://gitops-promoter.readthedocs.io/en/latest/getting-started/) for setup instructions.
