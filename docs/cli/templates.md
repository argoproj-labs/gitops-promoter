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
- `promoter templates webrequest` — simulate a `WebRequestCommitStatus` through **two
  reconcile-shaped steps** (plus an optional third) that together exercise every template and
  expression path.

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

Simulates a `WebRequestCommitStatus` through two reconcile-shaped steps (plus an optional third),
each threading derived outputs into the next step (mirroring the real controller's carry-forward
behavior):

1. **reconcile** — first reconcile. The trigger expression is evaluated and the mock HTTP response
   is injected **iff the trigger fires** (or, for polling-mode WRCS, unconditionally — polling has
   no gate). When injected, renders your HTTP URL / body / header templates, runs `response.output`,
   evaluates `success.when` with a non-nil `Response`, runs `success.when.output`, and renders the
   CommitStatus description / URL with the latest outputs. When not injected, behaves as a
   no-request reconcile (`Response = nil`) — exercises your success expression's carry-forward
   branch on the seeded state.
2. **next-reconcile** — second reconcile with state carried forward from "reconcile". The mock
   `Response` is injected under the same rules as step 1 (trigger fires, or polling mode). When the
   trigger is false, you see carry-forward with `Response = nil` (e.g. fingerprint already recorded in
   `TriggerOutput.lastFingerprint`). The trigger is always evaluated and shown in the output.
3. **after-state-change** *(optional, when `--promotion-strategy-updated` or
   `--web-request-updated` is provided)* — a later reconcile after upstream state changed, with
   the updated `PromotionStrategy` and/or `WebRequestCommitStatus` swapped in. Like the default
   steps, the mock is injected iff the trigger re-fires (or polling is configured). Use this to exercise
   scenarios where state changes between reconciles:

    - `--promotion-strategy-updated`: upstream state change (e.g. a new
      `Proposed.Note.DrySha` arrives) — fingerprint-based carry-forward where a bump to
      `success.when.variables.fingerprint` makes the trigger re-fire (if your trigger compares
      against `TriggerOutput.lastFingerprint`), causing a fresh HTTP call and re-evaluation.
    - `--web-request-updated`: the WRCS itself differs next reconcile, typically because the
      **controller wrote back updated status** (e.g. new `Status.Environments[*].TriggerOutput` /
      `ResponseOutput` / `LastRequestTime` / conditions). Templates and expressions that reference
      `.WebRequestCommitStatus.Status.*` see the new values here, while the prior step's
      carry-forward outputs (`TriggerOutput`, `ResponseOutput`, `SuccessOutput`, `Phase`) are still
      propagated unless overridden by a populated `wrcsUpdated.status` seed. Less commonly, this
      also models an admin editing the spec (template tweak, tightened expression).

    Both flags can be used together to simulate both kinds of change arriving in the same
    reconcile.

The `trigger.when.expression` is evaluated on every step and its result is surfaced in the output.
It **gates** whether the mock `Response` is injected on every step (including "next-reconcile"), same
as the controller; polling mode injects unconditionally on every step. The `trigger.when.output` expression runs in every step to match the real controller's
behavior (`when.output` runs every reconcile regardless of whether the request fires) — its result
goes into `status.triggerOutput` for the **next** step, not into the current step's `success.when`
environment (matching how the reconciler persists its status at the end of a reconcile).

### Seeding step state from `wrcs.status`

The simulator mirrors the real controller's behavior of reading persisted status at the start of a
reconcile. If the input WRCS carries a populated `status` block, those values become the initial
`simEnvState` used by the "reconcile" step — exactly as if the controller were reconciling a
resource that already had this status written back by a previous run.

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

The same seeding applies when `--web-request-updated` is provided: the "after-state-change" step
re-seeds from the updated WRCS's status, overriding the prior step's carry-forward for any branch
present in the updated status. Branches not present in `wrcsUpdated.status` keep their prior-step
carry-forward. A cold-start simulation (no `status` block) behaves exactly as before.

### Usage

```bash
promoter templates webrequest \
  --web-request wrcs.yaml \
  --promotion-strategy ps.yaml \
  --namespace-labels ns-labels.yaml \
  --response response.yaml \
  [--promotion-strategy-updated ps-new.yaml] \     # optional; activates after-state-change
  [--web-request-updated wrcs-new.yaml] \          # optional; activates after-state-change
  [--response-updated response-new.yaml] \        # optional; mock for after-state-change only
  [--branch environment/dev] \
  [--output human|yaml|json] \
  [--color auto|always|never]
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

`response.yaml` — the mocked HTTP response injected whenever a step's trigger fires (or on every
step in polling mode):

```yaml
statusCode: 200
body:
  approved: true
  sha: deadbeef
headers:
  Content-Type:
    - application/json
```

`response-updated.yaml` *(optional)* — same shape as `response.yaml`. When you pass
`--promotion-strategy-updated` and/or `--web-request-updated`, you can also pass
`--response-updated` to use this mock **only** for the third `after-state-change` step when it
injects (for example a different `statusCode` or body than the first two steps). If omitted, that
step reuses the primary `--response` file.

`body` is parsed as YAML; use a nested map/list/scalar to match what your real endpoint would return
and what your `response.output` / `success.when` expressions expect. Use a string for non-JSON
endpoints.

### Example output (human)

```
=== Step: reconcile (context=environments) =========================
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

=== Step: next-reconcile (context=environments) ====================
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
  are **skipped** in the simulator; when the trigger fires (or in polling mode), the mock response
  is injected regardless of credentials or allowed hosts.
- Polling-mode `lastRequestTime` / polling interval logic is **not** simulated — the simulator is not
  time-aware. Expressions that use `now()` will be evaluated at the time the command runs. Use
  status seeding + `--web-request-updated` to simulate elapsed-time scenarios.
- By default the same `--response` mock is used for every injecting step. Pass `--response-updated`
  together with `--promotion-strategy-updated` and/or `--web-request-updated` to use a different mock
  for the third step only. You can still combine with `--web-request-updated` status seeding to model
  other intermediate state.
- The simulator uses the WebRequestCommitStatus expression cache for the duration of the command
  only; it is discarded on exit.
