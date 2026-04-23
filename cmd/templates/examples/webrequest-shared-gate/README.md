# Example: webrequest-shared-gate

Shared deployment gate using `mode.context: promotionstrategy`. A single HTTP request covers all
applicable environments, and the `success.when` expression returns an **object** (not a boolean)
that specifies a per-branch phase.

This is the canonical shape for any "one call, many environments" gate — e.g. a central deployment
system that reports readiness for each environment in a single response.

## What the simulation shows

This WRCS uses polling mode, which has no trigger gate — so the "reconcile" step always fires.

1. **reconcile** — polling fires → a single shared HTTP call is rendered once. `success.when`
   evaluates the mock response's `environments[]` array and returns a per-branch phase. The
   simulator renders **one CommitStatus per environment**, each with its resolved phase and SHA.
2. **next-reconcile** — polling has no trigger gate, so the mock is injected again (same as the
   controller). `success.when` re-evaluates the HTTP branch with a non-nil `Response`, and
   CommitStatuses are re-rendered with the latest outputs.

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
