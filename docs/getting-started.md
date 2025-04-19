# Getting Started

This guide will help you get started installing and setting up the GitOps Promoter. We currently only support
GitHub, GitHub Enterprise and GitLab as the SCM providers. We would welcome any contributions to add support for other
providers.

## Requirements

* kubectl CLI
* kubernetes cluster
* GitHub or GitHub Enterprise Application
  * Will take PRs to add support for other SCM providers

## Installation

To install GitOps Promoter, you can use the following command:

```bash
kubectl apply -f https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.1.0/install.yaml
```

## GitHub App Configuration

You will need to [create a GitHub App](https://docs.github.com/en/developers/apps/creating-a-github-app) and configure
it to allow the GitOps Promoter to interact with your GitHub repository.


During the creation the GitHub App, you will need to configure the following settings:

### Permissions

| Action            | Permission     |
| ----------------- | -------------- |
| `Commit statuses` | Read and write |
| `Contents`        | Read and write |
| `Pull requests`   | Read and write |

### Webhooks (Optional - but highly recommended)

!!! note "Configure your webhook ingress"

    We do support configuration of a GitHub App webhook that triggers PR creation upon Push. However, we do not configure
    the ingress to allow GitHub to reach the GitOps Promoter. You will need to configure the ingress to allow GitHub to reach 
    the GitOps Promoter via the service promoter-webhook-receiver which listens on port `3333`. If you do not use webhooks 
    you might want to adjust the auto reconciliation interval to a lower value using these `promotionStrategyRequeueDuration` and
    `changeTransferPolicyRequeueDuration` fields of the `ControllerConfiguration` resource.

Webhook URL: `https://<your-promoter-webhook-receiver-ingress>/`

### Usage

The GitHub App will generate a private key that you will need to save. You will also need to get the App ID and the
installation ID in a secret as follows:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <your-secret-name>
type: Opaque
stringData:
  privateKey: <your-private-key>
```

!!! note 

    This Secret will need to be installed to the same namespace that you plan on creating PromotionStrategy resources in.

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
  github:
    appID: <your-app-id>
    installationID: <your-installation-id>
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: <git-repository-ref-name>
spec:
  github:
    name: <repo-name>
    owner: <github-org-username>
  scmProviderRef:
    name: <your-scmprovider-name> # The secret that contains the GitHub App configuration
```

!!! note 

    The GitRepository and ScmProvider also need to be installed to the same namespace that you plan on creating PromotionStrategy 
    resources in, and it also needs to be in the same namespace of the secret it references.

## GitLab Configuration

To configure the GitOps Promoter with GitLab, you will need to create a GitLab Access Token under the "Developer" role with `api` and `write_repository` scopes and configure the necessary resources to allow the promoter to interact with your repository. This Access Token should be used in a secret as follows:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <your-secret-name>
type: Opaque
stringData:
  token: <your-access-token>
```

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
  gitlab: {}
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: <git-repository-ref-name>
spec:
  gitlab:
    name: <repo-name>
    namespace: <user-or-group-with-subgroups>
    projectId: <project-id>
  scmProviderRef:
    name: <your-scmprovider-name> # The secret that contains the GitLab Access Token
```

## Promotion Strategy

The PromotionStrategy resource is the main resource that you will use to configure the promotion of your application to different environments.
Here is an example PromotionStrategy resource:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
spec:
  environments:
  - autoMerge: false
    branch: environment/development
  - autoMerge: false
    branch: environment/staging
  - autoMerge: false
    branch: environment/production
  gitRepositoryRef:
    name: <git-repository-ref-name> # The name of the GitRepository resource
```

!!! note 

    Notice that the branches are prefixed with `environment/`. This is a convention that we recommend you follow.

!!! note 

    The `autoMerge` field is optional and defaults to `true`. We set it to `false` here because we do not have any
    CommitStatus checks configured. With these all set to `false` we will have to manually merge the PRs.
