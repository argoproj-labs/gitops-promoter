# Notifications

The `PromoterNotification` API lets you deliver a webhook when something notable happens during
promotion — a promotion completed, a gate failed, or a `ChangeTransferPolicy` advanced. A
notification subscribes to one or more **event types**, optionally filters by a **label
selector** on the originating resource, and delivers a (optionally templated, optionally
HMAC-signed) HTTP request to a URL you control.

Notifications are **namespace-scoped**: a `PromoterNotification` only ever matches events from
originating resources in its own namespace.

## Event types

`spec.eventTypes` is the set of events a notification subscribes to (at least one is required).
The enum is intentionally small and stable:

| Event type          | Emitted when                                                              | Source controller     |
|---------------------|---------------------------------------------------------------------------|-----------------------|
| `PromotionComplete` | An environment's active dry SHA advances (a promotion lands).             | PromotionStrategy     |
| `GateFailed`        | A proposed commit-status gate newly enters the `failure` phase.           | PromotionStrategy     |
| `CTPProposed`       | A `ChangeTransferPolicy` proposes a new dry SHA on the proposed branch.   | ChangeTransferPolicy  |
| `CTPActive`         | A `ChangeTransferPolicy`'s active branch advances to a new dry SHA.       | ChangeTransferPolicy  |
| `GateStale`         | *Reserved — not emitted yet (see below).*                                 | —                     |

!!! note "Deliberate gaps"
    - **`GateStale` is reserved and not emitted today.** The enum value exists, but the
      promotion status carries no first-class "staleness" signal to detect (a gate is `pending`,
      `success`, or `failure`). `GateStale` will be wired once such a signal exists; until then a
      notification subscribed to it simply never fires.
    - **There is no PullRequest merge/close event type.** A merged PR's effect — the active
      branch advancing — is already surfaced as `CTPActive` (and as `PromotionComplete` at the
      PromotionStrategy level), so there is no semantic gap. Subscribe to those instead.

    The event taxonomy is an **extensible enum**: future versions may add types, so receivers
    should tolerate values they do not recognize.

## Matching: selector and namespace scope

An event matches a `PromoterNotification` when **all** of the following hold:

1. **Namespace** — the notification is in the same namespace as the originating resource.
2. **Event type** — the event's type is one of `spec.eventTypes`.
3. **Selector** — `spec.selector` (a standard Kubernetes label selector) matches the labels of
   the originating resource (the `PromotionStrategy` / `ChangeTransferPolicy` that produced the
   event). A **nil/omitted selector matches everything** in the namespace. An empty selector also
   matches everything.

A notification whose selector does not match an event delivers **nothing** for that event.

With **zero** `PromoterNotification` resources in a namespace, events for that namespace are a
no-op: nothing is matched and nothing is delivered.

## Delivery

`spec.delivery.webhook` configures the HTTP delivery:

| Field            | Default       | Notes                                                              |
|------------------|---------------|--------------------------------------------------------------------|
| `url`            | *(required)*  | `http(s)://…` endpoint.                                            |
| `method`         | `POST`        | One of `POST`, `PUT`, `PATCH`.                                     |
| `headers`        | *(none)*      | Static headers sent verbatim on every request. **Never put secrets here** — use `signing`. |
| `timeoutSeconds` | `10`          | Per-attempt request timeout (1–300).                              |
| `retry`          | see below     | Retry/backoff policy.                                              |
| `signing`        | *(none)*      | HMAC signing (see [Signing](#signing)).                           |

Every request carries `Content-Type: application/json` (unless overridden) and an
`X-Promoter-Event-Id` header (the stable [event ID](#payload), present on every request). When
signing is configured an `X-Promoter-Signature` header is added too.

Header precedence (lowest → highest) is: the `Content-Type` default, then the
`X-Promoter-Event-Id` header, then your static `headers`, then the `X-Promoter-Signature`
signature header. The signature is set last so a static header can never overwrite the integrity
signature; the event-ID header is set before static headers, so an operator *can* deliberately
override it, but it is always present by default.

### Retry, backoff, and failure handling

`spec.delivery.webhook.retry` controls retries:

| Field         | Default       | Notes                                            |
|---------------|---------------|--------------------------------------------------|
| `maxAttempts` | `3`           | Total attempts **including** the first (1–10).   |
| `backoff`     | `Exponential` | `Fixed` (constant wait) or `Exponential` (doubling). |

Semantics:

- A delivery **succeeds** when an attempt returns an HTTP **2xx** status.
- Between attempts the dispatcher waits `baseBackoff` (1s); with `Exponential` the wait doubles
  after each attempt, capped at 30s.
- The body is rendered and signed **once**, before the first attempt — the same exact bytes are
  re-sent on every retry (so the signature stays valid).
- If all attempts are exhausted without a 2xx, the delivery **fails permanently**: the event is
  **dropped**, `status.totalFailed` is incremented, and the `Ready` condition is set to `False`
  with reason `DeliveryFailing`.
- A payload **render/sign failure** (e.g. a bad `payloadTemplate` or a missing/invalid signing
  Secret) is a permanent **configuration error**: it fails immediately with no HTTP attempts. It
  surfaces as `Ready=False` / reason `DeliveryFailing` with a message like
  `configuration error building request (no attempt made): …`. Treat this as a config to fix, not
  a transient failure to wait out.

> **Best-effort delivery — not durable.** There is **no dead-letter queue, no on-disk spool, and no
> replay.** Once retries are exhausted (or the work queue is saturated — see below), the event is
> gone except for the recorded status, metrics, and logs. If a receiver is unreachable for longer
> than the retry window (~`maxAttempts` × backoff), those notifications are **permanently lost**.
> This is intentional: emitting a notification must never block or fail a promotion. Do not rely on
> notifications for anything that requires guaranteed delivery.

> **Backpressure.** Matched deliveries are placed on a bounded in-memory work queue. If that queue
> stays full past an enqueue timeout (default 30s) — e.g. a slow/wedged receiver backing up the
> workers — further matched deliveries for that event are **dropped** (best-effort) rather than
> stalling the originating reconcile. Drops increment `promoter_notifications_dropped_total` and are
> logged.

> **Per-event forensics live in logs and metrics, not the CR.** `status.lastDelivery` reflects only
> the **most recent** delivery; on a busy notification it is continuously overwritten. To see *which*
> events failed (not just the running `totalFailed` count), use the controller logs and the
> `promoter_notifications_*` metrics — the CR status is a point-in-time snapshot, not a history.

Delivery is **at-least-once**: an attempt may succeed on the receiver but have its response lost,
causing a retry. The promoter does **not** de-duplicate on the sending side — each event carries a
stable event ID so **receivers can de-duplicate**. The event ID is exposed in **three** places, all
carrying the same value:

- the `X-Promoter-Event-Id` request header — the **recommended** dedup key (header names are
  case-insensitive on the wire, so match it case-insensitively);
- the payload body (the default-JSON `eventID` field, or the `{{ .EventID }}` template variable);
- `status.lastDelivery.eventID` on the `PromoterNotification` (for observability).

## Payload

`spec.payloadTemplate` is a **Go `text/template`** (not CEL) rendered against the event to
produce the request body. When it is empty, a stable JSON serialization of the event is sent.

Fields available in the template (and present in the default JSON):

| Template field           | Description                                            |
|--------------------------|--------------------------------------------------------|
| `{{ .Type }}`            | Event type (e.g. `PromotionComplete`).                 |
| `{{ .Object.Name }}`     | Originating resource name.                             |
| `{{ .Object.Namespace }}`| Originating resource namespace.                        |
| `{{ .Object.Kind }}`     | Originating resource kind.                             |
| `{{ .Environment }}`     | Promotion environment (target branch), if any.         |
| `{{ .GateName }}`        | Gate/commit-status key (gate events).                  |
| `{{ .PreviousSha }}`     | Prior git SHA, where a transition is involved.         |
| `{{ .NewSha }}`          | New/current git SHA.                                   |
| `{{ .PreviousState }}` / `{{ .NewState }}` | Prior/new phase, for gate transitions. |
| `{{ .ActiveURL }}` / `{{ .ProposedURL }}` | Links to the active/proposed views, if available. |
| `{{ .Labels.<key> }}`    | A label from the originating resource.                 |
| `{{ .EventID }}`         | Stable, deterministic event identifier (for dedup).    |
| `{{ .OccurredAtRFC3339 }}` | Event time as an RFC3339 string.                     |
| `{{ .Vars.<name> }}`     | Result of a `spec.variables` expression (see below).   |

Unset fields render as empty — templates should tolerate missing values.

### Variables (computed with expr)

Go `text/template` is awkward for building structured data and conditional logic. `spec.variables`
lets you compute values with [expr](https://expr-lang.org) **before** the template runs, then
reference them as `{{ .Vars.<name> }}`. Each entry is a named expression evaluated against the event;
keep the logic in the expressions and keep the template a simple interpolation.

```yaml
spec:
  variables:
    severity: 'event.type == "GateFailed" ? "critical" : "info"'
    shortSha: 'event.newSha[:7]'
    team:     'event.labels["team"]'
  payloadTemplate: |
    {
      "severity": "{{ .Vars.severity }}",
      "text": "{{ .Vars.team }} — {{ .Type }} at {{ .Vars.shortSha }}"
    }
```

The expression environment exposes the event as `event`, with **lower-camelCase string fields** so
natural comparisons type-check (`event.type` is a plain string, not a typed enum):

| In expressions       | Description                                              |
|----------------------|----------------------------------------------------------|
| `event.type`         | Event type as a string (e.g. `"PromotionComplete"`).     |
| `event.object`       | `.namespace` / `.name` / `.kind` / `.apiVersion` / `.uid`. |
| `event.labels`       | Map of the originating resource's labels.                |
| `event.environment`  | Promotion environment (target branch), if any.           |
| `event.gateName`     | Gate/commit-status key (gate events).                    |
| `event.previousSha` / `event.newSha` | Prior / new git SHA.                     |
| `event.previousState` / `event.newState` | Prior / new phase (gate transitions). |
| `event.activeURL` / `event.proposedURL` | Links, if available.                  |
| `eventID`            | Stable, deterministic event identifier (top-level).      |

Notes:

- Variables are evaluated against the **event only** — one variable cannot reference another.
- A variable that fails to compile or evaluate is a **configuration error**: the delivery fails
  immediately (`Ready=False` / `DeliveryFailing`) with no HTTP attempt, just like a bad
  `payloadTemplate`. Fix the expression; it will not succeed on retry.
- When `payloadTemplate` is empty, the evaluated variables are still included under a `vars` object
  in the default JSON payload.

## Signing

When `spec.delivery.webhook.signing.secretRef` is set, the dispatcher HMAC-signs the rendered
body and adds a signature header so the receiver can verify authenticity:

```
X-Promoter-Signature: sha256=<lowercase-hex>
```

where `<lowercase-hex>` is the hex encoding of `HMAC-SHA256(secret, body)`. The secret is read
from the `Key` of the referenced `Secret`, which must be in the **same namespace** as the
`PromoterNotification`.

This is the same shape GitHub uses for `X-Hub-Signature-256`, so existing receiver code
translates easily. To verify, a receiver recomputes `sha256=<hex>` over the **raw request body**
and compares it to the header using a constant-time comparison. A reference verifier lives in
`hack/notification-echo/main.go` (run with `--hmac-secret`).

Slack incoming webhooks do **not** verify an HMAC signature — omit `signing` for those.

Signed or not, every request also carries the `X-Promoter-Event-Id` header (see
[Delivery](#delivery)); a receiver can use it as the idempotency key and, when signing is on,
verify `X-Promoter-Signature` for authenticity.

## Status and conditions

`status` reflects the most recent delivery and cumulative counters:

| Field                          | Meaning                                                          |
|--------------------------------|------------------------------------------------------------------|
| `status.totalDelivered`        | Cumulative successfully delivered events.                        |
| `status.totalFailed`           | Cumulative events that failed permanently after exhausting retries. |
| `status.lastDelivery.time`     | Time of the most recent attempt.                                 |
| `status.lastDelivery.eventID`  | EventID of the most recent attempt.                              |
| `status.lastDelivery.responseStatus` | HTTP status of the most recent attempt (`0` if no response). |
| `status.conditions[Ready]`     | `True` / reason `DeliveryHealthy` on success; `False` / reason `DeliveryFailing` on permanent failure. |

## Metrics

The controller exports these Prometheus counters (labels: `namespace`, `name`, `event_type`):

| Metric                                  | Meaning                                                        |
|-----------------------------------------|----------------------------------------------------------------|
| `promoter_notifications_delivered_total`| Events delivered successfully (an attempt returned 2xx).       |
| `promoter_notifications_failed_total`   | Events that failed permanently after exhausting all retries.   |
| `promoter_notifications_retry_total`    | Delivery retries (each failed attempt followed by another).    |
| `promoter_notifications_dropped_total`  | Matched deliveries dropped before any attempt because the work queue stayed full past the enqueue timeout (backpressure shedding). |

See [Monitoring → Metrics](monitoring/metrics.md).

## Examples

Two ready-to-edit samples live in `config/samples/`:

- `promoter_v1alpha1_promoternotification_slack.yaml` — a **Slack incoming-webhook** subscriber:
  fires on `PromotionComplete`, filters by label selector, no signing, and renders a Slack
  message via `payloadTemplate`.
- `promoter_v1alpha1_promoternotification_audit.yaml` — an **audit-log** subscriber: subscribes
  to several event types, matches all resources (no selector), HMAC-signs each request, uses the
  default JSON payload, and ships with a companion signing `Secret`.

### Slack incoming webhook

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromoterNotification
metadata:
  name: slack-on-promotion
spec:
  eventTypes:
    - PromotionComplete
  selector:
    matchLabels:
      promoter.example/team: payments
  delivery:
    webhook:
      url: https://hooks.slack.com/services/REPLACE/WITH/YOUR-WEBHOOK
  payloadTemplate: |
    {"text": ":rocket: *Promotion complete* for `{{ .Object.Name }}` → *{{ .Environment }}* (`{{ .NewSha }}`)"}
```

### Audit log with signing

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromoterNotification
metadata:
  name: audit-all-events
spec:
  eventTypes: [PromotionComplete, GateFailed, CTPActive, CTPProposed]
  delivery:
    webhook:
      url: https://audit.internal.svc.cluster.local/promoter-events
      signing:
        secretRef:
          name: promoter-audit-signing
          key: hmac-secret
---
apiVersion: v1
kind: Secret
metadata:
  name: promoter-audit-signing
type: Opaque
stringData:
  hmac-secret: replace-with-a-long-random-shared-secret   # e.g. `openssl rand -hex 32`
```

## Try it

`hack/notification-echo/` is a tiny standalone webhook receiver for watching delivery, retry,
permanent-failure, and signature behavior live. It logs every request (method, headers, body) and has
controllable failure modes:

```sh
# Always succeed (200), log every request.
go run ./hack/notification-echo --addr :8080

# Return 500 for the first 2 requests, then 200 — watch retry-then-success.
go run ./hack/notification-echo --fail-first 2

# Always return 500 — watch the dispatcher exhaust retries, then drop the event (TotalFailed++, DeliveryFailing).
go run ./hack/notification-echo --always-fail

# Verify the X-Promoter-Signature header against a shared secret.
go run ./hack/notification-echo --hmac-secret "my-shared-secret"
```

Point a `PromoterNotification` at `http://<host>:8080/` (set `signing.secretRef` to a Secret
whose value matches `--hmac-secret` to see signature verification succeed), then drive a
promotion and watch the echo server's log. Inspect delivery outcomes on the CR with:

```sh
kubectl get promoternotification <name> -o jsonpath='{.status}' | jq
```

and the counters via the metrics endpoint:

```sh
curl -s localhost:9080/metrics | grep promoter_notifications_
```
