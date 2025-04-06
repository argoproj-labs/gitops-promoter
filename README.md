[![codecov](https://codecov.io/gh/argoproj-labs/gitops-promoter/graph/badge.svg?token=Nbye3NDioO)](https://codecov.io/gh/argoproj-labs/gitops-promoter)

# GitOps Promoter

GitOps Promoter facilitates environment promotion for config managed via GitOps.

## Key Features

* Drift-free promotion process
* Robust promotion gating system
* Complete integration with git and SCM tooling
* No fragile automated changes to user-facing files

The main ideas behind the project are explained in ["Space Age GitOps: The Rise of the Humble Pull Request"](https://www.youtube.com/watch?v=p5EPKY3vM-E).

A live demo is presented in ["Space Age GitOps: Lifting off with Argo Promotions"](https://www.youtube.com/watch?v=2JmLCqM1nTM).

## Example

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: example-promotion-strategy
spec:
  gitRepositoryRef:
    name: example-git-repo
  checks:
    - key: argocd-app-health
    - key: security-scan
  environments:
    - branch: environment/dev
    - branch: environment/test
      checks:
        - key: performance-test
    - branch: environment/prod
      autoMerge: false
      checks:
      - key: deployment-freeze
```

## Getting Started

The project is currently experimental, please use with caution. See the 
[docs site](https://argo-gitops-promoter.readthedocs.io/en/latest/getting-started/) for setup instructions.
