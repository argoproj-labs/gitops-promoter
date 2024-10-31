# Promoter Docs

## Quick Start

### Start the controller

```shell
make install
make run
```

### Create a GitHub app

[Create a new GitHub app](https://github.com/settings/apps/new) with the following permissions:

* r/w commit status
* r/w contents
* r/w pull requests

Turn off the webhook.

After creating the app, generate a private key and save it to private-key.pem.

### Install the app

Install the app in the repository you want to use it in. After you install the app, the installation ID will appear in the URL. Copy this ID.

### Set up the promoter

Create a secret with the private key, the installation ID, and the app ID.

```shell
kubectl create secret generic my-auth --from-literal=privateKey="$(cat private-key.pem)" --from-literal=installationID=123456 --from-literal=appID=123456
```

Modify the following resources to fit your environment, and then `kubectl apply` them.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  labels:
    app.kubernetes.io/name: promoter
    app.kubernetes.io/managed-by: kustomize
  name: scmprovider-example
spec:
  github:
    domain: github.com
  secretRef:
    name: my-auth

---

apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: scmprovider-example
spec:
  dryBranch: master
  environments:
    - branch: environments/dev
    - branch: environments/test
    - branch: environments/prod
  repository:
    name: argocd-example-apps
    owner: crenshaw-dev
    scmProviderRef:
      name: scmprovider-example
```

