# WebRequestCommitStatus

## Overview

The WebRequestCommitStatus controller validates commits by making HTTP requests to external endpoints and evaluating the responses using expressions. This enables integration with external approval systems, change management platforms, custom health checks, and any API-based validation.

### How It Works

For each environment configured in a WebRequestCommitStatus resource:

1. The controller reads the PromotionStrategy to get commit SHAs
2. **In Trigger Mode**: Evaluates the trigger expression to decide if an HTTP request should be made
3. **In Polling Mode**: Makes HTTP requests at the configured interval
4. The controller renders Go templates for URL, headers, and body
5. The controller makes the HTTP request with the configured authentication
6. The controller evaluates the validation expression against the HTTP response
7. The controller creates/updates a CommitStatus with the result
8. The PromotionStrategy checks the CommitStatus before allowing promotion

### Operating Modes

WebRequestCommitStatus supports two operating modes:

#### Polling Mode

Simple interval-based polling. The controller makes HTTP requests at a fixed interval.

**Behavior:**
- When `reportOn: proposed`: Polls until success, then stops polling for that SHA
- When `reportOn: active`: Always polls at the configured interval

**Use cases:**
- External approval systems that you want to check regularly
- Simple health check endpoints
- Systems where you always want to poll

#### Trigger Mode

Expression-based triggering. The controller evaluates an expression **before** making HTTP requests to determine if the request should be made at all.

**Behavior:**
- Evaluates trigger expression before each potential HTTP request
- If expression returns `true` or `{trigger: true}`: Makes the HTTP request
- If expression returns `false` or `{trigger: false}`: Skips the request, requeues for later
- Can persist custom data between reconciles for stateful decisions

**Use cases:**
- Only call expensive APIs when something has changed
- Wait for specific conditions before making requests
- Implement complex polling logic based on PromotionStrategy state

## Example Configurations

### Basic Polling - External Approval System

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-approval
spec:
  promotionStrategyRef:
    name: my-app
  key: approval-check
  descriptionTemplate: "Checking approval for {{ .Environment.Branch }}"
  urlTemplate: "https://approvals.example.com/status/{{ .ReportedSha }}"
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.approvals.example.com/v1/check"
    method: POST
    headerTemplates:
      Content-Type: "application/json"
    bodyTemplate: |
      {
        "sha": "{{ .ReportedSha }}",
        "environment": "{{ .Environment.Branch }}",
        "repository": "{{ .PromotionStrategy.Spec.RepositoryReference.Name }}"
      }
    timeout: 30s
    authentication:
      bearer:
        secretRef:
          name: approval-api-token
  expression: "Response.StatusCode == 200 && Response.Body.status == 'approved'"
  mode:
    polling:
      interval: 1m
```

### Trigger Mode - Only Check When SHA Changes

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: change-management
spec:
  promotionStrategyRef:
    name: my-app
  key: change-ticket
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://servicenow.example.com/api/change/{{ .ReportedSha }}"
    method: GET
    timeout: 30s
    authentication:
      basic:
        secretRef:
          name: servicenow-creds
  expression: "Response.StatusCode == 200 && Response.Body.state == 'approved'"
  mode:
    trigger:
      requeueDuration: 30s
      expression: |
        {
          trigger: ReportedSha != ExpressionData["lastCheckedSha"],
          lastCheckedSha: ReportedSha
        }
```

### OAuth2 Authentication

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: oauth-protected-api
spec:
  promotionStrategyRef:
    name: my-app
  key: api-validation
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/validate/{{ .ReportedSha }}"
    method: GET
    timeout: 30s
    authentication:
      oauth2:
        tokenURL: "https://auth.example.com/oauth/token"
        scopes:
          - read:api
          - write:api
        secretRef:
          name: oauth-credentials
  expression: "Response.StatusCode == 200"
  mode:
    polling:
      interval: 2m
```

### mTLS Authentication

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: mtls-protected-api
spec:
  promotionStrategyRef:
    name: my-app
  key: secure-validation
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://secure-api.example.com/validate"
    method: GET
    timeout: 30s
    authentication:
      tls:
        secretRef:
          name: client-certificate
  expression: "Response.StatusCode == 200 && Response.Body.valid == true"
  mode:
    polling:
      interval: 1m
```

### Advanced Trigger - Wait for Previous Environment Health

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: staged-approval
spec:
  promotionStrategyRef:
    name: my-app
  key: staged-check
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://api.example.com/validate"
    method: GET
    timeout: 30s
  expression: "Response.StatusCode == 200"
  mode:
    trigger:
      requeueDuration: 30s
      # Only trigger when the previous environment (staging) has healthy dry SHAs
      expression: |
        let stagingEnvs = filter(PromotionStrategy.Status.Environments, {.Branch == "environment/staging"});
        len(stagingEnvs) > 0 && len(stagingEnvs[0].LastHealthyDryShas) > 0
```

## Integrating with PromotionStrategy

### As Proposed Commit Status (Gate Promotions)

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: my-app-repo
  proposedCommitStatuses:
    - key: approval-check  # Must match WebRequestCommitStatus.spec.key
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

### As Active Commit Status (Validate Running State)

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: my-app-repo
  activeCommitStatuses:
    - key: health-check  # Must match WebRequestCommitStatus.spec.key
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

### Environment-Specific Validation

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: my-app-repo
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
      proposedCommitStatuses:
        - key: production-approval  # Only for production
```

## Template Variables

All Go templates (URL, headers, body, description, url) have access to the following variables:

| Variable | Type | Description |
|----------|------|-------------|
| `.ReportedSha` | string | The commit SHA being reported on (based on `reportOn` setting) |
| `.LastSuccessfulSha` | string | Last SHA that achieved success for this environment |
| `.Phase` | string | Current phase (`pending`, `success`, `failure`) |
| `.PromotionStrategy` | PromotionStrategy | Full PromotionStrategy object |
| `.Environment` | EnvironmentStatus | Current environment's status |
| `.NamespaceMetadata.Labels` | map[string]string | Namespace labels |
| `.NamespaceMetadata.Annotations` | map[string]string | Namespace annotations |

### Template Examples

```yaml
# Simple SHA in URL
urlTemplate: "https://api.example.com/validate/{{ .ReportedSha }}"

# Access environment branch
urlTemplate: "https://api.example.com/env/{{ .Environment.Branch }}/status"

# Use both proposed and active SHAs
urlTemplate: "https://api.example.com/diff?from={{ .Environment.Active.Hydrated.Sha }}&to={{ .Environment.Proposed.Hydrated.Sha }}"

# Use namespace labels
urlTemplate: "https://api.example.com/validate?asset={{ index .NamespaceMetadata.Labels \"asset-id\" }}"

# Truncate SHA in description
descriptionTemplate: "Checking {{ .ReportedSha | trunc 7 }} on {{ .Environment.Branch }}"
```

## Validation Expression

The validation expression is evaluated using the [expr](https://github.com/expr-lang/expr) library against the HTTP response. It must return a boolean value.

### Available Variables

| Variable | Type | Description |
|----------|------|-------------|
| `Response.StatusCode` | int | HTTP response status code |
| `Response.Body` | any | Parsed JSON as map[string]any, or raw string if not JSON |
| `Response.Headers` | map[string][]string | HTTP response headers |

### Expression Examples

```yaml
# Simple status check
expression: "Response.StatusCode == 200"

# Check JSON field
expression: "Response.StatusCode == 200 && Response.Body.approved == true"

# Check string field value
expression: 'Response.StatusCode == 200 && Response.Body.status == "approved"'

# Check header presence
expression: 'len(Response.Headers["X-Approval"]) > 0'

# Complex logic
expression: |
  Response.StatusCode == 200 && 
  Response.Body.state in ["approved", "auto-approved"] &&
  Response.Body.risk_level != "critical"
```

## Trigger Expression

The trigger expression is evaluated **before** making HTTP requests to determine if the request should be made.

### Available Variables

| Variable | Type | Description |
|----------|------|-------------|
| `ReportedSha` | string | The SHA being validated |
| `LastSuccessfulSha` | string | Last SHA that achieved success |
| `Phase` | string | Phase from previous reconcile |
| `PromotionStrategy` | PromotionStrategy | Full PromotionStrategy object |
| `Environment` | EnvironmentStatus | Current environment's status |
| `ExpressionData` | map[string]any | Custom data from previous evaluation |

### Return Values

The trigger expression can return:

1. **Boolean**: `true` to trigger, `false` to skip
2. **Object with trigger field**: `{trigger: bool, ...customData}` - Custom data is persisted for next evaluation

### Trigger Expression Examples

```yaml
# Always trigger (equivalent to polling)
expression: "true"

# Only trigger when SHA changes
expression: 'ReportedSha != ExpressionData["lastCheckedSha"]'

# Track SHA and only trigger when it changes
expression: |
  {
    trigger: ReportedSha != ExpressionData["trackedSha"],
    trackedSha: ReportedSha
  }

# Trigger based on PR state
expression: |
  Environment.PullRequest != nil && Environment.PullRequest.State == "open"
```

## Authentication

### Secret Requirements

#### Basic Auth

Secret must contain keys `username` and `password`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: basic-auth-creds
type: Opaque
stringData:
  username: myuser
  password: mypassword
```

#### Bearer Token

Secret must contain key `token`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: api-token
type: Opaque
stringData:
  token: your-api-token
```

#### OAuth2 Client Credentials

Secret must contain keys `clientID` and `clientSecret`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: oauth-creds
type: Opaque
stringData:
  clientID: your-client-id
  clientSecret: your-client-secret
```

#### TLS Client Certificate

Secret must contain keys `tls.crt`, `tls.key`, and optionally `ca.crt`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: client-cert
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-cert>
  tls.key: <base64-encoded-key>
  ca.crt: <base64-encoded-ca>  # optional
```

## Field Reference

### spec.promotionStrategyRef

Reference to the PromotionStrategy this applies to.

**Required**

### spec.key

Unique identifier for this validation. Matched against PromotionStrategy's commit status selectors.

**Requirements:**
- Lowercase alphanumeric with hyphens
- Max 63 characters
- Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`

**Required**

### spec.reportOn

Which commit SHA to report the CommitStatus on.

**Values:**
- `proposed` (default): Reports on the proposed hydrated commit SHA
- `active`: Reports on the active hydrated commit SHA

**Default:** `proposed`

### spec.httpRequest

HTTP request configuration.

| Field | Type | Description |
|-------|------|-------------|
| `urlTemplate` | string | URL template (required) |
| `method` | string | HTTP method: GET, POST, PUT, PATCH (required) |
| `headerTemplates` | map[string]string | Header templates (optional) |
| `bodyTemplate` | string | Body template (optional) |
| `timeout` | duration | Request timeout (default: 30s) |
| `authentication` | HttpAuthentication | Authentication config (optional) |

### spec.expression

Validation expression evaluated against HTTP response. Must return boolean.

**Required**

### spec.mode

Operating mode configuration. Exactly one of `polling` or `trigger` must be specified.

#### Polling Mode

```yaml
mode:
  polling:
    interval: 1m  # default
```

#### Trigger Mode

```yaml
mode:
  trigger:
    requeueDuration: 1m  # default
    expression: "..."    # required
```

## Status Fields

```yaml
status:
  environments:
    - branch: environment/development
      reportedSha: abc123def456
      lastSuccessfulSha: abc123def456
      phase: success
      lastRequestTime: "2024-01-15T10:30:00Z"
      lastResponseStatusCode: 200
      expressionData:
        trackedSha: "abc123def456"
  conditions:
    - type: Ready
      status: "True"
      reason: ReconcileComplete
      message: All environments validated successfully
```

## Troubleshooting

### HTTP Request Failures

If HTTP requests are failing:

1. Check the `lastResponseStatusCode` in status
2. Verify the URL template renders correctly
3. Check authentication credentials are valid
4. Ensure the target endpoint is reachable from the cluster

### Expression Evaluation Failures

If expressions are failing:

1. Check the `message` field in status for error details
2. Verify the response body structure matches your expression
3. Test expressions with simpler conditions first
4. Remember that `Response.Body` is `map[string]any` for JSON or `string` for non-JSON

### Trigger Not Firing

If trigger mode isn't making requests:

1. Check `expressionData` in status to see stored state
2. Verify the trigger expression logic
3. Remember that first reconcile has empty `ExpressionData`
