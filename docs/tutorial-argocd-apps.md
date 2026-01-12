# Tutorial: how to set up the promoter with Argo CD and GitHub

This is a step by step tutorial to set up GitOps Promoter in a test environment.

We have 3 main goals:

1. Set up the cluster with Argo CD and GitOps Promoter
2. Set up a sample GitOps repository on GitHub
3. Deploy the applications

Once there, we can play with the promotion strategies and explore the Promoter further.

## Requirements

To complete this tutorial, you will need the following:

* [Kind](https://kind.sigs.k8s.io/) installed, or any kubernetes cluster with internet access.
* [Kubectl](https://kubernetes.io/fr/docs/tasks/tools/install-kubectl/) installed
* [A GitHub Account](https://github.com/)
* [Git](https://git-scm.com/)

> [!NOTE]
> GitOps Promoter provides several opt-in Argo CD integrations that improve the user experience. Check out the 
> [Argo CD Integrations](./argocd-integrations.md) page for more information.

## Set up the test cluster

### Create the cluster

If you don't have a test cluster yet, create one. With kind, simply run `kind create cluster`

> [!TIP]
> You can name the cluster with `kind create cluster --name promoter`

Confirm that your access works with `kubectl get nodes`. Nodes should have a name starting with the name of your cluster (per example: `promoter-control-plane`)

### Install Argo CD

> More information in [Argo CD official documentation](https://argo-cd.readthedocs.io/en/stable/getting_started/)
>
> More information on the [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/)

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

### Install GitOps Promoter

> See [Getting Started](./getting-started.md)

```bash
kubectl apply -f https://github.com/argoproj-labs/gitops-promoter/releases/download/v0.20.2/install.yaml
```

> [!NOTE]
> You may want to run the command twice to deploy the controller configuration after the CRDs were deployed.

## Set up a sample repository

### Create a fork

Argo provides an example repository: [https://github.com/argoproj/argocd-example-apps](https://github.com/argoproj/argocd-example-apps). Fork it!

> [!IMPORTANT]
> Make sure your staging branches (`environment/development-next`, `environment/staging-next`, etc.) are not auto-deleted
> when PRs are merged. You can do this either by disabling auto-deletion of branches in the repository settings (in
> Settings > Automatically delete head branches) or by adding a branch protection rule for a matching pattern such as
> `environment/*-next` (`/` characters are separators in GitHub's glob implementation, so `*-next` will not work).

### Create a GitHub application

In your GitHub account, go to Settings > Developer settings > GitHub Apps.

Click on `New GitHub App`, pass the MFA challenge.

Fill up the form with the following (leave non specified to defaults):

| Field                                                  | Value                                                        |
|--------------------------------------------------------|--------------------------------------------------------------|
| GitHub App name                                        | A unique name of your choice (unique across the whole world) |
| Homepage URL                                           | The URL of your profile                                      |
| Webhook > Active                                       | False                                                        |
| Permissions > Repository Permissions > Checks          | Read and write                                               |
| Permissions > Repository Permissions > Content         | Read and write                                               |
| Permissions > Repository Permissions > Pull requests   | Read and write                                               |

Hit the `Create GitHub App` button.

Once the app is created. Go to Install app and install it in your account.

#### Using the webhook

The webhook notifies the Promoter and Argo CD that a new commit was push/merged to the main branch. It greatly reduces the latency between deployment.

However, we are running the workload locally and the webhook is not required for a simple demo.

If you want to set up the webhook, read [Getting started](./getting-started.md) and try to use [https://smee.io/](https://smee.io/).

> [!TIP]
> You can adjust the setting down to something like 15 or 30 seconds.
>
> See the [default config](https://github.com/argoproj-labs/gitops-promoter/blob/65d5905c51acac5d1caf3af01ceb0747795207e5/config/config/controllerconfiguration.yaml#L14-L17) for the config location.
>
> ```shell
> kubectl patch -n promoter-system controllerconfiguration promoter-controller-configuration \
>   --type merge \
>   -p '{"spec": {"promotionStrategyRequeueDuration": "30s", "changeTransferPolicyRequeueDuration": "30s", "argocdCommitStatusRequeueDuration": "30s", "pullRequestRequeueDuration": "30s"}}'
> ```

#### Generate a key

In the app config, in the section General. Under Private keys. Click on Generate a private key.

Keep the file close for later use.

#### Get app's identification

In the app config, in the section General. Find the "App ID" field and take not of the number.

Then, in Settings > Applications > `<your app>`. Find the installation ID in the URL: `https://github.com/settings/installations/<installation-id>`.

Keep these numbers for later use.

### Give access to the repo to the app

Back in Settings. Go to Integrations > Applications. You should see your GitHub App in the list.

Click on Edit and change the Repository access section.

- Only select repositories
- Select your fork of `argocd-example-apps`
- Click on Save

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
for env in 'development' 'staging' 'prod'; do
cat << EOF | kubectl apply -f-
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: ${env}-helm-guestbook
  namespace: argocd
  labels:
    # This label allows the ArgoCDCommitStatus to find the applications.
    app-name: helm-guestbook
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

If you go to the Argo CD UI, in the applications, you should now see the "SOURCE HYDRATOR" section in the header.

It should have the message "from HEAD (...) to environment/development-next (...)"

> [!IMPORTANT]
> The Application will have an error under "APP CONDITIONS" that "app path does not exist." That's because the
> Promoter has not yet moved the hydrated manifests to the syncSource branches. That's the next step.

This means three things in case of a change in the main branch:

1. Nothing will change in our application (it is sync on the non "-next" branch)
2. Argo CD Hydrator will push a new commit to the "-next" branch
3. We need to manually create a pull request from the "-next" branch to environment branch. This for each environment...

Fortunately, GitOps Promoter is here to automate the process.

## Deploy GitOps Promoter resources

### Create the secret

```bash
kubectl create secret generic github-app-promoter \
  --from-file=githubAppPrivateKey=${GITHUB_APP_KEY_PATH}
```

### Create the SCM Provider

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
    installationID: ${GITHUB_APP_INSTALLATION_ID} # Optional, will query ListInstallations if not provided
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

Create the promotion strategy.

```bash
cat << EOF | kubectl apply -f-
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo-github
spec:
  activeCommitStatuses:
  # The ArgoCDCommitStatus CR will maintain this commit status based on the application health.
  - key: argocd-health
  environments:
    - autoMerge: true
      branch: environment/development
    - autoMerge: true
      branch: environment/staging
    - autoMerge: false
      branch: environment/prod
  gitRepositoryRef:
    name: github-argocd-example-apps
EOF
```

Finally, create an ArgoCDCommitStatus resource to monitor the status of the Argo CD applications and maintain the
`argocd-health` commit status.

```bash
cat << EOF | kubectl apply -f-
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: argocd-health
spec:
  promotionStrategyRef:
    name: demo-github
  applicationSelector:
    matchLabels:
      app-name: helm-guestbook
EOF
```

Here, we implement a simple strategy:

1. Auto-merge on development
2. Auto-merge on staging (if development is successful)
3. Manual merge on production

> [!NOTE]
> You should see 2 PRs getting merged automatically and 1 PR to merge manually for the production. That is normal: we branched from the main branch which is DRY. The promoter detects change and do what's needed to sync the two branches.

## Play with your environment

We deployed the application "helm-guestbook" from the example repository.

Try editing the main branch: number of replicas, helm templates... And see the PRs getting promoted from development to staging, then wait for your approval for the prod.

> [!NOTE]
> Since we are not using the webhook. It can takes 5 to 15 minutes to complete the cycle unless you've set the requeue duration to a lower value.
