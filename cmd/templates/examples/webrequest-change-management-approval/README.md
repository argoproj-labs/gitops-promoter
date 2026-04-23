# Example: webrequest-change-management-approval

A polling-based approval workflow using `mode.context: promotionstrategy` + `mode.trigger`. The WRCS
queries a change-management service for approved change records whose time window brackets `now()`
— effectively "is there a currently-active approved change ticket that allows this promotion?"

This is the **most time-sensitive** example: every phase hinges on `now()` comparisons, and the
example is a natural demonstration of the [status-seed mechanism](../../../../docs/cli/templates.md)
for simulating warm reconciles and mid-sequence state changes.

## Features demonstrated

- **Trigger with `shouldTriggerByTime`** — fires again once `(now() - lastRequestTime) >= 1m`,
  demonstrated via a seeded `status.…triggerOutput.lastRequestTime: "2020-01-01T00:00:00Z"`.
- **`trigger.when.variables` gate chain** — shared computation of `isFirstRun`, `isNewFingerprint`,
  `shouldTriggerByTime`, `hasOpenPR`, `allNoteDryShasMatch`, `preGateNoOpenPR`, exposed as
  `Variables.*` to both `trigger.when.expression` (fire decision) and
  `trigger.when.output.expression` (persisted state).
- **Time-window record filter** — `response.output` filters `change_records` by
  `date(start_time) <= now() && date(end_time) >= now()`, proving records currently in-window are
  counted as approved.
- **Carry-forward with fingerprint integrity** — `success.when` Response=nil branch checks
  `Phase == "success" && Variables.fingerprint == TriggerOutput.lastFingerprint`, so a fingerprint
  drift invalidates a previously-successful gate.
- **`after-state-change` re-seed** via `--web-request-updated` with a populated `status` block —
  exercises the "controller wrote back status and the next reconcile reads it" path without
  needing a spec change.
- **NamespaceMetadata injection** — `asset-id` label drives the `urlTemplate` query parameter via
  `{{ index .NamespaceMetadata.Labels "asset-id" }}`.

## What each step does

### Step 1 — `reconcile` (seeded warm reconcile, trigger fires)

The base `wrcs.yaml` has a `status` block that seeds prior state with
`lastRequestTime: 2020-01-01` and empty `lastFingerprint`. The `reconcile` step computes:

- `isFirstRun = false` (lastRequestTime is set)
- `isNewFingerprint = true` (empty seed vs. current computed fingerprint)
- `shouldTriggerByTime = true` (decades have elapsed since 2020)
- `hasOpenPR = true` (production has an open PR and carries the gated key)
- `allNoteDryShasMatch = true` (all 3 branches share the same dry SHA in `ps.yaml`)
- `preGateNoOpenPR = true` (development and staging have no open PRs)

→ `trigger.when.expression` returns `true` (all gates pass) → mock HTTP response is injected.
`response.output` filters `change_records` by time window → both records counted as approved.
`success.when` HTTP path returns true (`StatusCode=200, len(records)>0, any(records, start <= now <= end)`)
→ **Phase = success**. Rendered description template:
`success: 2/2 approved in window (last check: …)`.

### Step 2 — `next-reconcile` (carry-forward)

`Response = nil` because the trigger is false. Phase = success carries forward from `reconcile`, and
`Variables.fingerprint` still matches `TriggerOutput.lastFingerprint`. `shouldTriggerByTime` is
now false (the `reconcile` step's `when.output` persisted a fresh `lastRequestTime`). The trigger
info shows false. → **Phase = success**.

### Step 3 — `after-state-change` (optional, via `--web-request-updated`)

`wrcs-step4.yaml` re-seeds prior state with `lastFingerprint: "ancient-fingerprint-…"` and
`lastRequestTime: 2020-01-01`. `Variables.fingerprint` recomputed from `ps.yaml` (unchanged) →
mismatch → `isNewFingerprint = true` AND `shouldTriggerByTime = true` → trigger fires again → mock
response is injected again → `success.when` HTTP path → **Phase = success** (records still in
window).

Use this to demonstrate "cooldown elapsed AND fingerprint drifted — controller re-fires fresh HTTP
to re-check approval state" at once.

## Run (2 steps, default)

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request        cmd/templates/examples/webrequest-change-management-approval/wrcs.yaml \
  --promotion-strategy cmd/templates/examples/webrequest-change-management-approval/ps.yaml \
  --namespace-labels   cmd/templates/examples/webrequest-change-management-approval/namespace-labels.yaml \
  --response           cmd/templates/examples/webrequest-change-management-approval/response.yaml
```

Expected: reconcile → success, next-reconcile → success.

## Run (3 steps, with state-change)

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-change-management-approval/wrcs.yaml \
  --web-request-updated cmd/templates/examples/webrequest-change-management-approval/wrcs-step4.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-change-management-approval/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-change-management-approval/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-change-management-approval/response.yaml
```

Expected: reconcile → success, next-reconcile → success, after-state-change → success (fresh HTTP
call driven by the stale fingerprint; approvals are still in window).

## Exercising different paths

| What to tweak | Effect |
|---|---|
| Delete the `status:` block in `wrcs.yaml` | `reconcile` becomes cold start (`isFirstRun=true`), trigger fires via the first-run path rather than cooldown-elapsed |
| Change `lastRequestTime` in `wrcs.yaml.status` to `"<real-wall-clock less than 1m ago>"` | `shouldTriggerByTime=false` → trigger won't fire on cooldown; still fires via `isFirstRun=false && isNewFingerprint=true`. If you also seed a matching `lastFingerprint`, the trigger won't fire at all and no mock response is injected |
| Narrow the `start_time` / `end_time` in `response.yaml` to times in the past | No records match the in-window filter → `approvedCount=0` → `success.when` HTTP path fails → Phase = pending |
| Remove `pullRequest` from `environments/production` in `ps.yaml` | `hasOpenPR=false` → trigger does not fire → `reconcile` step shows `Response: nil` (no HTTP call) |
| Set `pullRequest.state: open` on `environments/staging` in `ps.yaml` | `preGateNoOpenPR=false` → trigger does not fire (staging is in `lowerSpecs` for the production-only gated key) |
| In `wrcs-step4.yaml`, change `lastFingerprint` to the current value AND `lastRequestTime` close to real now | `after-state-change` trigger returns false → no re-fire → carry-forward success.when passes → stays success |
| In `wrcs-step4.yaml`, set `lastRequestTime` close to real now (keep stale `lastFingerprint`) | `shouldTriggerByTime=false` but `isNewFingerprint=true` → trigger still fires via the fingerprint path; simulating "cooldown not elapsed but state drifted, so we re-check anyway" |
