# Web Request Commit Status Controller

The Web Request Commit Status controller provides HTTP-based gating for environment promotions. It makes configurable HTTP requests to external endpoints and evaluates the response using expressions to determine if a promotion should proceed.

## Overview

The WebRequestCommitStatus controller enables integration with external systems for promotion decisions. It can query approval systems, monitoring platforms, feature flag services, or any HTTP-accessible endpoint to gate promotions.

### How It Works

For each environment where the WebRequestCommitStatus key is referenced:

1. The controller makes an HTTP request to the configured URL
2. It evaluates the response using an expression (using the [expr](https://expr-lang.org/) library)
3. It creates/updates a CommitStatus based on the evaluation result
4. The CommitStatus phase is set to:
   - `pending` - If the HTTP request failed, or the expression evaluated to `false`
   - `success` - If the expression evaluated to `true`
   - `failure` - If the expression failed to compile

This gating mechanism allows external systems to control when promotions can proceed.

## Example Configurations

### Basic External Approval Check

In this example, we configure an external approval check for all environments:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-approval
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: external-approval
  descriptionTemplate: "External approval check"
  urlTemplate: "https://approvals.example.com/requests/{{ .ReportedSha }}"
  reportOn: proposed
  polling:
    interval: 2m
  httpRequest:
    urlTemplate: "https://api.example.com/approvals/check"
    method: GET
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.approved == true'
```

This configuration:
- Queries an external approval API
- Checks if the response indicates approval
- Reports on the proposed commit SHA
- Provides a clickable link in the SCM provider (GitHub, GitLab) to the approval system UI

### Using Template Variables

Template variables are supported in the `descriptionTemplate`, `urlTemplate`, and `httpRequest` fields (`urlTemplate`, `headerTemplates` keys and values, and `bodyTemplate`).

The following example shows templates in both the commit status URL and the HTTP request URL:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: deployment-check
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: deployment-check
  descriptionTemplate: "Deployment verification for {{ .Branch }}"
  urlTemplate: "https://dashboard.example.com/deployments/{{ .ActiveHydratedSha | trunc 7 }}"
  reportOn: active
  polling:
    interval: 1m
  httpRequest:
    urlTemplate: "https://api.example.com/deployments/{{ .ActiveHydratedSha }}/status"
    method: GET
    headerTemplates:
      Content-Type: application/json
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.status == "healthy"'
```

Available template variables:
- `{{ .Branch }}` - The environment branch name (e.g., "environment/staging")
- `{{ .ProposedHydratedSha }}` - The proposed commit SHA
- `{{ .ActiveHydratedSha }}` - The active/deployed commit SHA
- `{{ .ReportedSha }}` - The commit SHA being reported on (based on `reportOn` setting)
- `{{ .LastSuccessfulSha }}` - Last SHA that achieved success (empty until first success)
- `{{ .Phase }}` - Current phase (success/pending/failure)
- `{{ .NamespaceMetadata.Labels }}` - Map of labels from the namespace where the WebRequestCommitStatus resides
- `{{ .NamespaceMetadata.Annotations }}` - Map of annotations from the namespace where the WebRequestCommitStatus resides

**Note:** For `httpRequest` templates (`urlTemplate`, `headerTemplates`, `bodyTemplate`), `Phase` and `LastSuccessfulSha` reflect the **previous** reconcile's values since the current phase isn't known until after the request completes. For the commit status fields (`descriptionTemplate` and `urlTemplate` at the spec level), they reflect the **current** reconcile's result.

To access specific label or annotation values with simple keys, use dot notation: `{{ .NamespaceMetadata.Labels.environment }}`. For keys containing hyphens or special characters, use the `index` function: `{{ index .NamespaceMetadata.Labels "cost-center" }}` or `{{ index .NamespaceMetadata.Annotations "notification-url" }}`

## Authentication

The WebRequestCommitStatus controller supports multiple authentication methods for securing HTTP requests to external endpoints. All credentials must be stored in Kubernetes secrets and referenced via `secretRef` fields.

### Authentication Methods

#### 1. Basic Authentication

HTTP Basic Authentication encodes username and password as base64 and sends them in the Authorization header.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: basic-auth-example
spec:
  promotionStrategyRef:
    name: my-strategy
  key: approval-check
  httpRequest:
    urlTemplate: "https://api.example.com/check"
    method: GET
    authentication:
      basic:
        secretRef:
          name: my-basic-auth-secret
          usernameKey: username  # Optional, defaults to "username"
          passwordKey: password  # Optional, defaults to "password"
  expression: 'Response.StatusCode == 200'
```

The secret should contain:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-basic-auth-secret
  namespace: default
stringData:
  username: admin
  password: supersecret
```

#### 2. Bearer Token Authentication

Bearer tokens are commonly used for API authentication with API keys, JWTs, or personal access tokens.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: bearer-auth-example
spec:
  promotionStrategyRef:
    name: my-strategy
  key: api-check
  httpRequest:
    urlTemplate: "https://api.example.com/status"
    method: GET
    authentication:
      bearer:
        secretRef:
          name: my-bearer-token
          key: token  # Optional, defaults to "token"
  expression: 'Response.StatusCode == 200'
```

The secret should contain:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-bearer-token
  namespace: default
stringData:
  token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

#### 3. OAuth2 Client Credentials

OAuth2 client credentials flow is ideal for server-to-server authentication. The controller automatically obtains and refreshes access tokens.

**How it works:**
1. Controller requests an access token from the tokenURL using client credentials
2. Token is cached and automatically refreshed when it expires
3. Token is added to requests as `Authorization: Bearer <access-token>`

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: oauth2-example
spec:
  promotionStrategyRef:
    name: my-strategy
  key: oauth-api-check
  httpRequest:
    urlTemplate: "https://api.example.com/secure/endpoint"
    method: GET
    authentication:
      oauth2:
        tokenURL: "https://auth.example.com/oauth/token"
        scopes: ["read:api", "write:api"]
        secretRef:
          name: oauth-credentials
          clientIDKey: client-id      # Optional, defaults to "clientID"
          clientSecretKey: client-secret  # Optional, defaults to "clientSecret"
  expression: 'Response.StatusCode == 200'
```

The secret should contain:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: oauth-credentials
  namespace: default
stringData:
  client-id: my-client-id
  client-secret: my-client-secret
```

**Note:** This uses the OAuth2 client credentials grant type (RFC 6749 Section 4.4). It does NOT support authorization code flow or user-interactive flows.

#### 4. TLS Client Certificate (Mutual TLS)

Mutual TLS (mTLS) authentication uses client certificates to prove identity. This is configured at the transport layer, not as an HTTP header.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: mtls-example
spec:
  promotionStrategyRef:
    name: my-strategy
  key: secure-api-check
  httpRequest:
    urlTemplate: "https://secure-api.example.com/check"
    method: GET
    authentication:
      tls:
        secretRef:
          name: my-client-cert
          certKey: tls.crt   # Optional, defaults to "tls.crt"
          keyKey: tls.key    # Optional, defaults to "tls.key"
          caKey: ca.crt      # Optional, defaults to "ca.crt"
  expression: 'Response.StatusCode == 200'
```

The secret should contain:
```yaml
apiVersion: v1
kind: Secret
type: kubernetes.io/tls  # Or Opaque
metadata:
  name: my-client-cert
  namespace: default
data:
  tls.crt: <base64-encoded-certificate>
  tls.key: <base64-encoded-private-key>
  ca.crt: <base64-encoded-ca-certificate>  # Optional, for custom CAs
```

You can create the secret from certificate files:
```bash
kubectl create secret tls my-client-cert \
  --cert=client.crt \
  --key=client.key \
  -n default

# Optionally add CA certificate
kubectl patch secret my-client-cert -n default \
  --type='json' \
  -p='[{"op":"add","path":"/data/ca.crt","value":"'$(base64 -w0 < ca.crt)'"}]'
```

### Authentication Validation

Only one authentication method can be specified per WebRequestCommitStatus resource. The API will reject resources that specify multiple authentication methods (e.g., both `basic` and `bearer`).

### POST Request with Body

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: validation-check
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: validation-check
  descriptionTemplate: "Validation service check"
  urlTemplate: "https://api.example.com/validate/{{ .ProposedHydratedSha | trunc 7 }}/details"
  reportOn: proposed
  polling:
    interval: 5m
  httpRequest:
    urlTemplate: "https://api.example.com/validate"
    method: POST
    headerTemplates:
      Content-Type: application/json
    bodyTemplate: |
      {
        "proposedSha": "{{ .ProposedHydratedSha }}",
        "activeSha": "{{ .ActiveHydratedSha }}",
        "key": "{{ .Key }}"
      }
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.valid == true'
```

### Using Namespace Labels and Annotations in Templates

Labels and annotations from the namespace where the WebRequestCommitStatus resides can be used in templates to pass organizational metadata and context to external systems:

```yaml
# First, add labels and annotations to your namespace
apiVersion: v1
kind: Namespace
metadata:
  name: production
  labels:
    environment: production
    cost-center: engineering
    team: platform
  annotations:
    slack-channel: "#prod-deployments"
    pagerduty-service: "prod-webapp"
    notification-url: "https://notifications.example.com/prod"
---
# Then reference them in your WebRequestCommitStatus
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: deployment-check
  namespace: production
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: deployment-check
  descriptionTemplate: "Deployment verification for {{ .NamespaceMetadata.Labels.environment }}"
  urlTemplate: "{{ index .NamespaceMetadata.Annotations \"notification-url\" }}/deployments/{{ .ReportedSha | trunc 7 }}"
  reportOn: proposed
  polling:
    interval: 2m
  httpRequest:
    urlTemplate: "https://api.example.com/deployments/validate"
    method: POST
    headerTemplates:
      Content-Type: application/json
      X-Environment: '{{ .NamespaceMetadata.Labels.environment }}'
      X-Team: '{{ .NamespaceMetadata.Labels.team }}'
      X-Cost-Center: '{{ index .NamespaceMetadata.Labels "cost-center" }}'
    bodyTemplate: |
      {
        "sha": "{{ .ProposedHydratedSha }}",
        "namespace": "{{ .Namespace }}",
        "environment": "{{ .NamespaceMetadata.Labels.environment }}",
        "team": "{{ .NamespaceMetadata.Labels.team }}",
        "costCenter": "{{ index .NamespaceMetadata.Labels "cost-center" }}",
        "slackChannel": "{{ index .NamespaceMetadata.Annotations "slack-channel" }}",
        "pagerdutyService": "{{ index .NamespaceMetadata.Annotations "pagerduty-service" }}",
        "notificationUrl": "{{ index .NamespaceMetadata.Annotations "notification-url" }}"
      }
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.approved == true'
```

**Template syntax notes:**
- Simple keys (alphanumeric, no hyphens): `{{ .NamespaceMetadata.Labels.environment }}`
- Keys with hyphens or special characters: `{{ index .NamespaceMetadata.Labels "cost-center" }}`
- Nested in structures: Works in `urlTemplate`, `headerTemplates` values, `bodyTemplate`, and `descriptionTemplate` fields

### Integrating with PromotionStrategy

To use web request gating, configure your PromotionStrategy to check for the commit status key:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  proposedCommitStatuses:
    - key: external-approval
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

## Expression Language

The expression field uses the [expr](https://expr-lang.org/) library. The expression must return a boolean value.

### Available Variables

The expression has access to a `Response` object with the following fields:

- `Response.StatusCode` (int) - The HTTP response status code
- `Response.Body` (any) - The parsed JSON response body (or raw string if not JSON)
- `Response.Headers` (map[string][]string) - The HTTP response headers

### Expression Examples

```yaml
# Simple status code check
expression: 'Response.StatusCode == 200'

# Check JSON body field
expression: 'Response.StatusCode == 200 && Response.Body.approved == true'

# Check string value
expression: 'Response.StatusCode == 200 && Response.Body.status == "approved"'

# Check array length
expression: 'len(Response.Body.errors) == 0'

# Check header presence
expression: 'len(Response.Headers["X-Approval"]) > 0'

# Complex condition
expression: 'Response.StatusCode == 200 && (Response.Body.approved == true || Response.Body.override == true)'
```

## Polling Behavior

The polling behavior depends on the `reportOn` setting and the optional `polling.expression`:

### reportOn: proposed (default)

- Polls until success for a given SHA, then stops polling
- When the SHA changes (detected via PromotionStrategy watch), polling restarts
- Efficient for approval workflows where you only need to check until approved

### reportOn: active

- Polls forever, even after success
- Useful for continuous health monitoring of deployed commits
- The status can transition from success back to pending if the external system state changes

### Trigger Expression (polling.expression)

When `polling.expression` is configured, it overrides the default `reportOn` behavior. The expression is evaluated **before** each HTTP request to decide whether to make the request at all. This provides fine-grained control over when HTTP requests are triggered, enabling:

- Conditional request triggering based on environment state
- Request throttling and rate limiting
- Attempt limiting (e.g., max 10 requests)
- Custom backoff strategies
- State-based decision making

When a trigger expression is configured:
- The controller always requeues at `polling.interval`
- Each reconcile evaluates the expression
- If expression returns `true` → HTTP request is made
- If expression returns `false` → HTTP request is skipped, previous status is reused

See the [Custom Trigger Expression](#custom-trigger-expression) section for detailed examples.

### Custom Trigger Expression

For advanced use cases, you can use `polling.expression` to dynamically control whether HTTP requests should be triggered. This expression is evaluated **before** each HTTP request to determine if the request should be made.

The expression can return:
1. **Boolean**: `true` to make the request, `false` to skip it
2. **Object with 'trigger' field**: `{trigger: true/false, ...customData}` - the `trigger` field controls whether to make the request, and any additional fields are stored and available in the next reconcile as `ExpressionData`

#### Available Variables

The trigger expression has access to:

| Variable | Type | Description |
|----------|------|-------------|
| `PromotionStrategy` | PromotionStrategy | The full PromotionStrategy spec and status |
| `Environment` | EnvironmentStatus | Current environment's status from PromotionStrategy |
| `Branch` | string | Environment branch name |
| `ExpressionData` | map[string]any | Custom data from previous trigger expression evaluation |

**Note:** `PromotionStrategy.Status.Environments` is an ordered array representing the promotion sequence. `Environments[0]` is the first environment (e.g., dev), `Environments[1]` is second (e.g., staging), etc.

**Important:** The trigger expression evaluates **before** the HTTP request, so it does NOT have access to `Response`, `ValidationResult`, or `Phase` from the current reconcile. Use this to control whether to make the request based on environment state or custom tracking logic.

#### Boolean Expression Examples

```yaml
# Always trigger requests (default behavior)
polling:
  expression: "true"

# Only trigger if environment has both proposed and active SHAs
polling:
  expression: 'Environment.Proposed.Hydrated.Sha != "" && Environment.Active.Hydrated.Sha != ""'

# Only trigger for specific branches
polling:
  expression: 'Branch == "environment/production" || Branch == "environment/staging"'

# Trigger when SHA has changed in first environment
polling:
  expression: |
    len(PromotionStrategy.Status.Environments) > 0 &&
    PromotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha != ""
```

#### Object Expression Examples (State Tracking)

Object expressions allow you to track state across reconciles and implement custom logic:

```yaml
# Limit to 10 request attempts
polling:
  interval: 1m
  expression: |
    {
      trigger: (ExpressionData.attempts ?? 0) < 10,
      attempts: (ExpressionData.attempts ?? 0) + 1
    }

# Track dry SHA changes and only trigger when it changes
polling:
  expression: |
    currentDrySha = len(PromotionStrategy.Status.Environments) > 0 ? 
                    PromotionStrategy.Status.Environments[0].Proposed.Dry.Sha : "";
    {
      trigger: currentDrySha != "" && currentDrySha != ExpressionData.trackedSha,
      trackedSha: currentDrySha
    }

# Throttle requests - only trigger if 5 minutes have passed
polling:
  interval: 30s  # Check frequently
  expression: |
    {
      trigger: (ExpressionData.lastRequestTime ?? 0) < (now() - 300),  # 5 minutes
      lastRequestTime: now()
    }

# Progressive backoff - increase delay after each attempt
polling:
  interval: 1m
  expression: |
    attempts = ExpressionData.attempts ?? 0;
    lastTime = ExpressionData.lastRequestTime ?? 0;
    minDelay = attempts * 60;  # 1 min, 2 min, 3 min, etc.
    {
      trigger: (now() - lastTime) >= minDelay,
      attempts: attempts + 1,
      lastRequestTime: now()
    }
```

#### Accessing Previous Environment

To access the previous environment in the promotion sequence:

```yaml
polling:
  expression: |
    currentIdx = findIndex(PromotionStrategy.Status.Environments, {.Branch == Branch});
    # Only trigger if we're not the first environment
    currentIdx > 0 && PromotionStrategy.Status.Environments[currentIdx-1].Active.Hydrated.Sha != ""
```

#### Error Handling

If the trigger expression fails to compile or evaluate, the error is returned to the reconcile loop and will:
- Set the Ready condition to `False` with the error message
- Trigger automatic requeue with exponential backoff
- Make expression errors immediately visible via `kubectl describe`

This ensures broken expressions are immediately visible rather than silently falling back to default behavior.

## Status Fields

The WebRequestCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - environment: environment/development
      proposedHydratedSha: abc123def456
      activeHydratedSha: xyz789ghi012
      reportedSha: abc123def456
      lastSuccessfulSha: abc123def456
      phase: success
      lastRequestTime: "2024-01-15T10:00:00Z"
      response:
        statusCode: 200
      expressionResult: true
      expressionMessage: "Expression evaluated to true"
```

Fields:
- `environment` - The environment being validated
- `proposedHydratedSha` - The proposed commit SHA
- `activeHydratedSha` - The active commit SHA
- `reportedSha` - The SHA where the CommitStatus was reported
- `lastSuccessfulSha` - The last SHA that achieved success
- `phase` - Current gate status (`pending`, `success`, or `failure`)
- `lastRequestTime` - When the last HTTP request was made
- `response.statusCode` - The HTTP response status code from the last request
- `expressionResult` - The boolean result of the expression evaluation

**Note:** Errors during HTTP requests or expression evaluation are reported in the resource's `Ready` condition rather than in the environment status.

## Use Cases

### External Approval System

Integrate with ticketing or approval systems:

```yaml
spec:
  httpRequest:
    urlTemplate: "https://jira.example.com/api/tickets/{{ .ProposedHydratedSha }}/status"
    method: GET
  expression: 'Response.StatusCode == 200 && Response.Body.status == "approved"'
```

### Monitoring Health Check

Verify monitoring shows healthy state:

```yaml
spec:
  descriptionTemplate: "Health check for {{ .Branch }}"
  urlTemplate: "https://grafana.example.com/d/app-health?var-branch={{ .Branch }}"
  reportOn: active
  httpRequest:
    urlTemplate: "https://prometheus.example.com/api/v1/query?query=up{job='myapp'}"
    method: GET
  expression: 'Response.StatusCode == 200 && Response.Body.data.result[0].value[1] == "1"'
```

### Change Management Integration

Integrate with change management systems:

```yaml
spec:
  descriptionTemplate: "Change management approval"
  urlTemplate: "https://servicenow.example.com/change/{{ .ProposedHydratedSha | trunc 7 }}"
  httpRequest:
    urlTemplate: "https://servicenow.example.com/api/changes"
    method: POST
    bodyTemplate: |
      {
        "sha": "{{ .ProposedHydratedSha }}",
        "action": "check_approval"
      }
  expression: 'Response.StatusCode == 200 && Response.Body.change_approved == true'
```
