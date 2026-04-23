# `promoter templates` — offline template and expression playground

The `templates` subcommand renders Go templates and evaluates `expr` expressions from this project
**offline**, using user-supplied YAML fixtures. It has no Kubernetes client and never performs real
HTTP requests — it is a read-only tool for iterating on template and expression configuration without
running the controller.

!!! tip "Ready-to-run examples"
    See [`cmd/templates/examples/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/cmd/templates/examples)
    for complete fixture directories you can run immediately:

    - `pullrequest-basic/` — PR title/description rendering
    - `webrequest-approval/` — external approval workflow (environments context)
    - `webrequest-shared-gate/` — deployment gate with per-branch phases (promotionstrategy context)
    - `webrequest-change-management/` — real-world CM-record workflow with `when.variables`, fingerprint-based carry-forward, and complex body template
    - `webrequest-change-management-approval/` — polling-based approval flow with time-window checks (`shouldTriggerByTime`, record-window filter) demonstrated via the status-seed mechanism

    Each directory has a `README.md` with the exact command to run.

Two subcommands are available:

- `promoter templates pullrequest` — render the `PullRequestTemplate` title and description using a
  `ChangeTransferPolicy` and an optional `PromotionStrategy`.
- `promoter templates webrequest` — simulate a `WebRequestCommitStatus` through **three fixed steps**
  that together exercise every template and expression path.

All subcommands support `--output human|yaml|json` (default `human`).

---

## `promoter templates pullrequest`

Renders the PR title and description. The input is exactly what the controller uses: a
`PullRequestTemplate` (or a full `ControllerConfiguration` that contains one) plus a
`ChangeTransferPolicy` and an optional `PromotionStrategy`.

### Usage

```bash
promoter templates pullrequest \
  --pull-request-template pr-template.yaml \
  --change-transfer-policy ctp.yaml \
  [--promotion-strategy ps.yaml] \
  [--output human|yaml|json]
```

### Fixture shapes

`pr-template.yaml` — either a bare template:

```yaml
title: "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`"
description: |
  Promote to {{ .ChangeTransferPolicy.Spec.ActiveBranch }}
  {{- if .PromotionStrategy }}
  Strategy: {{ .PromotionStrategy.metadata.name }}
  {{- end }}
```

…or a full `ControllerConfiguration` with `spec.pullRequest.template`; the CLI extracts the template
and ignores everything else.

`ctp.yaml` — a standard `ChangeTransferPolicy` manifest. Only the fields referenced by the template
need to be populated; the CLI does not validate the full schema.

`ps.yaml` — optional `PromotionStrategy` manifest, used only when the template references it.

### Example output (human)

```
Title:
  Promote abcdef1 to `environment/dev`

Description:
  Promote to environment/dev
```

---

## `promoter templates webrequest`

Simulates a `WebRequestCommitStatus` through three fixed steps (plus an optional fourth), each
threading derived outputs into the next step (mirroring the real controller's carry-forward
behavior):

1. **before-response** — `Response = nil`; no prior outputs. Exercises your success expression's
   carry-forward branch on a fresh resource, and confirms your CommitStatus templates do not crash
   with empty `ResponseOutput` / `SuccessOutput`.
2. **with-response** — `Response = mock`; prior outputs carried from step 1. Renders your HTTP URL /
   body / header templates, runs `response.output`, evaluates `success.when` with a non-nil
   `Response`, runs `success.when.output`, and renders the CommitStatus description / URL with the
   latest outputs.
3. **after-response** — `Response = nil` again; prior outputs (including `ResponseOutput`) carried
   from step 2. Exercises your carry-forward branch again on a resource that has already seen a
   successful request.
4. **after-state-change** *(optional, when `--promotion-strategy-updated` or
   `--web-request-updated` is provided)* — runs exactly like `after-response` (Response=nil, all
   prior outputs carried from step 3), but swaps in the updated `PromotionStrategy` and/or the
   updated `WebRequestCommitStatus` for that step only. Use this to exercise scenarios where state
   changes between reconciles:

    - `--promotion-strategy-updated`: upstream state change (e.g. a new
      `Proposed.Note.DrySha` arrives) — the canonical case is fingerprint-based carry-forward
      where `success.when.variables.fingerprint` will no longer match
      `TriggerOutput.lastFingerprint` and a previously-successful gate flips back to pending.
    - `--web-request-updated`: the WRCS itself differs next reconcile, typically because the
      **controller wrote back updated status** (e.g. new `Status.Environments[*].TriggerOutput` /
      `ResponseOutput` / `LastRequestTime` / conditions). Templates and expressions that reference
      `.WebRequestCommitStatus.Status.*` see the new values in step 4 while step 3's carry-forward
      outputs (`TriggerOutput`, `ResponseOutput`, `SuccessOutput`, `Phase`) are still propagated.
      Less commonly, this also models an admin editing the spec (template tweak, tightened
      expression) — either way, point the flag at a WRCS fixture that reflects the post-change
      state.

    Both flags can be used together to simulate both kinds of change arriving in the same
    reconcile.

The `trigger.when.expression` is always evaluated and its result is surfaced in the output as
**information only** — it does **not** gate whether the mock `Response` is injected. This is driven
by the step. The `trigger.when.output` expression, however, runs in every step to match the real
controller's behavior (`when.output` runs every reconcile regardless of whether the request fires)
— its result goes into `status.triggerOutput` for the **next** step, not into the current step's
`success.when` environment (matching how the reconciler persists its status at the end of a
reconcile).

### Seeding step state from `wrcs.status`

The simulator mirrors the real controller's behavior of reading persisted status at the start of a
reconcile. If the input WRCS carries a populated `status` block, those values become the initial
`simEnvState` used by step 1 — exactly as if the controller were reconciling a resource that
already had this status written back by a previous run.

Specifically, the simulator extracts `TriggerOutput`, `ResponseOutput`, `SuccessOutput`, `Phase`,
and `PhasePerBranch` from:

- `status.environments[*].*` when `mode.context = environments` (one entry per branch)
- `status.promotionStrategyContext.*` when `mode.context = promotionstrategy` (shared)

Populating this is the supported way to express "the controller already ran N times; here's where
it left off" — useful for testing any status-dependent expression such as:

- Polling-cooldown checks that compare `now()` to `TriggerOutput.lastRequestTime`.
- Fingerprint-drift checks that compare a recomputed fingerprint to `TriggerOutput.lastFingerprint`.
- Carry-forward success branches that gate on `Phase == "success"`.
- Change-window expiry checks that examine timestamps stored in `ResponseOutput`.

The same seeding applies when `--web-request-updated` is provided: step 4 re-seeds from the
updated WRCS's status, overriding step 3's carry-forward for any branch present in the updated
status. Branches not present in `wrcsUpdated.status` keep their step-3 carry-forward. A cold-start
simulation (no `status` block) behaves exactly as before.

### Usage

```bash
promoter templates webrequest \
  --web-request wrcs.yaml \
  --promotion-strategy ps.yaml \
  --namespace-labels ns-labels.yaml \
  --response response.yaml \
  [--promotion-strategy-updated ps-new.yaml] \     # optional; activates step 4
  [--web-request-updated wrcs-new.yaml] \          # optional; activates step 4
  [--branch environment/dev] \
  [--output human|yaml|json]
```

### Fixture shapes

`wrcs.yaml` — a `WebRequestCommitStatus` manifest.

`ps.yaml` — a `PromotionStrategy` manifest. Its `spec.environments`, `spec.proposedCommitStatuses` /
`spec.activeCommitStatuses`, and `status.environments[*].proposed.hydrated.sha` /
`status.environments[*].active.hydrated.sha` must be populated so the simulator can resolve which
environments are applicable for this WebRequestCommitStatus and what SHA to report on.

`ns-labels.yaml` — a plain YAML object exposed to templates as `.NamespaceMetadata`:

```yaml
labels:
  env: dev
annotations:
  owner: platform
```

`response.yaml` — the mocked HTTP response injected in the with-response step:

```yaml
statusCode: 200
body:
  approved: true
  sha: deadbeef
headers:
  Content-Type:
    - application/json
```

`body` is parsed as YAML; use a nested map/list/scalar to match what your real endpoint would return
and what your `response.output` / `success.when` expressions expect. Use a string for non-JSON
endpoints.

### Example output (human)

```
=== Step: before-response (context=environments) ===================
Environment: environment/dev
  Trigger expression (info): true
  Response: nil
  TriggerOutput:  {"trackedBranch":"environment/dev"}
  ResponseOutput: {}
  SuccessOutput:  {}
  Phase: pending
CommitStatuses:
  [environment/dev]
    Sha:         deadbeef…
    Phase:       pending
    Description: waiting for environment/dev
    URL:         https://approvals.example.com/environment/dev

=== Step: with-response (context=environments) =====================
Environment: environment/dev
  Trigger expression (info): true
  Rendered HTTP request:
    Method:  GET
    URL:     https://approvals.example.com/api/check/environment/dev
    Headers:
      X-Branch: environment/dev
  Mock response (applied):
    StatusCode: 200
  TriggerOutput:  {"trackedBranch":"environment/dev"}
  ResponseOutput: {"approved":true,"sha":"deadbeef"}
  Phase: success
CommitStatuses:
  [environment/dev]
    Description: approved (deadbeef)

=== Step: after-response (context=environments) ====================
Environment: environment/dev
  Trigger expression (info): false
  Response: nil
  TriggerOutput:  {"trackedBranch":"environment/dev"}
  ResponseOutput: {"approved":true,"sha":"deadbeef"}
  Phase: success
CommitStatuses:
  [environment/dev]
    Description: approved (deadbeef)
```

### Context modes

- `mode.context: environments` (default) — the simulator iterates every applicable environment (or
  just the one matching `--branch` when set), maintains a separate carry-forward state per
  environment, and renders one CommitStatus per environment in each step.
- `mode.context: promotionstrategy` — the simulator runs a single shared evaluation per step, and
  uses the success expression's `{ defaultPhase?, environments? }` object return value to derive
  `phasePerBranch`. One CommitStatus is rendered per applicable environment in each step, using the
  resolved per-branch phase.

### Structured output

Pass `--output yaml` or `--output json` to emit the result array as structured data. This is useful
for diffing expected vs. actual behavior across commits or for piping into other tools.

---

## Limitations

- No real HTTP calls are made. Authentication (`httpRequest.authentication`) and SCM host validation
  are **skipped** in the simulator; the mock response is always injected in the with-response step
  regardless of credentials or allowed hosts.
- Polling-mode `lastRequestTime` / polling interval logic is **not** simulated — the simulator is not
  time-aware. Expressions that use `now()` will be evaluated at the time the command runs.
- The simulator uses the WebRequestCommitStatus expression cache for the duration of the command
  only; it is discarded on exit.
