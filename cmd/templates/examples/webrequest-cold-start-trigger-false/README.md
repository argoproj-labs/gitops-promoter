# Example: webrequest-cold-start-trigger-false

Minimal `mode.trigger` + `mode.context: promotionstrategy` fixture where **`trigger.when` is false on
the first reconcile**, so the simulator shows **no mock HTTP** on step 1 (`Response = nil`) while
still evaluating `trigger.when.output` and `success.when`.

Use this to compare against examples where the first step fires (e.g. `webrequest-change-management`).

## What you should see

1. **`reconcile`** — `Trigger expression (info): false`. No “Rendered HTTP request” / mock injection
   block (or `ResponseInjected` is false in machine-readable output). `success.when` takes the
   carry-forward branch with `Phase` empty → **pending**. `TriggerOutput` still contains
   `evaluatedAt` and `ready: false` from `trigger.when.output` (output is evaluated even when the
   boolean gate is false).
2. **`next-reconcile`** — same gate in this fixture, so still false → still no HTTP, still **pending**.

To see the HTTP path, change `when.variables` to `{ "ready": true }` and re-run; step 1 will inject
the mock and `success.when` will use the `Response` branch against `response.yaml`.

## Run

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-cold-start-trigger-false/wrcs.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-cold-start-trigger-false/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-cold-start-trigger-false/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-cold-start-trigger-false/response.yaml
```
