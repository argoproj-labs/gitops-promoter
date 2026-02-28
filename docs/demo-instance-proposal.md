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

> [!NOTE]
> The project already ships demo manifests used by the `gitops-promoter demo` CLI command (see `cmd/demo/manifests/`).
> Those manifests may serve as a starting point for the hosted demo configuration. Changes to the hosted demo config and
> the CLI demo manifests should be kept in sync to avoid divergence.

### 4. Commit Status Managers

The demo should exercise as many built-in commit-status controllers as possible:

| Commit Status Controller | Purpose in Demo |
|---|---|
| **Argo CD** | Gates promotion on the Argo CD application reaching `Healthy + Synced` in the previous environment. |
| **Timed** | Enforces a mandatory wait (e.g., five minutes) between environment promotions. |
| **Git Commit** | Verifies that the hydrated commit message matches the expected format before allowing promotion. |

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

## Prerequisites

Before beginning the implementation, the following must be in place:

| Prerequisite | Notes |
|---|---|
| **AWS account** | An `argoproj-labs` AWS account (or dedicated sub-account). Requires IAM permissions to manage VPCs, EKS clusters, IAM roles, and Route 53 records. |
| **Route 53 hosted zone** | A hosted zone for the demo domain (e.g. `gitops-promoter.io`) in the target AWS account. The domain registrar's nameservers must point at this hosted zone so that cert-manager can solve DNS-01 ACME challenges automatically. |
| **GitHub org access** | Admin access to `argoproj-labs` to create repositories, a GitHub App, and a GitHub OAuth App. |
| **GitHub team** | A `gitops-promoter-maintainers` team in `argoproj-labs` whose members receive Argo CD write access. |
| **Local tools** | `kubectl`, `helm` (≥ 3.x), `aws` CLI configured with credentials for the target account, `kubeseal` (matching the Sealed Secrets controller version installed in the cluster), `git`, and `kind` (used to bootstrap the ACK management cluster). |

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
├── infra/                   # ACK manifests for EKS cluster
│   ├── cluster.yaml         # ACK EKS Cluster CR
│   ├── nodegroup.yaml       # ACK EKS ManagedNodeGroup CR
│   └── vpc.yaml             # ACK EC2 VPC, Subnet, and InternetGateway CRs
├── renovate.json5
└── README.md
```

The `demo-dry` contents can live in a dedicated branch (`main`) of a separate repository
(`gitops-promoter-demo-config`) so that the DRY branch, staging branches, and live branches are all in one place,
keeping the cluster config separate from the workload config.

### Phase 2 — Kubernetes Cluster

Run the demo on **EKS (Amazon Elastic Kubernetes Service)**. A managed node group with two `t3.medium` nodes is
sufficient for the demo workloads.

Prefer Kubernetes controllers over imperative scripts wherever possible:

- **Cluster provisioning** — Use
  [AWS Controllers for Kubernetes (ACK)](https://aws-controllers-k8s.github.io/community/) with the EKS and EC2
  service controllers to declare and reconcile the EKS cluster itself as Kubernetes resources. The cluster manifests
  are committed to the infrastructure repository under `infra/` and ACK keeps the real cluster in sync with them,
  making the cluster fully re-creatable without any imperative tooling.
- **TLS certificates** — Install [cert-manager](https://cert-manager.io/) with the Route 53 DNS solver. A single
  `Certificate` CR issues a wildcard certificate for the demo domain; cert-manager renews it automatically.
- **Ingress** — Deploy the [ingress-nginx](https://kubernetes.github.io/ingress-nginx/) controller via its Helm chart
  (managed by Argo CD). All `Ingress` resources are declared in the infrastructure repository.
- **Secrets** — [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets) (already planned for Phase 4) is
  deployed as a Kubernetes controller and manages all sensitive values declaratively.

#### One-Time ACK Bootstrap

ACK itself needs a cluster to run on before it can create the target EKS cluster. Use a temporary local `kind`
cluster as the management cluster:

```bash
# 1. Create a local management cluster
kind create cluster --name ack-bootstrap

# 2. Create an IAM user or role for ACK and export credentials
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1   # choose the deployment region

# 3. Install the ACK EKS and EC2 service controllers
helm install ack-eks-controller \
  oci://public.ecr.aws/aws-controllers-k8s/eks-chart \
  --namespace ack-system --create-namespace \
  --set aws.region=$AWS_REGION

helm install ack-ec2-controller \
  oci://public.ecr.aws/aws-controllers-k8s/ec2-chart \
  --namespace ack-system \
  --set aws.region=$AWS_REGION

# 4. Apply the infra/ manifests (VPC first, then the cluster and node group)
kubectl apply -f infra/vpc.yaml
kubectl wait --for=condition=ACK.ResourceSynced vpc/demo-vpc --timeout=120s

kubectl apply -f infra/cluster.yaml
kubectl wait --for=condition=ACK.ResourceSynced cluster/gitops-promoter-demo --timeout=15m

kubectl apply -f infra/nodegroup.yaml
kubectl wait --for=condition=ACK.ResourceSynced managednodegroup/demo-nodes --timeout=10m

# 5. Retrieve kubeconfig for the new EKS cluster
aws eks update-kubeconfig --name gitops-promoter-demo --region $AWS_REGION

# 6. Tear down the temporary management cluster
kind delete cluster --name ack-bootstrap
```

The `infra/` manifests should specify the VPC CIDR, subnets (at least two availability zones for EKS), the
Kubernetes version, and the node group instance type and desired capacity. Store the AWS region and cluster name
as comments or a README in the `infra/` directory so they are easy to find during disaster recovery.

> [!NOTE]
> For future cluster changes (Kubernetes version upgrades, node group scaling), re-run the ACK bootstrap process
> from a fresh `kind` cluster, apply the updated `infra/` manifests, and tear down the `kind` cluster when done.

### Phase 3 — Bootstrap Argo CD

#### Step 3a — Create a GitHub OAuth App for Argo CD SSO

In the `argoproj-labs` GitHub organisation settings, create a new OAuth App:

| Field | Value |
|---|---|
| Application name | `GitOps Promoter Demo` |
| Homepage URL | `https://demo.gitops-promoter.io` |
| Authorization callback URL | `https://demo.gitops-promoter.io/api/dex/callback` |

Note the generated **Client ID** and **Client Secret** — they are needed when creating the Sealed Secret in
Step 3c.

#### Step 3b — Install Prerequisites

Before Argo CD is installed, the following controllers must be running:

```bash
# cert-manager (for TLS certificates)
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --set installCRDs=true

# Create a ClusterIssuer for Let's Encrypt (DNS-01 via Route 53)
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: maintainers@argoproj-labs.io   # replace with actual address
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
      - dns01:
          route53:
            region: us-east-1
            hostedZoneID: <HOSTED_ZONE_ID>
EOF

# ingress-nginx
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx --create-namespace

# Sealed Secrets controller
helm upgrade --install sealed-secrets sealed-secrets/sealed-secrets \
  --namespace kube-system
```

#### Step 3c — Seal Initial Secrets

With Sealed Secrets running, fetch the controller's public key and seal the two secrets Argo CD needs:

```bash
# Fetch the public key (needed for offline sealing)
kubeseal --fetch-cert > /tmp/sealed-secrets-pub.pem

# Seal the Dex GitHub OAuth secret
kubectl create secret generic argocd-dex-github-oauth \
  --namespace argocd \
  --from-literal=clientID=<OAUTH_CLIENT_ID> \
  --from-literal=clientSecret=<OAUTH_CLIENT_SECRET> \
  --dry-run=client -o yaml \
  | kubeseal --cert /tmp/sealed-secrets-pub.pem -o yaml \
  > manifests/argocd-dex-github-oauth-sealed.yaml

# Commit the SealedSecret (plaintext values are never stored)
git add manifests/argocd-dex-github-oauth-sealed.yaml
git commit -m "chore: add sealed Dex OAuth secret"
```

#### Step 3d — Install Argo CD

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
    policy.default: role:readonly  # unauthenticated public gets read-only
    policy.csv: |
      # Maintainers of argoproj-labs/gitops-promoter get write access
      g, argoproj-labs:gitops-promoter-maintainers, role:admin
  cm:
    # GitHub SSO — maintainers log in with their GitHub accounts
    dex.config: |
      connectors:
        - type: github
          id: github
          name: GitHub
          config:
            clientID: $dex.github.clientID
            clientSecret: $dex.github.clientSecret
            orgs:
              - name: argoproj-labs
                teams:
                  - gitops-promoter-maintainers
    extension.config: |
      extensions:
        - name: gitops-promoter
          backend:
            services:
              - url: http://gitops-promoter-ui.gitops-promoter.svc.cluster.local
```

Install the GitOps Promoter Argo CD extension. The extension sidecar or backend service should be deployed alongside
Argo CD.

Once Argo CD is running, apply the root App of Apps to hand off all remaining resource management to Argo CD:

```bash
kubectl apply -f apps/root-app.yaml
```

From this point on, all changes to the cluster are made by editing files in the repository and letting Argo CD
reconcile them.

### Phase 4 — Install GitOps Promoter

#### Step 4a — Create the GitHub App

In the `argoproj-labs` GitHub organisation settings, create a new GitHub App with the following permissions:

| Permission | Level |
|---|---|
| `Contents` | Read and write |
| `Pull requests` | Read and write |
| `Checks` | Read and write |

Install the App on the `gitops-promoter-demo-config` repository only. After installation:

1. Note the **App ID** shown on the App's settings page.
2. Note the **Installation ID** from the URL when viewing the installation
   (`https://github.com/organizations/argoproj-labs/settings/installations/<installation-id>`).
3. Generate and download a **private key** (`.pem` file) from the App's settings page.

#### Step 4b — Seal the GitHub App Secret

```bash
kubectl create secret generic github-app-credentials \
  --namespace gitops-promoter \
  --from-file=githubAppPrivateKey=<path-to-private-key.pem> \
  --dry-run=client -o yaml \
  | kubeseal --cert /tmp/sealed-secrets-pub.pem -o yaml \
  > manifests/github-app-credentials-sealed.yaml

git add manifests/github-app-credentials-sealed.yaml
git commit -m "chore: add sealed GitHub App credentials"
```

#### Step 4c — Deploy GitOps Promoter via Argo CD

Create the Argo CD Application that manages GitOps Promoter via its Helm chart. This is a multi-source Application
that pulls chart values from the infrastructure repository:

```yaml
# apps/gitops-promoter.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitops-promoter
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: https://argoproj-labs.github.io/gitops-promoter
      chart: gitops-promoter
      targetRevision: "0.22.6"   # kept up to date by Renovate
      helm:
        valueFiles:
          - $values/charts/gitops-promoter/values.yaml
    - repoURL: https://github.com/argoproj-labs/gitops-promoter-demo
      targetRevision: HEAD
      ref: values   # makes this source available as $values in the first source
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

Store SCM credentials (GitHub App private key, App ID, installation ID) and Dex OAuth secrets in Kubernetes Secrets
managed by [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets) so they are safe to commit to the
infrastructure repository in encrypted form. See Phase 7 for the complete `kubeseal` command that creates the
`github-app-credentials` SealedSecret used by both GitOps Promoter and the CronJob.

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

#### Initialize the Branch Structure

The environment branches must exist before GitOps Promoter and the Argo CD Source Hydrator will work. Create them
as empty branches from `main`:

```bash
git clone https://github.com/argoproj-labs/gitops-promoter-demo-config
cd gitops-promoter-demo-config

for branch in env/dev/next env/dev/live env/staging/next env/staging/live env/production/next env/production/live; do
  git checkout --orphan "$branch"
  git rm -rf .
  git commit --allow-empty -m "chore: initialize $branch"
  git push origin "$branch"
  git checkout main
done
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
each environment to produce hydrated commits from `main` → `env/*/next`. Each environment needs an Argo CD
`Application` that uses `sourceHydrator` to hydrate DRY source and sync from the live branch. Commit these
Application manifests to the infrastructure repository under `manifests/apps/`:

```yaml
# manifests/apps/demo-dev.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo-dev
  namespace: argocd
  labels:
    app-name: demo   # used by ArgoCDCommitStatus selector
spec:
  project: default
  destination:
    server: https://kubernetes.default.svc
    namespace: demo-dev
  sourceHydrator:
    drySource:
      repoURL: https://github.com/argoproj-labs/gitops-promoter-demo-config
      path: app
      targetRevision: main
    hydrateTo:
      targetBranch: env/dev/next
    syncSource:
      targetBranch: env/dev/live
      path: app
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

Repeat with `demo-staging` (using `env/staging/next` / `env/staging/live`) and `demo-production` (using
`env/production/next` / `env/production/live`), adjusting the namespace and branch names accordingly.

### Phase 6 — GitOps Promoter Configuration

Define the core GitOps Promoter CRDs in `promoter-config/`:

**`scm-provider.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: demo-scm
  namespace: gitops-promoter
spec:
  github:
    appID: 12345          # replace with actual App ID
    # installationID is optional; omit to auto-discover from repo owner
  secretRef:
    name: github-app-credentials  # SealedSecret created in Phase 4b
```

**`git-repository.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: demo-config
  namespace: gitops-promoter
spec:
  github:
    owner: argoproj-labs
    name: gitops-promoter-demo-config
  scmProviderRef:
    kind: ScmProvider
    name: demo-scm
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
# Gates staging promotion: dev app must be Healthy+Synced
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: argocd-dev-healthy
  namespace: gitops-promoter
spec:
  promotionStrategyRef:
    name: demo
  applicationSelector:
    matchLabels:
      app-name: demo
---
# Gates production promotion: staging app must be Healthy+Synced
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: argocd-staging-healthy
  namespace: gitops-promoter
spec:
  promotionStrategyRef:
    name: demo
  applicationSelector:
    matchLabels:
      app-name: demo
```

> [!NOTE]
> GitOps Promoter needs permission to read Argo CD `Application` resources. Create a `ClusterRole` and
> `ClusterRoleBinding` granting the `gitops-promoter-controller-manager` ServiceAccount `get`/`list`/`watch` on
> `argoproj.io` `applications`.

**`commit-statuses/timed-commit-status.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: timed-5m
  namespace: gitops-promoter
spec:
  promotionStrategyRef:
    name: demo
  environments:
    - branch: env/staging/live
      duration: 5m
    - branch: env/production/live
      duration: 5m
```

**`commit-statuses/git-commit-status.yaml`**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitCommitStatus
metadata:
  name: git-commit-check
  namespace: gitops-promoter
spec:
  promotionStrategyRef:
    name: demo
  key: git-commit-check
  description: "Commit subject must start with 'chore:'"
  target: proposed
  expression: 'Commit.Subject.startsWith("chore:")'
```

### Phase 7 — Synthetic Commit CronJob

A Kubernetes CronJob runs every ten minutes and makes a trivial change to the DRY branch to trigger a new promotion
cycle. Because GitHub App installation tokens are short-lived (≤ 1 hour), an init container generates a fresh token
before each run and writes it to a shared `emptyDir` volume:

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
          volumes:
            - name: token
              emptyDir: {}
            - name: app-credentials
              secret:
                secretName: github-app-credentials
          initContainers:
            - name: generate-token
              image: alpine:3.20   # kept up to date by Renovate
              command:
                - /bin/sh
                - -c
                - |
                  apk add --no-cache curl openssl
                  APP_ID=$(cat /secrets/githubAppID)
                  INSTALL_ID=$(cat /secrets/githubAppInstallationID)
                  NOW=$(date +%s); EXP=$((NOW + 540))
                  HEADER=$(printf '{"alg":"RS256","typ":"JWT"}' | base64 | tr -d '=' | tr '+/' '-_')
                  PAYLOAD=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' "$NOW" "$EXP" "$APP_ID" | base64 | tr -d '=' | tr '+/' '-_')
                  SIG=$(printf '%s.%s' "$HEADER" "$PAYLOAD" \
                    | openssl dgst -binary -sha256 -sign /secrets/githubAppPrivateKey \
                    | base64 | tr -d '=' | tr '+/' '-_')
                  JWT="$HEADER.$PAYLOAD.$SIG"
                  curl -sf -X POST \
                    -H "Authorization: Bearer $JWT" \
                    -H "Accept: application/vnd.github.v3+json" \
                    "https://api.github.com/app/installations/$INSTALL_ID/access_tokens" \
                    | grep -o '"token":"[^"]*"' | cut -d'"' -f4 \
                    > /token/GITHUB_TOKEN
              volumeMounts:
                - name: app-credentials
                  mountPath: /secrets
                  readOnly: true
                - name: token
                  mountPath: /token
          containers:
            - name: git
              image: alpine/git:2.45.2   # kept up to date by Renovate
              command:
                - /bin/sh
                - -c
                - |
                  export GITHUB_TOKEN=$(cat /token/GITHUB_TOKEN)
                  git clone https://x-access-token:${GITHUB_TOKEN}@github.com/argoproj-labs/gitops-promoter-demo-config /work
                  cd /work
                  git config user.email "demo-bot@gitops-promoter.io"
                  git config user.name "Demo Bot"
                  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > timestamp.txt
                  git add timestamp.txt
                  git commit -m "chore: automated demo cycle $(date -u +%Y-%m-%dT%H:%M:%SZ)"
                  git push origin main
              volumeMounts:
                - name: token
                  mountPath: /token
                  readOnly: true
          restartPolicy: OnFailure
```

The `github-app-credentials` Secret must contain three keys: `githubAppPrivateKey` (the PEM private key),
`githubAppID`, and `githubAppInstallationID`. Seal all three together in one SealedSecret:

```bash
kubectl create secret generic github-app-credentials \
  --namespace gitops-promoter \
  --from-file=githubAppPrivateKey=<path-to-private-key.pem> \
  --from-literal=githubAppID=<APP_ID> \
  --from-literal=githubAppInstallationID=<INSTALLATION_ID> \
  --dry-run=client -o yaml \
  | kubeseal --cert /tmp/sealed-secrets-pub.pem -o yaml \
  > manifests/github-app-credentials-sealed.yaml
```

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

Configure the GitHub App's webhook to point at the GitOps Promoter webhook receiver Ingress. This ensures
promotion PRs are opened within seconds of a new hydrated commit arriving, rather than waiting for the next
reconcile loop.

In the GitHub App settings, set:

| Field | Value |
|---|---|
| Webhook URL | `https://promoter-webhook.gitops-promoter.io/` |
| Webhook secret | A random string (store in a SealedSecret as `webhookSecret` in the `gitops-promoter` namespace) |
| SSL verification | Enabled |

Subscribe to the following events:

- **Push** — triggers hydration when a new commit lands on `main`
- **Pull request** — updates promotion PR status when a PR is opened, closed, or merged
- **Check run** / **Check suite** — required if commit status checks are surfaced as GitHub Checks

Configure GitOps Promoter to verify the webhook secret by setting `webhookSecret.secretRef` in
`charts/gitops-promoter/values.yaml`:

```yaml
webhookReceiver:
  ingress:
    enabled: true
    hostname: promoter-webhook.gitops-promoter.io
  webhookSecret:
    secretRef:
      name: promoter-webhook-secret
      key: webhookSecret
```

### Phase 10 — Monitoring and Alerting

Deploy the GitOps Promoter Prometheus metrics endpoint and configure a lightweight alerting rule (e.g., via the
Prometheus Alertmanager or a free tier of Grafana Cloud) to page the maintainers if:

- The cluster becomes unreachable.
- No promotion cycle completes within 30 minutes (indicating the CronJob or hydrator is broken).
- Argo CD application health degrades.

### Phase 11 — Maintenance Documentation

The infrastructure repository should include a `docs/` directory with runbooks for routine operational tasks:

| Document | Contents |
|---|---|
| `docs/secret-rotation.md` | Step-by-step guide for rotating the GitHub App private key and updating the Sealed Secret in the cluster. Covers generating a new key, re-encrypting with `kubeseal`, committing the updated SealedSecret, and verifying the rollout. |
| `docs/github-oauth-rotation.md` | Instructions for rotating the Dex GitHub OAuth client secret used for Argo CD SSO. |
| `docs/cluster-bootstrap.md` | How to provision a brand-new EKS cluster using the ACK management cluster and bootstrap Argo CD from scratch using the root App of Apps. |
| `docs/break-glass.md` | Procedure for obtaining temporary admin access to the cluster in an emergency (e.g., Argo CD is unreachable). |
| `docs/renovate-troubleshooting.md` | What to do when a Renovate automerge PR fails and manual intervention is required to update a dependency. |

These documents should be reviewed and updated whenever the corresponding infrastructure changes.

---

## Security Considerations

| Risk | Mitigation |
|---|---|
| Public users performing destructive Argo CD actions | `role:readonly` is set as the Argo CD default policy; no write permissions are granted to unauthenticated users. |
| Exposed Git credentials in CronJob | Credentials are stored in Sealed Secrets and never committed in plaintext. |
| Cluster over-privilege | The CronJob ServiceAccount is limited to the `gitops-promoter` namespace with minimal RBAC. |
| Unintended public writes to demo config repo | The GitHub App is scoped to the demo config repository only, and only the CronJob automation uses it for commits. |

---

## Cost Estimate

Running the demo on EKS with two `t3.medium` nodes is estimated at approximately **$70–100 per month** (On-Demand
pricing). Switching to Reserved Instances or Savings Plans can reduce this by 30–40%.

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
