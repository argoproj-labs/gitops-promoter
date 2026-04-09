# Web Request Commit Status Controller

The Web Request Commit Status controller enables environment gating based on external HTTP/HTTPS API validation. This controller makes HTTP requests to external systems and evaluates their responses to determine if a promotion should proceed, allowing integration with virtually any external validation system.

## Overview

The WebRequestCommitStatus controller provides flexible validation by calling external HTTP APIs and evaluating the responses. It supports both simple polling and advanced expression-based triggering, making it suitable for a wide range of integration scenarios.

### How It Works

Behavior depends on **`spec.mode.context`** (see [Request scope](#request-scope-environments-vs-promotionstrategy) below). By default (`environments`), the controller runs **one HTTP request per applicable environment**. With `promotionstrategy`, it runs **at most one HTTP request per WebRequestCommitStatus** and maps the result to every applicable environment’s `CommitStatus`.

For each applicable environment (after resolving context):

1. The controller determines which SHA to validate based on the `reportOn` setting:
   - `proposed` (default): Validates the commit that will be promoted
   - `active`: Validates the currently deployed commit
2. The controller evaluates whether to make an HTTP request (polling mode always makes requests, trigger mode evaluates a trigger expression first). In `promotionstrategy` context this decision applies to the **single** shared request for that reconcile.
3. If triggered, the controller makes an HTTP request to the configured endpoint using templated URL, headers, and body (in `promotionstrategy` context there is only one request per fire, not one per environment).
4. The controller evaluates `success.when.expression` **every reconcile** to determine the commit status phase. When an HTTP request was made, `Response` is populated; when no request was made, `Response` is `nil`. The expression also has access to `PromotionStrategy`, `Environment` (environments context), `ResponseOutput`, and other trigger-expression variables — see [Success expression variables](#success-expression-variables) below.
5. The controller creates/updates a **CommitStatus per environment**, each with that environment’s SHA and its own phase when using per-branch results.
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
- Can store and access custom state from success evaluation via `SuccessOutput` (via `success.when.output.expression`)
- Always reconciles at `requeueDuration` interval (default: 1 minute)

## Request scope: environments vs promotionstrategy

Use **`spec.mode.context`** to choose **`environments`** (default) or **`promotionstrategy`**. This field controls **how many HTTP requests** the controller performs per `WebRequestCommitStatus` and **how** the success expression’s result maps to `CommitStatus` resources.

| Value | HTTP requests per WebRequestCommitStatus (when the trigger allows) | `CommitStatus` resources |
|-------|------------------------------------------------------|---------------------------|
| `environments` (default) | One per applicable environment | One per environment; each request uses that environment’s templates and SHA |
| `promotionstrategy` | **One** for the whole `WebRequestCommitStatus` | Still one per environment, but all are updated from the **same** response; each row uses that environment’s `reportOn` SHA |

The controller reconciles when the `WebRequestCommitStatus` or the referenced **`PromotionStrategy`** changes (for example environment SHAs moving), so promotion-strategy context stays in sync with the strategy.

### When to use `promotionstrategy` context

Use it when a **single** external API call represents validation for the whole PromotionStrategy (or a subset of environments that share one backend), and you do not want N identical HTTP calls for N environments. Examples:

- One “release train” or “deployment pipeline” status API keyed by application or repo, not by individual environment branch
- A batch endpoint that returns status for multiple environments in one JSON payload (pair with a success expression that returns [per-branch phases](#success-expression-return-types-promotionstrategy-context))

Keep **`environments`** context when each environment should hit a **different** URL or body (typical `{{ .Branch }}` / SHA-from-PromotionStrategy per call).

### Template and trigger variables (`promotionstrategy` context)

For the **HTTP request** (URL, headers, body), **trigger** `when.expression`, and **trigger** `when.output.expression`, `Branch` is empty (`""`) because there is no single current environment for that one request. Use `PromotionStrategy` (e.g. status environments) for branch-specific values.

**You can use:** `PromotionStrategy` (full spec and status), `WebRequestCommitStatus` (full spec and status snapshot), `Phase` (aggregate of all applicable branches' phases), `TriggerOutput`, `ResponseOutput` (trigger mode), `SuccessOutput`, and `NamespaceMetadata` labels and annotations.

When rendering **CommitStatus** `descriptionTemplate` and `urlTemplate`, the controller sets **`{{ .Branch }}`** to that environment's branch and **`{{ .Phase }}`** to that environment's resolved phase (`success`, `pending`, or `failure`). Use `{{ .Branch }}` with `{{ .PromotionStrategy }}` to look up branch-specific data.

### Success expression variables

The `success.when.expression` is evaluated **every reconcile**, regardless of whether an HTTP request was made. It receives the same variables as `trigger.when.expression` (see `WhenWithOutputSpec` in `api/v1alpha1/webrequestcommitstatus_types.go`), plus `Response`:

| Variable | Type | Description |
|----------|------|-------------|
| `Response` | map or nil | HTTP response from this reconcile's request. `nil` when no request was made. When non-nil: `Response.StatusCode` (int), `Response.Body` (parsed JSON or raw string), `Response.Headers` (map[string][]string). |
| `Branch` | string | The environment branch currently being processed. Empty for the shared HTTP request in `promotionstrategy` context; set per-branch for CommitStatus templates. Use with `PromotionStrategy` or `WebRequestCommitStatus` to look up per-branch data. |
| `Phase` | string | Phase from the previous reconcile (`"success"`, `"pending"`, or `"failure"`). Per-environment in `environments` context; aggregate of all branches in `promotionstrategy` context. |
| `PromotionStrategy` | PromotionStrategy | The full PromotionStrategy spec and status. Use `find(PromotionStrategy.Status.Environments, {.Branch == Branch})` to access per-environment data (e.g. `.Proposed.Hydrated.Sha`). |
| `WebRequestCommitStatus` | WebRequestCommitStatus | The full WebRequestCommitStatus spec and status (snapshot from the previous reconcile). Access per-branch status via `Status.Environments` or `Status.PromotionStrategyContext`. |
| `TriggerOutput` | map[string]any | Custom data from the previous `when.output.expression` evaluation (trigger mode only). |
| `ResponseOutput` | map[string]any | Response data from the previous HTTP request's `response.output.expression` (trigger mode only). |
| `SuccessOutput` | map[string]any | Custom data from the previous `success.when.output.expression` evaluation. |

> [!IMPORTANT]
> Since the expression runs every reconcile, it must handle `Response` being `nil` (no HTTP request this reconcile). Expressions that only reference `Response.*` will error when `Response` is `nil`, causing the reconcile to return an error and requeue. Guard `Response` access:

```
Response != nil ? Response.StatusCode == 200 : Phase == "success"
```

### Success expression return types (`promotionstrategy` context)

**Return types:**

1. **Boolean** — `true`: all applicable environments get phase **success**; `false`: all get **pending** (not failure).
2. **Object** — shape: `{ "defaultPhase"?: "success" \| "pending" \| "failure", "environments"?: [ { "branch": "<branch>", "phase": "..." }, ... ] }`  
   - If `environments` is omitted or empty, every environment gets `defaultPhase` (default **`pending`** if `defaultPhase` is omitted).  
   - If `environments` is non-empty, each listed **branch** gets its `phase`; any applicable environment **not** listed uses `defaultPhase`.

Branch strings must match **`PromotionStrategy.spec.environments[].branch`** for the environments this gate applies to.

### Status shape (`promotionstrategy` context)

Observed state is stored under **`status.promotionStrategyContext`**, not under **`status.environments`** (that slice is used only for `environments` context).

Notable fields:

- **`phasePerBranch`** — list of `{ branch, phase }` entries (Kubernetes list type **map**, merge key **`branch`**) with resolved phase (`success`, `pending`, or `failure`) for every applicable environment. This is always fully populated after reconciliation, regardless of whether the success expression returned a boolean or the object form.
- **`lastSuccessfulShas`** — list of `{ branch, lastSuccessfulSha }` entries (list type **map**, merge key **`branch`**) recording the last SHA that reached **success** for each branch (used for optimizations below)
- **`lastRequestTime`**, **`lastResponseStatusCode`**, **`triggerOutput`**, **`responseOutput`**, **`successOutput`** — same roles as per-environment status in the default context, but stored once for the shared request

In trigger mode, **`triggerOutput`**, **`responseOutput`**, and **`successOutput`** are read from / written to **`promotionStrategyContext`**, not to `status.environments[]`.

### Polling optimization (`reportOn: proposed` only)

When **`mode.polling`** is set, **`reportOn` is `proposed`**, and context is **`promotionstrategy`**, the controller can **skip** issuing a new HTTP request if **every** applicable environment is already **success** and each branch’s current proposed SHA matches the **`lastSuccessfulSha`** value in **`lastSuccessfulShas`** for that branch. It still refreshes `CommitStatus` resources and requeues on the polling interval. This avoids hammering the API when nothing has changed.

### Examples

#### Single boolean — one API for the whole strategy

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: pipeline-gate
spec:
  promotionStrategyRef:
    name: my-app
  key: pipeline-approved
  descriptionTemplate: "Pipeline gate: {{ .Phase }} ({{ .PromotionStrategy.Name }})"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://deployments.example.com/api/v1/apps/{{ .PromotionStrategy.Spec.RepositoryReference.Name }}/pipeline-status"
    method: GET
    authentication:
      bearer:
        secretRef:
          name: deployments-api-token
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.ready == true"
  mode:
    context: promotionstrategy
    polling:
      interval: 2m
```

#### Per-environment phases from one response

Use this when the JSON body lists status per environment branch:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: batch-env-status
spec:
  promotionStrategyRef:
    name: my-app
  key: batch-validation
  descriptionTemplate: "{{ .Phase }} — see runbook"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://orchestrator.example.com/status?app={{ .PromotionStrategy.Spec.RepositoryReference.Name }}"
    method: GET
  success:
    when:
      expression: |
        {
          defaultPhase: "pending",
          environments: [
            { branch: "environment/dev", phase: Response.Body.devOk ? "success" : "pending" },
            { branch: "environment/staging", phase: Response.Body.stagingOk ? "success" : "pending" },
            { branch: "environment/prod", phase: Response.Body.prodOk ? "success" : "pending" }
          ]
        }
  mode:
    context: promotionstrategy
    polling:
      interval: 3m
```

Adjust `branch` values to match your `PromotionStrategy` environment branches. The expression must return a value of the shape described above; the example assumes `Response.Body` fields exist and are booleans.

## Security Considerations

The WebRequestCommitStatus controller renders URLs (and optionally headers and body) from Go templates and makes HTTP requests on behalf of the cluster. Although URLs are typically admin-controlled via CRDs, no validation is performed on the rendered URL. Malicious or misconfigured templates could potentially make requests to internal services, cloud metadata endpoints (e.g. `169.254.169.254`), or other sensitive targets (SSRF risk). The following practices help reduce risk.

**Recommendations for administrators:**

- **Restrict who can create or modify WebRequestCommitStatus resources** using RBAC. Only trusted actors should be able to set or change `spec.httpRequest.urlTemplate` and related fields.
- **Be cautious with templates that include user-controlled or namespace-controlled data.** Template variables such as `NamespaceMetadata.Labels`, `NamespaceMetadata.Annotations`, and data from `TriggerOutput`, `ResponseOutput`, or `SuccessOutput` can influence the rendered URL. If those values are controllable by less-trusted users, they could push the URL toward internal or metadata endpoints.
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
    urlTemplate: "https://approvals.example.com/api/check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
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
  descriptionTemplate: "Validating change request for {{ .Branch }}"
  urlTemplate: "https://dashboard.example.com/changes/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
  httpRequest:
    urlTemplate: "https://api.example.com/v1/changes/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}/status"
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
  descriptionTemplate: "Checking deployment {{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha | trunc 7 }}{{ end }}{{ end }} ({{ .Phase }})"
  httpRequest:
    urlTemplate: "https://monitoring.example.com/api/deployment/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200 && Response.Body.ready == true"
  mode:
    trigger:
      requeueDuration: 1m
      when:
        expression: 'find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["lastCheckedSha"] ?? "")'
        output:
          expression: '{ lastCheckedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }'
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
  descriptionTemplate: "External validation for {{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha | trunc 7 }}{{ end }}{{ end }}"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200"
  mode:
    trigger:
      requeueDuration: 30s
      when:
        expression: |
          let env = find(PromotionStrategy.Status.Environments, {.Branch == Branch});
          any(env.Proposed.CommitStatuses, {.Key == "argocd-health" && .Phase == "success"})
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
    urlTemplate: "https://validation.example.com/api/check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
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

### Trigger Mode with Success Output

Store computed state from the success evaluation that persists across reconcile cycles. `SuccessOutput` is available in trigger expressions, success expressions, and templates on the next reconcile:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: approval-with-metadata
spec:
  promotionStrategyRef:
    name: my-app
  key: approval-with-metadata
  descriptionTemplate: "Approved by {{ index .SuccessOutput \"approver\" | default \"pending\" }}"
  httpRequest:
    urlTemplate: "https://approvals.example.com/api/check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
    method: GET
  success:
    when:
      expression: |
        Response != nil
          ? (Response.StatusCode == 200 && Response.Body.approved == true)
          : (Phase == "success" && (let wrcsEnv = find(WebRequestCommitStatus.Status.Environments ?? [], {.Branch == Branch}); wrcsEnv != nil && find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha == wrcsEnv.LastSuccessfulSha))
      output:
        expression: |
          {
            checkedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha,
            approver: Response != nil ? Response.Body.approver : (SuccessOutput ?? {})["approver"],
            approvedAt: Response != nil ? Response.Body.approvedAt : (SuccessOutput ?? {})["approvedAt"]
          }
  mode:
    trigger:
      requeueDuration: 1m
      when:
        expression: 'SuccessOutput == nil || SuccessOutput["checkedSha"] != find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha'
```

The `success.when.output.expression` runs every reconcile (whether or not an HTTP request was made). Its map result is stored in `status.environments[].successOutput` (or `status.promotionStrategyContext.successOutput` in `promotionstrategy` context) and exposed as `SuccessOutput` in all expressions and Go templates on the next reconcile.

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
  descriptionTemplate: "Checking compliance for {{ .Branch }}"
  httpRequest:
    urlTemplate: "https://compliance.example.com/api/v1/validate"
    method: POST
    headerTemplates:
      Content-Type: "application/json"
      X-Environment: "{{ .Branch }}"
    bodyTemplate: |
      {
        "branch": "{{ .Branch }}",
        "namespace": "{{ index .NamespaceMetadata.Labels \"team\" }}",
        "phase": "{{ .Phase }}"
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
    urlTemplate: "https://api.enterprise.com/v2/deployments/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}/approval"
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
    urlTemplate: "https://secure.internal.company.com/api/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
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

Set `authentication.scm: {}` and the controller will use the credentials from the ScmProvider referenced by the PromotionStrategy's repository. The authentication method is applied automatically based on the SCM provider type (GitHub App, GitLab token, Azure DevOps PAT, etc.).


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
  descriptionTemplate: "GitHub required statuses: {{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha | trunc 7 }}{{ end }}{{ end }}"
  httpRequest:
    urlTemplate: "https://api.github.com/repos/my-org/my-repo/commits/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}/check-runs?filter=latest"
    method: GET
    headerTemplates:
      Accept: "application/vnd.github+json"
      X-GitHub-Api-Version: "2022-11-28"
    authentication:
      scm: {}
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
    urlTemplate: "https://monitoring.example.com/health/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Active.Hydrated.Sha }}{{ end }}{{ end }}"
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
    urlTemplate: "https://api.example.com/comprehensive-check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
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
        "branch": "{{ .Branch }}",
        "team": "{{ index .NamespaceMetadata.Labels \"team\" }}",
        "phase": "{{ .Phase }}"
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

## Best Practices

### Reset to Pending When Proposed Content Changes

When a WebRequestCommitStatus achieves `success`, it stays successful for that SHA until something changes. If new content is pushed (new dry commit → new hydrated commit), the WRCS should go back to `pending` so the external system re-validates the new content before promotion proceeds. Without this, there is a risk of promoting content that was never approved by the external system.

The same applies to `reportOn: active` — when the active content changes, the WRCS should re-validate.

**Key insight:** The dry SHA and hydrated SHA always change together (the dry SHA is embedded in the hydrated commit's `hydrator.metadata`). The controller already tracks `LastSuccessfulSha` (hydrated) per environment in status. So comparing the current hydrated SHA against `LastSuccessfulSha` is sufficient to detect content changes — no separate dry SHA tracking needed.

#### `environments` context

In `environments` context, use `Branch` with `PromotionStrategy` and `WebRequestCommitStatus` to look up the current SHA and last successful SHA. Use trigger mode with a trigger that fires when the SHA changes or the phase is not yet success:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: approval-with-revalidation
spec:
  promotionStrategyRef:
    name: my-app
  key: external-approval
  descriptionTemplate: "External approval: {{ .Phase }}"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://approvals.example.com/api/check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"
    method: GET
    authentication:
      bearer:
        secretRef:
          name: api-token
  success:
    when:
      expression: |
        Response != nil
          ? (Response.StatusCode == 200 && Response.Body.approved == true)
          : (Phase == "success" && (let wrcsEnv = find(WebRequestCommitStatus.Status.Environments ?? [], {.Branch == Branch}); wrcsEnv != nil && find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha == wrcsEnv.LastSuccessfulSha))
  mode:
    trigger:
      requeueDuration: 1m
      when:
        expression: |
          find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["lastCheckedSha"] ?? "") || Phase != "success"
        output:
          expression: |
            { lastCheckedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }
```

How it works:

1. **First commit arrives** — trigger fires (SHA mismatch via PromotionStrategy lookup), HTTP request made, external system approves → `success`. Controller records `LastSuccessfulSha` = current hydrated SHA.
2. **New content pushed** — hydrated SHA changes, trigger fires, HTTP request made. If external system has not approved the new content → `pending`. If already approved → `success`.
3. **No changes, carry-forward reconcile** — trigger does not fire (SHA matches). Success expression runs with `Response` as `nil`. `Phase == "success"` and the current SHA matches `LastSuccessfulSha` from the WRCS status → stays `success` because the SHA hasn't changed.
4. **SHA changed but trigger hasn't fired yet** — success expression runs with `Response` as `nil`. Current SHA != `LastSuccessfulSha` → returns `false` → phase resets to `pending`. On the next reconcile, `Phase != "success"` fires the trigger.
5. **Still pending** — `Phase != "success"` keeps the trigger firing on every requeue until the external system approves.

No `success.when.output` expression is needed. No `SuccessOutput` tracking. The SHA comparison in the carry-forward path ensures the WRCS never stays `success` for a SHA that has been superseded.

> [!IMPORTANT]
> For `reportOn: proposed`, you must use **trigger mode** for this pattern. In polling mode with `reportOn: proposed`, the controller has an optimization that skips the success expression entirely once a SHA has succeeded. Trigger mode does not have this optimization. For `reportOn: active`, polling mode works because the optimization only applies to `reportOn: proposed`.

#### `promotionstrategy` context — per-branch re-validation

In `promotionstrategy` context, `Branch` is empty for the shared HTTP request (there is no single "current environment"). Use the `WebRequestCommitStatus` variable to access the full WRCS status, including `Status.PromotionStrategyContext.LastSuccessfulShas` — a per-branch list of the hydrated SHAs that last achieved success.

Use `find` with `let` to look up each branch's last successful SHA and compare it against the current hydrated SHA from the PromotionStrategy:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: per-branch-revalidation
spec:
  promotionStrategyRef:
    name: my-app
  key: external-approval
  descriptionTemplate: "External approval: {{ .Phase }}"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://approvals.example.com/api/status?app={{ .PromotionStrategy.Spec.RepositoryReference.Name }}"
    method: GET
  success:
    when:
      expression: |
        Response != nil
          ? (Response.StatusCode == 200 && Response.Body.approved == true
              ? { defaultPhase: "success" }
              : { defaultPhase: "pending" })
          : {
              defaultPhase: "pending",
              environments: map(PromotionStrategy.Status.Environments, {
                let env = #;
                let lastSha = find(
                  WebRequestCommitStatus.Status.PromotionStrategyContext.LastSuccessfulShas ?? [],
                  {.Branch == env.Branch}
                );
                {
                  branch: env.Branch,
                  phase: lastSha != nil && lastSha.LastSuccessfulSha == env.Proposed.Hydrated.Sha
                    ? "success" : "pending"
                }
              })
            }
  mode:
    context: promotionstrategy
    trigger:
      requeueDuration: 1m
      when:
        expression: |
          Phase != "success" ||
          any(PromotionStrategy.Status.Environments, {
            let env = #;
            let lastSha = find(
              WebRequestCommitStatus.Status.PromotionStrategyContext.LastSuccessfulShas ?? [],
              {.Branch == env.Branch}
            );
            lastSha == nil || lastSha.LastSuccessfulSha != env.Proposed.Hydrated.Sha
          })
```

How it works:

- **`success.when.expression`**: When `Response` is present and approved, returns `{ defaultPhase: "success" }` so all environments become success. When no response, builds per-branch phases by comparing each branch's current `Proposed.Hydrated.Sha` against the controller's `LastSuccessfulShas`. Branches whose SHA matches stay `"success"`; branches with a new SHA get `"pending"`.
- **`trigger.when.expression`**: Re-fires the HTTP request when any branch's hydrated SHA doesn't match its last successful SHA, or when the aggregate phase isn't success.

No `success.when.output` expression is needed. The controller's existing `LastSuccessfulShas` tracking (accessible via `WebRequestCommitStatus.Status.PromotionStrategyContext.LastSuccessfulShas`) replaces the manual `SuccessOutput` bookkeeping that was previously required.

## Field reference

Field-level documentation (required/optional, template variables, expression variables, defaults) is maintained on the API types. Use either:

- **Godoc:** `api/v1alpha1/webrequestcommitstatus_types.go`
- **CLI:** `kubectl explain webrequestcommitstatus.spec` (and drill down, e.g. `kubectl explain webrequestcommitstatus.spec.mode.trigger`)

## Expression Language

WebRequestCommitStatus uses the [expr](https://github.com/expr-lang/expr) library for expression evaluation. The library provides a powerful expression language with familiar syntax.