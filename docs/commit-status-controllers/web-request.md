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
4. The controller evaluates the validation expression against the HTTP response
5. The controller creates/updates a CommitStatus with the validation result
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
- Can store and access custom state via `TriggerData`
- Can access previous HTTP response data via `ResponseData`
- Always reconciles at `requeueDuration` interval (default: 1 minute)

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
  expression: "Response.StatusCode == 200 && Response.Body.ready == true"
  mode:
    trigger:
      requeueDuration: 1m
      triggerExpression: |
        {
          trigger: ReportedSha != TriggerData["lastCheckedSha"],
          lastCheckedSha: ReportedSha
        }
```

### Trigger Mode - Conditional on Previous Environment

Only trigger validation once the previous environment is healthy:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: staged-validation
spec:
  promotionStrategyRef:
    name: my-app
  key: staged-validation
  descriptionTemplate: "Waiting for staging environment"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/validate/{{ .ReportedSha }}"
    method: POST
    bodyTemplate: |
      {
        "sha": "{{ .ReportedSha }}",
        "environment": "{{ .Environment.Branch }}"
      }
  expression: "Response.StatusCode == 200"
  mode:
    trigger:
      requeueDuration: 30s
      triggerExpression: |
        len(filter(PromotionStrategy.Status.Environments, {.Branch == "environment/staging"})[0].LastHealthyDryShas) > 0
```

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
  descriptionTemplate: "Progressive validation (attempt {{ index .TriggerData \"attemptCount\" | default 0 }})"
  httpRequest:
    urlTemplate: "https://validation.example.com/api/check/{{ .ReportedSha }}"
    method: GET
  expression: "Response.StatusCode == 200 && Response.Body.validated == true"
  mode:
    trigger:
      requeueDuration: 1m
      triggerExpression: |
        {
          trigger: ResponseData == nil || 
                   ResponseData.status == "retry" || 
                   ResponseData.validated == false,
          attemptCount: (TriggerData["attemptCount"] ?? 0) + 1
        }
      responseExpression: |
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
  httpRequest:
    urlTemplate: "https://api.example.com/comprehensive-check/{{ .ReportedSha }}"
    method: GET
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

## Field Reference

### Template data (all templateable fields)

The following fields support Go templates and all receive the **same template data**:

| Field | Purpose |
|-------|---------|
| `spec.descriptionTemplate` | Commit status description (SCM UI) |
| `spec.urlTemplate` | Commit status target URL (SCM UI) |
| `spec.httpRequest.urlTemplate` | HTTP request URL |
| `spec.httpRequest.headerTemplates` | HTTP request header values (map values) |
| `spec.httpRequest.bodyTemplate` | HTTP request body |

**Available template variables:**

| Variable | Type | Description |
|----------|------|-------------|
| `ReportedSha` | string | Commit SHA being reported on |
| `LastSuccessfulSha` | string | Last SHA that achieved success |
| `Phase` | string | Current phase: `success`, `pending`, or `failure` |
| `PromotionStrategy` | object | Full PromotionStrategy resource |
| `Environment` | object | Current environment status (branch, Proposed, Active, PullRequest, etc.) |
| `NamespaceMetadata.Labels` | map[string]string | Labels on the WebRequestCommitStatus namespace |
| `NamespaceMetadata.Annotations` | map[string]string | Annotations on the namespace |
| `TriggerData` | map[string]any | Custom data from the trigger expression (trigger mode only). Use `{{ index .TriggerData "key" }}` for a value. |
| `ResponseData` | map[string]any | Data from the last HTTP response when using `responseExpression` (trigger mode only). Keys match what your response expression returns (e.g. `approvedCount`, `lastCheckedAt`). |

**When is ResponseData / TriggerData from?**

| Field | When data is from |
|-------|-------------------|
| `spec.descriptionTemplate`, `spec.urlTemplate` | **Latest** — from the most recent HTTP request and trigger evaluation (what you see in status is up to date). |
| `spec.httpRequest.urlTemplate`, `headerTemplates`, `bodyTemplate` | **Previous attempt** — from the last run. These are rendered *before* making the current HTTP request, so they never contain the response from the request being built. |

**TriggerData and ResponseData lifecycle (trigger mode):**

- **TriggerData** comes from the trigger expression when it returns an object: all keys except `trigger` are stored. That map is persisted in `status.environments[].triggerData` and on the next reconciliation is passed back into the trigger expression and into all templates as `TriggerData`. Use it to track state (e.g. last checked SHA, attempt count) and to build description/URL from that state.
- **ResponseData** is set only when `responseExpression` is configured. The expression runs after the HTTP request and its returned map is stored in `status.environments[].responseData`. On the next run it is available to the trigger expression and to description/URL templates as `ResponseData`. Use it to drive retry logic or to show response-derived links/counts in the SCM status.

**Template functions:** All standard [Sprig](https://masterminds.github.io/sprig/) functions except `env`, `expandenv`, and `getHostByName`.

**Examples:**
```yaml
# Description
descriptionTemplate: "{{ .Phase }}: {{ .ResponseData.approvedCount }}/{{ .ResponseData.totalRecordCount }} approved"

# URL
urlTemplate: "https://dashboard.example.com/{{ .Environment.Branch }}/{{ .ReportedSha }}"

# HTTP URL
urlTemplate: "https://api.example.com/check/{{ .ReportedSha }}?env={{ .Environment.Branch }}"

# Header value
headerTemplates:
  X-Sha: "{{ .ReportedSha }}"
  X-Environment: "{{ .Environment.Branch }}"

# Body
bodyTemplate: |
  {"sha": "{{ .ReportedSha }}", "branch": "{{ .Environment.Branch }}"}
```

---

### spec.key

Unique identifier for this validation rule. This key is matched against the PromotionStrategy's `proposedCommitStatuses` or `activeCommitStatuses`.

**Requirements:**
- Must be lowercase alphanumeric with hyphens
- Max 63 characters
- Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`

**Required:** Yes

### spec.reportOn

Specifies which commit SHA to report the CommitStatus on.

**Values:**
- `proposed` (default): Reports on the proposed hydrated commit SHA
- `active`: Reports on the active hydrated commit SHA

**Behavior:**
- When `proposed`: In polling mode, stops polling after success for that SHA
- When `active`: In polling mode, continuously polls even after success (active state can change)

**Optional:** Yes (defaults to `proposed`)

### spec.descriptionTemplate

Human-readable description shown in the SCM provider (GitHub, GitLab, etc.) as the commit status description.

**Templates:** Uses the [template data](#template-data-all-templateable-fields) above (same variables for all templateable fields).

**Optional:** Yes

### spec.urlTemplate

URL template with link to more details, shown in the SCM provider as the commit status target URL.

**Templates:** Uses the [template data](#template-data-all-templateable-fields) above.

**Optional:** Yes

### spec.httpRequest

Defines the HTTP request configuration.

#### spec.httpRequest.urlTemplate

HTTP endpoint to request.

**Templates:** Uses the [template data](#template-data-all-templateable-fields) above.

**Required:** Yes

#### spec.httpRequest.method

HTTP method to use.

**Values:** `GET`, `POST`, `PUT`, `PATCH`

**Required:** Yes

#### spec.httpRequest.headerTemplates

Additional HTTP headers to include. Map key is the header name, value is the header value template.

**Templates:** Uses the [template data](#template-data-all-templateable-fields) above.

**Optional:** Yes

**Example:**
```yaml
headerTemplates:
  Content-Type: "application/json"
  X-Custom-Header: "{{ .ReportedSha }}"
```

#### spec.httpRequest.bodyTemplate

Request body to send.

**Templates:** Uses the [template data](#template-data-all-templateable-fields) above.

**Optional:** Yes

**Example:**
```yaml
bodyTemplate: |
  {
    "sha": "{{ .ReportedSha }}",
    "environment": "{{ .Environment.Branch }}"
  }
```

#### spec.httpRequest.timeout

Maximum time to wait for the HTTP request to complete.

**Default:** `30s`

**Optional:** Yes

#### spec.httpRequest.authentication

Authentication configuration for the HTTP request.

**Optional:** Yes

**Supported methods:**

1. **Basic Authentication** - Username and password
   ```yaml
   authentication:
     basic:
       secretRef:
         name: basic-auth-secret  # Must contain keys: username, password
   ```

2. **Bearer Token** - API keys, JWTs, personal access tokens
   ```yaml
   authentication:
     bearer:
       secretRef:
         name: bearer-token-secret  # Must contain key: token
   ```

3. **OAuth2** - Automatic token management with client credentials flow
   ```yaml
   authentication:
     oauth2:
       tokenURL: "https://auth.example.com/oauth/token"
       scopes: ["read:api"]
       secretRef:
         name: oauth-creds  # Must contain keys: clientID, clientSecret
   ```

4. **TLS** - Mutual TLS with client certificates
   ```yaml
   authentication:
     tls:
       secretRef:
         name: tls-cert  # Must contain keys: tls.crt, tls.key, optionally ca.crt
   ```

### spec.expression

Expression evaluated using the [expr](https://github.com/expr-lang/expr) library against the HTTP response. Must return a boolean value where `true` indicates validation passed.

**Available variables:**
- `Response.StatusCode` (int): HTTP response status code
- `Response.Body` (any): Parsed JSON as map[string]any, or raw string if not JSON
- `Response.Headers` (map[string][]string): HTTP response headers

**Required:** Yes

**Examples:**
```yaml
# Simple status check
expression: "Response.StatusCode == 200"

# Status and body check
expression: "Response.StatusCode == 200 && Response.Body.approved == true"

# Complex validation
expression: |
  Response.StatusCode == 200 &&
  Response.Body.status == "approved" &&
  len(Response.Body.errors) == 0
```

### spec.mode

Controls how the controller polls or triggers HTTP requests. Exactly one of `polling` or `trigger` must be specified.

**Required:** Yes

#### spec.mode.polling

Enables interval-based polling mode.

**Fields:**
- `interval` (duration): How often to retry the HTTP request. Default: `1m`

**Example:**
```yaml
mode:
  polling:
    interval: 2m
```

#### spec.mode.trigger

Enables expression-based triggering mode.

**Fields:**

##### spec.mode.trigger.requeueDuration

How long to wait before requeuing to re-evaluate the trigger expression.

**Default:** `1m`

**Optional:** Yes

##### spec.mode.trigger.triggerExpression

Expression that dynamically controls whether the HTTP request should be made. Evaluated BEFORE each potential HTTP request.

**Return types:**
1. **Boolean:** `true` to make HTTP request, `false` to skip
2. **Object with trigger field:** `{trigger: bool, ...customData}` where additional fields are stored in `TriggerData`

**Available variables:**
- `PromotionStrategy` (PromotionStrategy): full PromotionStrategy spec and status
- `Environment` (EnvironmentStatus): current environment's status
- `Phase` (string): current phase (success/pending/failure)
- `ReportedSha` (string): SHA being validated
- `LastSuccessfulSha` (string): last SHA that achieved success
- `TriggerData` (map[string]any): custom data from previous trigger evaluation
- `ResponseData` (map[string]any): response data from previous HTTP request

**Required:** Yes

**Examples:**
```yaml
# Always trigger (equivalent to polling)
triggerExpression: "true"

# Only trigger when SHA changes
triggerExpression: "ReportedSha != TriggerData['lastCheckedSha']"

# Track state and trigger on changes
triggerExpression: |
  {
    trigger: ReportedSha != TriggerData["trackedSha"],
    trackedSha: ReportedSha
  }
```

##### spec.mode.trigger.responseExpression

Optional expression that extracts and transforms data from the HTTP response before storing it in `ResponseData`. Allows storing only needed fields instead of the entire response.

**Available variables:**
- `Response.StatusCode` (int): HTTP response status code
- `Response.Body` (any): parsed JSON response body
- `Response.Headers` (map[string][]string): HTTP response headers

**Return type:** Must return an object/map that will be stored as `ResponseData`

**Optional:** Yes

**Examples:**
```yaml
# Store specific fields
responseExpression: |
  {
    status: Response.Body.status,
    retryAfter: Response.Body.retryAfter
  }

# Conditional extraction
responseExpression: |
  Response.StatusCode == 200 ?
    {status: "success", data: Response.Body.result} :
    {status: "error", error: Response.Body.error}
```

### Status Fields

The WebRequestCommitStatus resource maintains detailed status for each environment:

```yaml
status:
  environments:
    - branch: environment/development
      reportedSha: abc123def456
      lastSuccessfulSha: abc123def456
      phase: success
      lastRequestTime: "2024-01-15T10:30:00Z"
      lastResponseStatusCode: 200
      triggerData:
        lastCheckedSha: abc123def456
        attemptCount: 3
      responseData:
        status: approved
        approver: john.doe@example.com
```

**Fields:**
- `branch`: Environment branch name
- `reportedSha`: Commit SHA being reported on (based on `reportOn` setting)
- `lastSuccessfulSha`: Last SHA that achieved success status
- `phase`: Current validation phase. This controller sets only `pending` or `success` (never `failure`) based on the validation expression.
- `lastRequestTime`: When the last HTTP request was made
- `lastResponseStatusCode`: HTTP status code from last request
- `triggerData`: Custom data from trigger expression (trigger mode only)
- `responseData`: Extracted response data (trigger mode with `responseExpression` only)

## Expression Language

WebRequestCommitStatus uses the [expr](https://github.com/expr-lang/expr) library for expression evaluation. The library provides a powerful expression language with familiar syntax.