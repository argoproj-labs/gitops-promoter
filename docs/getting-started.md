# Getting Started

This guide will help you get started installing and setting up the GitOps Promoter. We currently only support
GitHub and GitHub Enterprise as the SCM providers. We would welcome any contributions to add support for other
providers.

## Requirements

* kubectl CLI
* kustomize CLI
* kubernetes cluster
* GitHub or GitHub Enterprise Application
  * Will take PRs to add support for other SCM providers

## Installation

To install the GitOps Promoter, you can use the following command:

```bash
kubectl apply -f https://github.com/argoproj-labs/promoter/releases/download/latest/install.yaml
```

## GitHub App Configuration
We will need to configure a GitHub App to allow the GitOps Promoter to interact with your GitHub repository.
To configure a GitHub App, you will need to create an app in your organization. You can follow the
instructions [here](https://docs.github.com/en/developers/apps/creating-a-github-app) to create the Github App.

!!! note We do support configuration of a GitHub App webhook that triggers PR creation upon Push. However, we do not configure
the ingress to allow Github to reach the GitOps Promoter. You will need to configure the ingress to allow GitHub to reach 
the GitOps Promoter via the service [promoter-webhook-receiver]() which listens on port `3333`. If you do not use webhooks 
you might want to adjust the auto reconciliation interval to a lower value using these cli flags `--promotion-strategy-requeue-duration` and
`--change-transfer-policy-requeue-duration`.

During the creation the GitHub App, you will need to configure the following settings:

* Permissions
  * Commit statuses - Read & write
  * Contents - Read & write
  * Pull requests - Read & write
* Webbhook URL (Optional - but highly recommended)
  * `https://<your-promoter-webhook-receiver-service>/`

The GitHub App will generate a private key that you will need to save. You will also need to get the App ID and the
installation ID in a secret as follows:

```yaml
apiVersion: v1
stringData:
  appID: <your-app-id>
  installationID: <your-installation-id>
  privateKey: <your-private-key>
kind: Secret
metadata:
  name: <your-secret-name>
type: Opaque
```

!!! note This secret will need to be installed to the same namespace that you plan on creating PromotionStrategy resources in.



We also need a GitRepository and ScmProvider, which is are custom resources that represents a git repository and a provider. 
Here is an example of both resources:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: <your-scmprovider-name>
spec:
  secretRef:
    name: <your-secret-name>
  github: {}
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: <git-repository-ref-name>
spec:
  name: <repo-name>
  owner: <github-org-username>
  scmProviderRef:
    name: <your-scmprovider-name> # The secret that contains the GitHub App configuration
```

!!! note The GitRepository and ScmProvider also needs to be installed to the same namespace that you plan on creating PromotionStrategy 
resources in, and it also needs to be in the same namespace of the secret it references.


## Promotion Strategy

The PromotionStrategy resource is the main resource that you will use to configure the promotion of your application to different environments.
Here is an example PromotionStrategy resource:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: argocon-demo
spec:
  environments:
  - autoMerge: false
    branch: environments/development
  - autoMerge: false
    branch: environments/staging
  - autoMerge: false
    branch: environments/production
  gitRepositoryRef:
    name: <git-repository-ref-name> # The name of the GitRepository resource
```

!!! note Notice that the branches are prefixed with environments/, this is a convention that we recommend you follow.