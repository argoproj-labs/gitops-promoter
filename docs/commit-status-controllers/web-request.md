# Web Request Commit Status Controller

The Web Request Commit Status controller enables environment gating based on external HTTP/HTTPS API validation. This controller makes HTTP requests to external systems and evaluates their responses to determine if a promotion should proceed, allowing integration with virtually any external validation system.

## Overview

The WebRequestCommitStatus controller provides flexible validation by calling external HTTP APIs and evaluating the responses. It supports both simple polling and advanced expression-based triggering, making it suitable for a wide range of integration scenarios.

### How It Works

For each environment configured via the PromotionStrategy:

1. The controller determines which SHA to validate based on the `reportOn` setting:
   - `proposed` (default): Validates the commit that will be promoted
   - `active`: Validates the currently deployed commit
2. The controller evaluates whether to make an HTTP request (polling mode always makes requests, trigger mode evaluates a trigger expression first)
3. If triggered, the controller makes an HTTP request to the configured endpoint using templated URL, headers, and body
4. The controller evaluates the success expression against the HTTP response
5. The controller creates/updates a CommitStatus with the results of the success expression
6. The PromotionStrategy checks the CommitStatus before allowing promotion

### Operating Modes

WebRequestCommitStatus supports two distinct operating modes:

#### Polling Mode

Continuously polls the HTTP endpoint at a fixed interval. Simple and reliable for most use cases.

**Use cases:**
- External approval systems with status endpoints
- Change management APIs that update status over time
- Monitoring systems with health check endpoints
- Simple API integrations without complex state tracking

**Behavior:**
- Makes HTTP request every interval (default: 1 minute)
- For `reportOn: proposed`: Stops polling once success is achieved for a given SHA
- For `reportOn: active`: Continuously polls forever to track active state changes

#### Trigger Mode

Uses expressions to dynamically control when HTTP requests are made. Powerful for advanced scenarios requiring state tracking or conditional logic.

**Use cases:**
- Only trigger requests when SHA changes
- Implement rate limiting or backoff strategies based on previous responses
- Track custom state between reconciliations
- Conditional triggering based on previous environment status
- Complex integration patterns requiring decision logic

**Behavior:**
- Evaluates trigger expression on each reconciliation
- Only makes HTTP request if trigger expression returns true
- Can store and access custom state via `TriggerOutput` (via `trigger.when.output.expression`)
- Can access previous HTTP response data via `ResponseOutput` (via `response.output.expression`)
- Always reconciles at `requeueDuration` interval (default: 1 minute)

## Security Considerations

The WebRequestCommitStatus controller renders URLs (and optionally headers and body) from Go templates and makes HTTP requests on behalf of the cluster. Although URLs are typically admin-controlled via CRDs, no validation is performed on the rendered URL. Malicious or misconfigured templates could potentially make requests to internal services, cloud metadata endpoints (e.g. `169.254.169.254`), or other sensitive targets (SSRF risk). The following practices help reduce risk.

**Recommendations for administrators:**

- **Restrict who can create or modify WebRequestCommitStatus resources** using RBAC. Only trusted actors should be able to set or change `spec.httpRequest.urlTemplate` and related fields.
- **Be cautious with templates that include user-controlled or namespace-controlled data.** Template variables such as `NamespaceMetadata.Labels`, `NamespaceMetadata.Annotations`, and data from `TriggerOutput` or `ResponseOutput` can influence the rendered URL. If those values are controllable by less-trusted users, they could push the URL toward internal or metadata endpoints.
- **Consider network policies** to limit egress traffic from the controller (e.g. only to approved external APIs). This helps limit which destinations the controller can reach even if a CRD is misconfigured or compromised.

### Summary for administrators

| Control | Recommendation |
|--------|----------------|
| **RBAC** | Restrict create/update/patch of `webrequestcommitstatuses` to trusted admins. |
| **Templates** | Avoid putting user- or tenant-controlled data into URL (or host) templates when possible. |
| **Network** | Use network policies or similar mechanisms to restrict controller egress to intended destinations. |

## Example Configurations

### Basic Polling Mode

Simple polling configuration that checks an external approval API:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-approval
spec:
  promotionStrategyRef:
    name: my-app
  key: external-approval
  descriptionTemplate: "Waiting for external approval"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://approvals.example.com/api/check/{{ .ReportedSha }}"
    method: GET
    timeout: 30s
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.approved == true"
  mode:
    polling:
      interval: 2m
```

### Polling with Authentication

Using bearer token authentication to call a protected API:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: change-validation
spec:
  promotionStrategyRef:
    name: my-app
  key: change-validation
  descriptionTemplate: "Validating change request for {{ .Environment.Branch }}"
  urlTemplate: "https://dashboard.example.com/changes/{{ .ReportedSha }}"
  httpRequest:
    urlTemplate: "https://api.example.com/v1/changes/{{ .ReportedSha }}/status"
    method: GET
    headerTemplates:
      Content-Type: "application/json"
    authentication:
      bearer:
        secretRef:
          name: api-token-secret
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.status == 'approved'"
  mode:
    polling:
      interval: 5m
---
apiVersion: v1
kind: Secret
metadata:
  name: api-token-secret
type: Opaque
stringData:
  token: "your-bearer-token-here"
```

### Trigger Mode - SHA Change Detection

Only make HTTP requests when the SHA changes, avoiding redundant calls:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: deployment-check
spec:
  promotionStrategyRef:
    name: my-app
  key: deployment-check
  descriptionTemplate: "Checking deployment {{ .ReportedSha | trunc 7 }}"
  httpRequest:
    urlTemplate: "https://monitoring.example.com/api/deployment/{{ .ReportedSha }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.ready == true"
  mode:
    trigger:
      requeueDuration: 1m
      when:
        expression: "ReportedSha != TriggerOutput[\"lastCheckedSha\"]"
        output:
          expression: "{ lastCheckedSha: ReportedSha }"
```

### Trigger Mode - Only when another commit status is success

Only run the HTTP request when a particular commit status (for example Argo CD health) is already success. This gates your validation on another gate so you avoid calling the API until it is relevant:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: validate-after-argocd
spec:
  promotionStrategyRef:
    name: my-app
  key: validate-after-argocd
  descriptionTemplate: "External validation for {{ .ReportedSha }}"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/validate/{{ .ReportedSha }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200"
  mode:
    trigger:
      requeueDuration: 30s
      when:
        expression: |
          size(filter(Environment.Proposed.CommitStatuses, {.Key == "argocd-health"})) > 0 &&
          filter(Environment.Proposed.CommitStatuses, {.Key == "argocd-health"})[0].Phase == "success"
```

Use the same `Key` as in your PromotionStrategy's proposed or active commit statuses (e.g. `argocd-health`, `timer`).

### Trigger Mode with Response Data Tracking

Store and use data from previous HTTP responses to implement retry logic:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: progressive-check
spec:
  promotionStrategyRef:
    name: my-app
  key: progressive-check
  descriptionTemplate: "Progressive validation (attempt {{ index .TriggerOutput \"attemptCount\" | default 0 }})"
  httpRequest:
    urlTemplate: "https://validation.example.com/api/check/{{ .ReportedSha }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.validated == true"
  mode:
    trigger:
      requeueDuration: 1m
      when:
        expression: |
          ResponseOutput == nil || 
          ResponseOutput.status == "retry" || 
          ResponseOutput.validated == false
        output:
          expression: |
            { attemptCount: (TriggerOutput["attemptCount"] ?? 0) + 1 }
      response:
        output:
          expression: |
            {
              status: Response.Body.status,
              validated: Response.Body.validated,
              retryAfter: Response.Body.retryAfter
            }
```

### POST Request with JSON Body

Sending structured data to an external validation service:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: compliance-check
spec:
  promotionStrategyRef:
    name: my-app
  key: compliance-check
  descriptionTemplate: "Checking compliance for {{ .Environment.Branch }}"
  httpRequest:
    urlTemplate: "https://compliance.example.com/api/v1/validate"
    method: POST
    headerTemplates:
      Content-Type: "application/json"
      X-Environment: "{{ .Environment.Branch }}"
    bodyTemplate: |
      {
        "commitSha": "{{ .ReportedSha }}",
        "environment": "{{ .Environment.Branch }}",
        "dryCommitSha": "{{ .Environment.Active.Dry.Sha }}",
        "namespace": "{{ index .NamespaceMetadata.Labels "team" }}"
      }
    authentication:
      basic:
        secretRef:
          name: compliance-api-creds
  success:
    when:
      expression: |
        Response.StatusCode == 200 && 
        Response.Body.compliant == true &&
        Response.Body.score >= 0.8
  mode:
    polling:
      interval: 5m
---
apiVersion: v1
kind: Secret
metadata:
  name: compliance-api-creds
type: Opaque
stringData:
  username: "service-account"
  password: "your-password-here"
```

### OAuth2 Authentication

Using OAuth2 client credentials flow for enterprise API integration:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: enterprise-gate
spec:
  promotionStrategyRef:
    name: my-app
  key: enterprise-gate
  descriptionTemplate: "Enterprise approval check"
  httpRequest:
    urlTemplate: "https://api.enterprise.com/v2/deployments/{{ .ReportedSha }}/approval"
    method: GET
    authentication:
      oauth2:
        tokenURL: "https://auth.enterprise.com/oauth/token"
        scopes: ["deployments:read", "approvals:read"]
        secretRef:
          name: oauth-credentials
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.approved == true"
  mode:
    polling:
      interval: 3m
---
apiVersion: v1
kind: Secret
metadata:
  name: oauth-credentials
type: Opaque
stringData:
  clientID: "your-client-id"
  clientSecret: "your-client-secret"
```

### Mutual TLS (mTLS) Authentication

Using client certificates for high-security environments:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: secure-validation
spec:
  promotionStrategyRef:
    name: my-app
  key: secure-validation
  descriptionTemplate: "Secure validation check"
  httpRequest:
    urlTemplate: "https://secure.internal.company.com/api/validate/{{ .ReportedSha }}"
    method: GET
    authentication:
      tls:
        secretRef:
          name: mtls-client-cert
  success:
    when:
      expression: "Response.StatusCode == 200"
  mode:
    polling:
      interval: 2m
---
apiVersion: v1
kind: Secret
metadata:
  name: mtls-client-cert
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-certificate>
  tls.key: <base64-encoded-private-key>
  ca.crt: <base64-encoded-ca-cert>  # Optional, for custom CA
```

### SCM Provider Credentials

Instead of creating separate secrets, you can reuse the SCM provider credentials configured in your PromotionStrategy. This is useful when:

- Making requests to the same SCM provider's API (e.g. GitHub API, GitLab API)
- Your external API accepts the same credentials as your SCM provider
- You want to avoid duplicating secrets

Set `authentication.scmAuth: {}` and the controller will use the credentials from the ScmProvider referenced by the PromotionStrategy's repository. The authentication method is applied automatically based on the SCM provider type (GitHub App, GitLab token, Azure DevOps PAT, etc.).

| SCM Provider   | Authentication method              | Applied as                          |
|----------------|------------------------------------|-------------------------------------|
| GitHub         | GitHub App (JWT / installation)    | Custom HTTP client (installation transport) |
| GitLab         | Access token                       | `PRIVATE-TOKEN` header               |
| Azure DevOps   | Personal Access Token (PAT)        | Basic auth (empty username, PAT as password) |
| Bitbucket Cloud| Repository token                   | Bearer token header                 |
| Forgejo / Gitea| Token or basic auth                | `Authorization: token <token>` or Basic auth (username/password) |

**Example — Gate on GitHub branch protection rules being satisfied:**

This uses the [GitHub check runs API](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference) to check whether all required check runs on the proposed SHA have completed successfully. The `filter=latest` parameter returns only the most recent run for each check name.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: github-required-statuses
spec:
  promotionStrategyRef:
    name: my-promotion-strategy
  key: github-required-statuses
  reportOn: proposed
  descriptionTemplate: "GitHub required statuses: {{ .ReportedSha }}"
  httpRequest:
    urlTemplate: "https://api.github.com/repos/my-org/my-repo/commits/{{ .ReportedSha }}/check-runs?filter=latest"
    method: GET
    headerTemplates:
      Accept: "application/vnd.github+json"
      X-GitHub-Api-Version: "2022-11-28"
    authentication:
      scmAuth: {}
  success:
    when:
      expression: |
        Response.StatusCode == 200 &&
        len(Response.Body.check_runs) > 0 &&
        all(Response.Body.check_runs, # r, r.status == "completed" && (r.conclusion == "success" || r.conclusion == "skipped"))
  mode:
    polling:
      interval: 1m
```

Replace `my-org/my-repo` with your repository's owner and name. The WebRequestCommitStatus uses the same GitHub App credentials as the ScmProvider in the PromotionStrategy — no additional secret is required.

### Active Commit Monitoring

Monitor the currently deployed commit rather than the proposed commit:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: production-health
spec:
  promotionStrategyRef:
    name: my-app
  key: production-health
  descriptionTemplate: "Monitoring production health"
  reportOn: active  # Monitor what's currently deployed
  httpRequest:
    urlTemplate: "https://monitoring.example.com/health/{{ .ReportedSha }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.errorRate < 0.01"
  mode:
    polling:
      interval: 1m  # Continuously monitor active deployment
```

### Complex Response Validation

Evaluating multiple conditions from the HTTP response:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: multi-check
spec:
  promotionStrategyRef:
    name: my-app
  key: multi-check
  descriptionTemplate: "Running comprehensive checks"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/comprehensive-check/{{ .ReportedSha }}"
    method: GET
  success:
    when:
      expression: |
        Response.StatusCode == 200 &&
        Response.Body.securityScan.passed == true &&
        Response.Body.performanceTest.score >= 90 &&
        len(Response.Body.criticalIssues) == 0 &&
        Response.Body.approvals.managerApproval == true &&
        Response.Body.approvals.securityApproval == true
  mode:
    polling:
      interval: 5m
```

### Using Namespace Metadata

Leverage namespace labels and annotations in your requests:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: team-approval
spec:
  promotionStrategyRef:
    name: my-app
  key: team-approval
  descriptionTemplate: "Waiting for {{ index .NamespaceMetadata.Labels \"team\" }} approval"
  httpRequest:
    urlTemplate: "https://approvals.example.com/api/check"
    method: POST
    headerTemplates:
      X-Team-ID: "{{ index .NamespaceMetadata.Labels \"team-id\" }}"
      X-Cost-Center: "{{ index .NamespaceMetadata.Annotations \"cost-center\" }}"
    bodyTemplate: |
      {
        "sha": "{{ .ReportedSha }}",
        "team": "{{ index .NamespaceMetadata.Labels \"team\" }}",
        "environment": "{{ .Environment.Branch }}"
      }
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.approved == true"
  mode:
    polling:
      interval: 2m
```

### Integrating with PromotionStrategy

Configure your PromotionStrategy to use the web request validation as a gate:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: my-app-repo
  proposedCommitStatuses:
    - key: external-approval  # Must match WebRequestCommitStatus.spec.key
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
      proposedCommitStatuses:
        - key: compliance-check  # Additional gate for production only
```

## Field reference

Field-level documentation (required/optional, template variables, expression variables, defaults) is maintained on the API types. Use either:

- **Godoc:** `api/v1alpha1/webrequestcommitstatus_types.go`
- **CLI:** `kubectl explain webrequestcommitstatus.spec` (and drill down, e.g. `kubectl explain webrequestcommitstatus.spec.mode.trigger`)

## Expression Language

WebRequestCommitStatus uses the [expr](https://github.com/expr-lang/expr) library for expression evaluation. The library provides a powerful expression language with familiar syntax.