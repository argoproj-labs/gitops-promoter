# Example: webrequest-shared-gate

Shared deployment gate using `mode.context: promotionstrategy`. A single HTTP request covers all
applicable environments, and the `success.when` expression returns an **object** (not a boolean)
that specifies a per-branch phase.

This is the canonical shape for any "one call, many environments" gate — e.g. a central deployment
system that reports readiness for each environment in a single response.

## What the simulation shows

1. **before-response** — no prior outputs. `success.when` takes its `Response == nil` branch and
   returns `{ defaultPhase: "pending" }` for every environment.
2. **with-response** — a single shared HTTP call is rendered once. `success.when` evaluates the
   mock response's `environments[]` array and returns a per-branch phase. The simulator renders
   **one CommitStatus per environment**, each with its resolved phase and SHA.
3. **after-response** — `Response=nil` again, but the carry-forward `Phase` is preserved. The
   shared evaluation reports an aggregate phase (derived from per-branch phases) and CommitStatuses
   are re-rendered with the carried state.

## Run

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-shared-gate/wrcs.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-shared-gate/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-shared-gate/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-shared-gate/response.yaml
```

Edit `response.yaml` to flip environments between `success` / `pending` / `failure` and re-run to
see per-branch CommitStatus descriptions change.
