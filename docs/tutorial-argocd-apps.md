# Tutorial: how to set up the promoter with ArgoCD and Github

This is a step by step tutorial to set up Gitops Promoter in a test environment.

We have 3 main goals:

1. Set up the cluster with ArgoCD and the Gitops Promoter
2. Set up a sample gitops repository on Github
3. Deploy the applications

Once there, we can play with the promotion strategies and explore the promoter further more.

## Requirements

To complete this tutorial you will need the following:

* [Kind](https://kind.sigs.k8s.io/) installed, or any kubernetes cluster with internet access.
* [Kubectl](https://kubernetes.io/fr/docs/tasks/tools/install-kubectl/) installed
* [A Github Account](https://github.com/)
* [Git](https://git-scm.com/)

## Set up the test cluster

### Create the cluster

If you don't have a test cluster yet, create one. With kind, simply run `kind create cluster`

!!! tip "Alternatively"

    You can name the cluster with `kind create cluster --name promoter`

Confirm that your access works with `kubectl get nodes`. Nodes should have a name starting with the name of your cluster (per example: `promoter-control-plane`)

### Install Argo CD

> More information in [Argo CD official documentation](https://argo-cd.readthedocs.io/en/stable/getting_started/)
>
> More information on the [ArgoCD hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/)

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install-with-hydrator.yaml
```

We want access to the UI, so open a dedicated terminal and run 

```bash
kubectl port-forward svc/argocd-server -n argocd 8080:443
```

Open your browser on `http://localhost:8080`, you should have the UI.

To get the `admin` password if you don't have the argocd CLI:

```bash
kubectl get secret -n argocd argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 --decode | xargs echo
```

Connect with this password for the `admin` user.

### Install the Gitops Promoter

> See [Getting Started](./getting-started.md)

```bash
kubectl apply -f https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.5.0/install.yaml
```

!!! note

    You may want to run the command twice to deploy the controller configuration after the CRDs were deployed.

## Set up a sample repository

### Create a fork

Argo provides with a example repository: [https://github.com/argoproj/argocd-example-apps](https://github.com/argoproj/argocd-example-apps). Fork it!

### Set up the environment branches

In your repo, create 6 branches from `main` :

- `environment/dev`
- `environment/dev-next` 
- `environment/staging`
- `environment/staging-next` 
- `environment/prod`
- `environment/prod-next`

!!! tip

    You can clone the repo locally and run the following commands to save time:

    ```bash
    git checkout main && git checkout -b environment/dev && git push
    git checkout main && git checkout -b environment/dev-next && git push
    git checkout main && git checkout -b environment/staging && git push
    git checkout main && git checkout -b environment/staging-next && git push
    git checkout main && git checkout -b environment/prod && git push
    git checkout main && git checkout -b environment/prod-next && git push
    git checkout main
    ```

### Create a Github application

In your Github account, go to Settings > Developer settings > Github Apps.

Clic on `New Github App`, pass the MFA challenge.

Fill up the form with the following (leave non specified to defaults):

| Field | Value |
|-|-|
| Github App name| A unique name of your choice (unique across the whole world) |
| Homepage URL | The URL of your profile |
| Webhook > Active | False |
| Permissions > Reporisotry Permissions > Commit statuses | Read and write |
| Permissions > Reporisotry Permissions > Content | Read and write |
| Permissions > Reporisotry Permissions > Pull requests | Read and write |

Hit the `Create GitHub App` button.

!!! info "Webhook"

    In this tutorial, we won't be using the webhook since we don't have any public IP with our kind cluster.

    See [Getting Started](./getting-started.md)

Once the app is created. Go to Install app and install it in your account.

#### Generate a key

In the app config, in the section General. Under Private keys. Clic on Generate a private key.

Keep the file close for later use.

#### Get app's identification

In the app config, in the section General. Find the "App ID" field and take not of the number.

Then, in Settings > Applications > `<your app>`. Find the installation id in the URL: `https://github.com/settings/installations/<installation-id>`.

Keep these numbers for later use.

### Give access to the repo to the app

Back in Settings. Go to Integrations > Applications. You should see your Github App in the list.

Clic on Edit and change the Repository access section.

- Only select repositories
- Select your fork of `argocd-example-apps`
- Clic on Save

## Prepare the terminal environment

We need to set up some temporary environment variables.

```bash
export GITHUB_ACCOUNT=<your-account-slug>
export GITHUB_APP_KEY_PATH=<path-to-the-generate-private-key>
export GITHUB_APP_ID=<your-github-app-id>
export GITHUB_APP_INSTALLATION_ID=<your-github-app-installation-id>
```

## Configure the repo secret

Create the secret.

```bash
kubectl create secret generic -n argocd github-app \
  --from-literal=url=https://github.com/${GITHUB_ACCOUNT}/argocd-example-apps \
  --from-literal=type=git \
  --from-literal=githubAppID=${GITHUB_APP_ID} \
  --from-literal=githubAppInstallationID=${GITHUB_APP_INSTALLATION_ID} \
  --from-file=githubAppPrivateKey=${GITHUB_APP_KEY_PATH}
kubectl label secret -n argocd github-app argocd.argoproj.io/secret-type=repository-write
```

## Deploy an application for 3 environments

Then create the application with the following:

```bash
for env in 'dev' 'staging' 'prod'; do
cat << EOF | kubectl apply -f-
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: ${env}-helm-guestbook
  namespace: argocd
spec:
  project: default
  destination:
    server: https://kubernetes.default.svc
    namespace: ${env}
  sourceHydrator:
    drySource:
      repoURL: https://github.com/${GITHUB_ACCOUNT}/argocd-example-apps
      path: helm-guestbook
      targetRevision: HEAD
    hydrateTo:
      targetBranch: environment/${env}-next
    syncSource:
      targetBranch: environment/${env}
      path: helm-guestbook
  syncPolicy:
    automated:
      prune: true
      allowEmpty: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
EOF
done
```

If you go to the ArgoCD UI, in the applications, you should now see the "SOURCE HYDRATOR" section in the header.

It shuold have the message "from HEAD (...) to environment/dev-next (...)"

This means three things in case of a change in the main branch:

1. Nothing will change in our application (it is sync on the non "-next" branch)
2. ArgoCD Hydrator will push a new commit to the "-next" branch
3. We need to manually create a pull request from the "-next" branch to environment branch. This for each environments...

Fortunately, the Gitops Promoter is here to automate the process.

## Deploy the GitOps promoter resources

### Create the secret

```bash
kubectl create secret generic github-app-promoter \
  --from-file=githubAppPrivateKey=${GITHUB_APP_KEY_PATH}
```

### Craete the SCM Provider

```bash
cat << EOF | kubectl apply -f-
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: github
spec:
  secretRef:
    name: github-app-promoter
  github:
    appID: ${GITHUB_APP_ID}
    installationID: ${GITHUB_APP_INSTALLATION_ID}
EOF
```

## Create the git repository

```bash
cat << EOF | kubectl apply -f-
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: github-argocd-example-apps
spec:
  github:
    owner: ${GITHUB_ACCOUNT}
    name: argocd-example-apps
  scmProviderRef:
    name: github
EOF
```

## Create the promotion strategy

Finally, create the promotion strategy.

```bash
cat << EOF | kubectl apply -f-
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-github
spec:
  environments:
    - autoMerge: true
      branch: environment/dev
    - autoMerge: true
      branch: environment/staging
    - autoMerge: false
      branch: environment/prod
  gitRepositoryRef:
    name: github-argocd-example-apps
EOF
```

Here, we implement a simple strategy :

1. Automerge on dev
2. Automerge on staging (if dev is successful)
3. Manual merge on production

!!! note

    You should see 2 PR getting merged automatically and 1 PR to merge manually for the production. That is normal: we branched from the main branch wich is DRY. The promoter detects change and do what's needed to sync the two branches.

## Play with your environment

We deployed the application "helm-guestbook" from the example repository.

Try editing the main branch: number of replicas, helm templates... And see the PRs getting promoted from dev to staging, then wait for your approval for the prod.

!!! note

    Since we are not using the webhook. It can takes 5 to 15 minutes to complete the cycle.