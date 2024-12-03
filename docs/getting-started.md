# Getting Started

This guide will help you get started installing and setting up the GitOps Promoter. We currently only support
Github and Github Enterprise as the SCM providers. We would welcome any contributions to add support for other
providers.

## Requirements

* kubectl CLI
* kustomize CLI
* kubernetes cluster
* Github or Github Enterprise Application

## Installation

To install the GitOps Promoter, you can use the following command:

```bash
kubectl apply -f https://github.com/crenshaw-dev/promoter/releases/download/latest/install.yaml
```

## Github App Configuration

To configure the Github App, you will need to create a new Github App in your organization. You can follow the
instructions [here](https://docs.github.com/en/developers/apps/creating-a-github-app) to create a new Github App.

!!! note We do support configuration of a Github App webhook. However, we do not configure the ingress to allow Github
to reach the GitOps Promoter. You will need to configure the ingress to allow Github to reach the GitOps Promoter 
via the service [promoter-webhook-receiver]() which listens on port `3333`. If you do not use webhooks you might want to
adjust the auto reconciliation interval to a lower value using these cli flags `--promotion-strategy-requeue-duration` and
`--change-transfer-policy-requeue-duration`.

During the creation the Github App, you will need to configure the following settings:

* Permissions
  * Commit statuses - Read & write
  * Contents - Read & write
  * Pull requests - Read & write
* Webbhook URL (Optional)
  * `https://<your-promoter-webhook-receiver-service>/`

The Github App will generate a private key that you will need to save. You will also need to get the App ID and the
installation ID in a secrete as follows:

```yaml
apiVersion: v1
stringData:
  appID: <your-app-id>
  installationID: <your-installtion-id>
  privateKey: <your-private-key>
kind: Secret
metadata:
  name: <your-secret-name>
type: Opaque
```

!!! note This secret will need to be installed to the same namespace that you plan on creating PromotionStrategy resources in.

