# Example: webrequest-change-management

A production-grade `WebRequestCommitStatus` that opens a change-management record when a promotion
is staged for production. Based on a real-world configuration and trimmed down to the essentials.

This is the most comprehensive of the examples: it exercises nearly every feature at once.

## Features demonstrated

- **`mode.context: promotionstrategy`** with **`mode.trigger`** (not polling).
- **`trigger.when.variables`** — a single shared expression computes every gate input (`hasOpenPR`,
  `allNoteDryShasMatch`, `isNewFingerprint`, `needsRetry`, `preGateNoOpenPR`, `fingerprint`,
  `canonicalNoteDrySha`) once, then both `when.expression` and `when.output.expression` consume
  the result as `Variables.*`.
- **`trigger.when.output`** — persists fingerprint + gate diagnostics to `status.triggerOutput`, so
  the next reconcile can compare `fingerprint` vs. `lastFingerprint` to detect retries and no-ops.
- **`trigger.response.output`** — extracts `id`, `message`, `change_request` from the change-
  management service response into `ResponseOutput`, which the SCM `descriptionTemplate` reads.
- **`success.when.variables`** — computes `fingerprint` independently so the carry-forward branch
  (`Response == nil`) can verify the stored `lastFingerprint` still corresponds to the current
  environments / note dry SHAs. If any branch's dry SHA changes, `fingerprint` changes and success
  decays back to pending, forcing a new request.
- **Complex Go-template body** — iterates `PromotionStrategy.Spec.Environments`, filters by the
  applicable `proposedCommitStatuses` key (global or per-env), matches up `Status.Environments`,
  pulls the proposed note dry SHA + open PR URL per branch, and produces a JSON body with a
  Markdown `description` field (via `toJson`).
- **NamespaceMetadata injection** — `asset_id` is pulled from the namespace label `asset-id` and
  `on_behalf_of` is pulled from the namespace annotation `owner` via
  `{{ index .NamespaceMetadata.Labels "asset-id" }}` and
  `{{ index .NamespaceMetadata.Annotations "owner" }}`. In production these values are typically
  stamped on the namespace by a platform admission controller, so every WRCS in the namespace
  reports against the correct asset and owner without per-resource configuration.
- **Step-4 re-seed via `wrcsUpdated.status`** — `wrcs-step4.yaml` carries the same spec as
  `wrcs.yaml` but seeds step 4's carry-forward `TriggerOutput` / `Phase` / etc. via a populated
  `status` block. Used with `--web-request-updated`, this simulates "the controller wrote back
  status and the next reconcile reads it" without needing a spec change.
- **Per-branch gating** — the key `change-management-open` is configured **only on production** (not
  global), so `preGateNoOpenPR` ensures we only open a CM record once development and staging
  have merged their PRs.

## Simulation steps

1. **before-response** — no `Response` and no prior outputs. `trigger.when.output` runs anyway, so
   `TriggerOutput` is populated with the gate diagnostics. The `descriptionTemplate` shows
   `"pending: open change request for note dry SHA"` because `ResponseOutput` is empty.
2. **with-response** — mock response with `statusCode: 202` and `body.id: "e4c72189-…"`. The body
   template renders (you'll see the 600-char JSON body with headers, environments list, SHAs, and
   a Markdown `description`). `response.output` extracts the change fields. `success.when` flips
   to success. `descriptionTemplate` now shows the change id, status code, start/end times, and
   short description.
3. **after-response** — `Response = nil` again. `success.when.variables` recomputes `fingerprint`
   which still matches `TriggerOutput.lastFingerprint` from step 2, so success is carried forward.
   Every carried-over `ResponseOutput` field remains visible in the SCM `descriptionTemplate`.
4. **after-state-change** *(optional, when `--promotion-strategy-updated` is provided)* — uses the
   `ps-step4.yaml` fixture, where `environments/production.proposed.note.drySha` has advanced to a
   new value. All prior outputs from step 3 are carried forward, but `success.when.variables`
   recomputes `fingerprint` from the new PS and it **no longer matches**
   `TriggerOutput.lastFingerprint` (still the step-2 value). The carry-forward branch returns
   false and `Phase` flips back to `pending`, proving the fingerprint-based invalidation works.

## Run (3 steps, default)

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-change-management/wrcs.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-change-management/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-change-management/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-change-management/response.yaml
```

## Run (4 steps, with state-change)

There are two ways to exercise step 4. Both produce the same end effect (`Phase: success` in
steps 2/3, `Phase: pending` in step 4) via different routes.

### Via `--promotion-strategy-updated` (upstream SHA advances)

Use this when the change you want to simulate is "a new dry SHA arrived upstream." The updated
PromotionStrategy changes `Proposed.Note.DrySha`, which recomputes the fingerprint, which breaks
`success.when`'s carry-forward check.

```bash
go run ./cmd templates webrequest \
  --web-request                cmd/templates/examples/webrequest-change-management/wrcs.yaml \
  --promotion-strategy         cmd/templates/examples/webrequest-change-management/ps.yaml \
  --promotion-strategy-updated cmd/templates/examples/webrequest-change-management/ps-step4.yaml \
  --namespace-labels           cmd/templates/examples/webrequest-change-management/namespace-labels.yaml \
  --response                   cmd/templates/examples/webrequest-change-management/response.yaml
```

### Via `--web-request-updated` (status writeback / re-seeded carry-forward)

Use this when the change you want to simulate is "the controller wrote back status and the next
reconcile sees that new state." The simulator re-seeds step 4's `TriggerOutput` / `ResponseOutput`
/ `SuccessOutput` / `Phase` from `wrcsUpdated.status.promotionStrategyContext` (for
`mode.context: promotionstrategy`) or `wrcsUpdated.status.environments[*]` (for
`mode.context: environments`). This is the natural shape for scenarios like:

- "After the controller recorded lastRequestTime, what does my polling expression do on the next
  reconcile?" — set `triggerOutput.lastRequestTime` to a value in the past.
- "After the controller recorded a fingerprint, what happens if the spec diverges?" — set
  `triggerOutput.lastFingerprint` to a stale value.
- "What if the controller wrote phase=failure?" — set `phasePerBranch[].phase: failure`.

```bash
go run ./cmd templates webrequest \
  --web-request          cmd/templates/examples/webrequest-change-management/wrcs.yaml \
  --web-request-updated  cmd/templates/examples/webrequest-change-management/wrcs-step4.yaml \
  --promotion-strategy   cmd/templates/examples/webrequest-change-management/ps.yaml \
  --namespace-labels     cmd/templates/examples/webrequest-change-management/namespace-labels.yaml \
  --response             cmd/templates/examples/webrequest-change-management/response.yaml
```

The bundled `wrcs-step4.yaml` uses the status route: it carries the same spec as `wrcs.yaml` but
its `status.promotionStrategyContext.triggerOutput.lastFingerprint` is set to a stale value so
step 4's carry-forward success.when returns false and Phase flips to pending.

### Use both together

`--promotion-strategy-updated` and `--web-request-updated` are independent; use both to model a
reconcile where both state changes arrived at once.

## Exercising different paths

- **Flip the response to 500** (`statusCode: 500`, `body.id: ""`) and re-run — success.when stays
  pending. Combined with the real controller logic, `needsRetry` would become true on the next
  reconcile since `Variables.isRetryable` would see the 500.
- **Change `environments/development`'s `proposed.note.drySha`** in `ps.yaml` so it differs from the
  other branches — `allNoteDryShasMatch` becomes false and the trigger expression returns false.
- **Set `environments/staging`'s pull request State to `open`** — `preGateNoOpenPR` becomes false
  (staging is in `lowerSpecs` relative to the production-only gated key), blocking the trigger.
- **Clear `TriggerOutput.lastFingerprint`** (simulated: it is empty in step 1 of this simulation by
  design) vs. populate it — this is what drives the `isNewFingerprint` toggle in the gate.
