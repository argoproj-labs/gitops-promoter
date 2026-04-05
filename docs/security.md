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

GitHub signs every webhook delivery with HMAC-SHA256 and sends the signature in the `X-Hub-Signature-256` header. GitOps Promoter can validate this header using a shared secret.

To enable signature verification:

1. **Create a Kubernetes Secret** in the controller's namespace containing the signing secret:

   ```bash
   kubectl create secret generic github-webhook-secret \
     --namespace promoter-system \
     --from-literal=webhookSecret=<your-strong-random-secret>
   ```

2. **Set the same value** as the *Secret* field when configuring the webhook in your GitHub App or repository webhook settings.

3. **Reference the secret** in `ControllerConfiguration`:

   ```yaml
   spec:
     webhookReceiver:
       github:
         secretRef:
           name: github-webhook-secret
   ```

When configured, requests with a missing or invalid `X-Hub-Signature-256` header are rejected with HTTP 401 before any reconciliation is triggered. The comparison uses `hmac.Equal` (constant-time) to prevent timing attacks.

See [Getting Started — Securing GitHub Webhooks](getting-started.md) for the full configuration walkthrough.
