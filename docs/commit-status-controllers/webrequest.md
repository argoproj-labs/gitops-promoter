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
  description: "External approval check"
  reportOn: proposed
  polling:
    interval: 2m
  httpRequest:
    url: "https://api.example.com/approvals/check"
    method: GET
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.approved == true'
```

This configuration:
- Queries an external approval API
- Checks if the response indicates approval
- Reports on the proposed commit SHA

### Using Template Variables

Template variables are supported in the `url`, `headers` (values), and `body` fields of `httpRequest`.

The following example shows templates in the URL:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: deployment-check
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: deployment-check
  description: "Deployment verification"
  reportOn: active
  polling:
    interval: 1m
  httpRequest:
    url: "https://api.example.com/deployments/{{ .ActiveHydratedSha }}/status"
    method: GET
    headers:
      Content-Type: application/json
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.status == "healthy"'
```

Available template variables:
- `{{ .ProposedHydratedSha }}` - The proposed commit SHA
- `{{ .ActiveHydratedSha }}` - The active/deployed commit SHA
- `{{ .Key }}` - The WebRequestCommitStatus key
- `{{ .Name }}` - The WebRequestCommitStatus resource name
- `{{ .Namespace }}` - The WebRequestCommitStatus namespace
- `{{ .Labels }}` - Map of labels from the WebRequestCommitStatus resource
- `{{ .Annotations }}` - Map of annotations from the WebRequestCommitStatus resource

**Note:** To access specific label or annotation values, use the `index` function: `{{ index .Labels "key-name" }}` or `{{ index .Annotations "key-name" }}`

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
  description: "Validation service check"
  reportOn: proposed
  polling:
    interval: 5m
  httpRequest:
    url: "https://api.example.com/validate"
    method: POST
    headers:
      Content-Type: application/json
    body: |
      {
        "proposedSha": "{{ .ProposedHydratedSha }}",
        "activeSha": "{{ .ActiveHydratedSha }}",
        "key": "{{ .Key }}"
      }
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.valid == true'
```

### Using Labels and Annotations in Templates

Labels and annotations from the WebRequestCommitStatus resource can be used in templates to pass metadata:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: deployment-check
  labels:
    team: platform
    env-tier: production
  annotations:
    slack-channel: "#deployments"
    jira-project: "DEPLOY"
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: deployment-check
  description: "Deployment verification with metadata"
  reportOn: proposed
  polling:
    interval: 2m
  httpRequest:
    url: "https://api.example.com/deployments/validate"
    method: POST
    headers:
      Content-Type: application/json
      X-Team: '{{ index .Labels "team" }}'
      X-Slack-Channel: '{{ index .Annotations "slack-channel" }}'
    body: |
      {
        "sha": "{{ .ProposedHydratedSha }}",
        "team": "{{ index .Labels "team" }}",
        "tier": "{{ index .Labels "env-tier" }}",
        "jiraProject": "{{ index .Annotations "jira-project" }}",
        "notificationChannel": "{{ index .Annotations "slack-channel" }}"
      }
    timeout: 30s
  expression: 'Response.StatusCode == 200 && Response.Body.approved == true'
```

**Note:** Use `{{ index .Labels "key-name" }}` or `{{ index .Annotations "key-name" }}` to access specific label/annotation values in templates.

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

## Authentication

The controller supports multiple authentication methods via Kubernetes Secrets.

### Bearer Token

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: api-credentials
type: Opaque
data:
  token: <base64-encoded-token>
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-check
spec:
  # ... other fields ...
  httpRequest:
    url: "https://api.example.com/check"
    method: GET
    authSecretRef:
      name: api-credentials
      type: bearer
```

### Basic Auth

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: api-credentials
type: Opaque
data:
  username: <base64-encoded-username>
  password: <base64-encoded-password>
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-check
spec:
  # ... other fields ...
  httpRequest:
    authSecretRef:
      name: api-credentials
      type: basic
```

### Custom Headers

All keys in the secret become HTTP headers:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: api-credentials
type: Opaque
data:
  X-API-Key: <base64-encoded-key>
  X-Custom-Header: <base64-encoded-value>
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: external-check
spec:
  # ... other fields ...
  httpRequest:
    authSecretRef:
      name: api-credentials
      type: header
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

The polling behavior depends on the `reportOn` setting:

### reportOn: proposed (default)

- Polls until success for a given SHA, then stops polling
- When the SHA changes (detected via PromotionStrategy watch), polling restarts
- Efficient for approval workflows where you only need to check until approved

### reportOn: active

- Polls forever, even after success
- Useful for continuous health monitoring of deployed commits
- The status can transition from success back to pending if the external system state changes

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
    url: "https://jira.example.com/api/tickets/{{ .ProposedHydratedSha }}/status"
    method: GET
  expression: 'Response.StatusCode == 200 && Response.Body.status == "approved"'
```

### Feature Flag Check

Ensure feature flags are enabled before promotion:

```yaml
spec:
  httpRequest:
    url: "https://launchdarkly.example.com/api/flags/enable-new-feature"
    method: GET
  expression: 'Response.StatusCode == 200 && Response.Body.enabled == true'
```

### Monitoring Health Check

Verify monitoring shows healthy state:

```yaml
spec:
  reportOn: active
  httpRequest:
    url: "https://prometheus.example.com/api/v1/query?query=up{job='myapp'}"
    method: GET
  expression: 'Response.StatusCode == 200 && Response.Body.data.result[0].value[1] == "1"'
```

### Change Management Integration

Integrate with change management systems:

```yaml
spec:
  httpRequest:
    url: "https://servicenow.example.com/api/changes"
    method: POST
    body: |
      {
        "sha": "{{ .ProposedHydratedSha }}",
        "action": "check_approval"
      }
  expression: 'Response.StatusCode == 200 && Response.Body.change_approved == true'
```
