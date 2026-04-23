# Example: webrequest-change-management-approval

A polling-based approval workflow using `mode.context: promotionstrategy` + `mode.trigger`. The WRCS
queries a change-management service for approved change records whose time window brackets `now()`
— effectively "is there a currently-active approved change ticket that allows this promotion?"

This is the **most time-sensitive** example: every phase hinges on `now()` comparisons, and the
example is a natural demonstration of the [status-seed mechanism](../../../../docs/cli/templates.md)
for simulating warm reconciles and mid-sequence state changes.

## Features demonstrated

- **Polling trigger with `shouldTriggerByTime`** — fires again once `(now() - lastRequestTime) >= 1m`.
  Demonstrated at step 1 via a seeded `status.…triggerOutput.lastRequestTime: "2020-01-01T00:00:00Z"`.
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
- **Step-4 re-seed** via `--web-request-updated` with a populated `status` block — exercises the
  "controller wrote back status and the next reconcile reads it" path without needing a spec change.
- **NamespaceMetadata injection** — `asset-id` label drives the `urlTemplate` query parameter via
  `{{ index .NamespaceMetadata.Labels "asset-id" }}`.

## What each step does

### Step 1 — before-response (seeded warm reconcile)

The base `wrcs.yaml` has a `status` block that seeds step 1's prior state with
`lastRequestTime: 2020-01-01` and empty `lastFingerprint`. Step 1 computes:

- `isFirstRun = false` (lastRequestTime is set)
- `isNewFingerprint = true` (empty seed vs. current computed fingerprint)
- `shouldTriggerByTime = true` (decades have elapsed since 2020)
- `hasOpenPR = true` (production has an open PR and carries the gated key)
- `allNoteDryShasMatch = true` (all 3 branches share the same dry SHA in `ps.yaml`)
- `preGateNoOpenPR = true` (development and staging have no open PRs)

→ `trigger.when.expression` returns `true` (all gates pass) — info only; step 1 doesn't actually fire.
`success.when` Response=nil branch fails because Phase is empty → **Phase = pending**.

### Step 2 — with-response (mock HTTP injected)

Mock response in `response.yaml` has 2 change records with wide time windows (`start: 2020-01-01`,
`end: 2099-12-31`). `response.output` filters → both records counted as approved
(`approvedCount: 2, totalRecordCount: 2`). `success.when` HTTP path returns true
(`StatusCode=200, len(records)>0, any(records, start <= now <= end)`). → **Phase = success**.

Rendered description template: `success: 2/2 approved in window (last check: …)`.

### Step 3 — after-response (carry-forward)

Response is nil, but Phase=success carries forward from step 2, and `Variables.fingerprint` still
matches `TriggerOutput.lastFingerprint`. → **Phase = success**.

### Step 4 — after-state-change (optional, via `--web-request-updated`)

`wrcs-step4.yaml` re-seeds step 4 with `lastFingerprint: "ancient-fingerprint-…"`. Step 4
recomputes `Variables.fingerprint` from `ps.yaml` (unchanged) → mismatch → carry-forward success.when
returns false → **Phase = pending**.

The same step-4 fixture also seeds `lastRequestTime: 2020-01-01` so `shouldTriggerByTime` remains
visible as true in the trigger info output — demonstrating "cooldown elapsed AND fingerprint
drifted" at once.

## Run (3 steps, default)

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request        cmd/templates/examples/webrequest-change-management-approval/wrcs.yaml \
  --promotion-strategy cmd/templates/examples/webrequest-change-management-approval/ps.yaml \
  --namespace-labels   cmd/templates/examples/webrequest-change-management-approval/namespace-labels.yaml \
  --response           cmd/templates/examples/webrequest-change-management-approval/response.yaml
```

Expected: steps 1 → pending, 2 → success, 3 → success.

## Run (4 steps, with state-change)

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-change-management-approval/wrcs.yaml \
  --web-request-updated cmd/templates/examples/webrequest-change-management-approval/wrcs-step4.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-change-management-approval/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-change-management-approval/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-change-management-approval/response.yaml
```

Expected: steps 1 → pending, 2 → success, 3 → success, 4 → pending.

## Exercising different paths

| What to tweak | Effect |
|---|---|
| Delete the `status:` block in `wrcs.yaml` | Step 1 becomes cold start (`isFirstRun=true`), trigger still fires but via the first-run path rather than cooldown-elapsed |
| Change `lastRequestTime` in `wrcs.yaml.status` to `"<real-wall-clock less than 1m ago>"` | Step 1 sees `shouldTriggerByTime=false` → trigger won't naturally fire on cooldown; still fires via `isFirstRun=false && isNewFingerprint=true` |
| Narrow the `start_time` / `end_time` in `response.yaml` to times in the past | No records match the in-window filter → `approvedCount=0` → step 2 fails, step 3 carry-forward stays pending |
| Remove `pullRequest` from `environments/production` in `ps.yaml` | `hasOpenPR=false` → trigger would not fire naturally (still exercised via the simulator's forced step-2 injection) |
| Set `pullRequest.state: open` on `environments/staging` in `ps.yaml` | `preGateNoOpenPR=false` → trigger would not fire naturally (staging is in `lowerSpecs` for the production-only gated key) |
| In `wrcs-step4.yaml`, change `lastFingerprint` to the current value | Step 4 carry-forward passes → stays success |
| In `wrcs-step4.yaml`, set `lastRequestTime` close to real now | Step 4 info shows `shouldTriggerByTime=false` — simulating "we're still within cooldown when the next reconcile happens" |
