# Security policy

## Supported versions

This project is experimental. We will release security fixes as quickly as possible, but we provide no guarantees on supporting old versions.

## Reporting a vulnerability

**Do not** open public GitHub issues for security vulnerabilities.

You may report via the [GitHub Security Advisories](https://github.com/argoproj-labs/gitops-promoter/security/advisories/new) for this repository.

## Dependencies and advisory response

We monitor dependency and supply-chain advisories using **GitHub Dependabot** and **Renovate** (automated update PRs), and we run **CodeQL** static analysis on pull requests. When a **publicly known** vulnerability affects this project’s dependencies or our own code at **medium severity or higher**, we aim to ship a fix or mitigation within **60 days** of confirmation. **Critical** issues are prioritized and addressed as quickly as practical.

This policy describes intent; timelines can vary with maintainer capacity and upstream fixes.

## Webhook receiver hardening

The `promoter-webhook-receiver` service accepts incoming SCM webhook POST requests and triggers reconciliation of `ChangeTransferPolicy` resources.

> [!WARNING]
> **For any production deployment that exposes the webhook receiver on the internet, you must enable webhook signature verification.**
> Without it, any party who can reach the endpoint can send crafted payloads and trigger reconcile loops — a potential noise/DoS vector.

### GitHub

GitHub signs every webhook delivery with HMAC-SHA256 and sends the signature in the `X-Hub-Signature-256` header. GitOps Promoter validates this header using the signing secret configured on each `ScmProvider` (or `ClusterScmProvider`).

To enable signature verification:

1. **Create a Kubernetes Secret** containing the signing secret.
   For a namespaced `ScmProvider`, put the Secret in the same namespace.
   For a `ClusterScmProvider`, put the Secret in the controller's namespace (default: `promoter-system`):

   ```bash
   kubectl create secret generic github-webhook-secret \
     --namespace <scmprovider-namespace> \
     --from-literal=webhookSecret=<your-strong-random-secret>
   ```

2. **Set the same value** as the *Secret* field when configuring the webhook in your GitHub App or repository webhook settings.

3. **Reference the secret** in the `ScmProvider` (or `ClusterScmProvider`) `spec.github.webhookSecretRef`:

   ```yaml
   spec:
     github:
       appID: 12345
       webhookSecretRef:
         name: github-webhook-secret
   ```

When configured, the webhook receiver checks the `X-Hub-Signature-256` header on requests that match a `ChangeTransferPolicy` backed by this provider. Requests with a missing or invalid signature are rejected with HTTP 401.

> [!WARNING]
> Signature verification is applied per `ScmProvider`. A provider without `webhookSecretRef` accepts all requests without a signature check. **For full protection, configure `webhookSecretRef` on every `(Cluster)ScmProvider`.**
