# Scheduled Commit Status Controller

The Scheduled Commit Status controller provides calendar-based gating for environment promotions. It uses cron expressions with durations to define recurring time windows during which promotions are allowed or blocked. This is useful for implementing deployment freezes, business-hours-only rollouts, or maintenance-window policies.

## Overview

The ScheduledCommitStatus controller evaluates cron-based time windows and automatically creates CommitStatus resources that act as proposed commit status gates on the PromotionStrategy.

### How It Works

For each environment configured in a ScheduledCommitStatus resource:

1. The controller resolves the timezone for each window (per-window override, then global `spec.timezone`, then UTC)
2. It merges global windows (from `spec.allow`/`spec.exclude`) with per-environment windows
3. It evaluates which windows are currently active:
   - If inside any **exclusion** window -> `pending` (blocked), regardless of allow windows
   - If no allow windows are defined -> `success` (exclusion-only mode: open unless excluded)
   - If inside any **allow** window -> `success` (allowed)
   - If outside all allow windows -> `pending` (blocked)
4. It creates/updates a CommitStatus for the environment's proposed SHA
5. It requeues at the next window transition time for precise state changes

Environments **not listed** in `spec.environments` get no CommitStatus from this controller. That is **not** the same as defaulting to success. A gate key your PromotionStrategy requires for an environment stays `pending` (`Waiting for status to be reported`) until a matching CommitStatus exists, so the promotion never merges. An unlisted environment is ungated only when `spec.key` is also absent from the checks the PromotionStrategy requires for it -- in particular, keep the key out of the top-level `spec.proposedCommitStatuses`, which applies to every environment. See [Requiring the key only where the window applies](#requiring-the-key-only-where-the-window-applies).

## Example Configurations

### Business-Hours Deployments

Gate development to business hours on weekdays and staging to a weekly window:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: promotion-window
  promotionStrategyRef:
    name: webservice-tier-1
  timezone: America/New_York
  environments:
    - branch: environment/development
      allow:
        - cron: "0 9 * * 1-5"
          duration: 8h
          description: Business hours Mon-Fri
    - branch: environment/staging
      allow:
        - cron: "0 10 * * 2"
          duration: 6h
          description: Tuesday deployment window
```

This configuration:

- Allows deployments to `development` Monday-Friday 09:00-17:00 Eastern
- Allows deployments to `staging` on Tuesdays 10:00-16:00 Eastern
- Does not write a CommitStatus for `production`. Production is ungated only if the PromotionStrategy does not require `promotion-window` there -- declare the key under `development` and `staging` only (see below).

### Global Exclusions (Deployment Freeze)

Use global `spec.exclude` windows to block all listed environments during known freeze periods:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: promotion-window
  promotionStrategyRef:
    name: webservice-tier-1
  exclude:
    - cron: "0 0 25 12 *"
      duration: 48h
      description: Holiday deployment freeze
  environments:
    - branch: environment/development
      allow:
        - cron: "0 6 * * 1-5"
          duration: 12h
    - branch: environment/staging
      allow:
        - cron: "0 9 * * 2"
          duration: 8h
```

This configuration:

- Blocks all listed environments for 48 hours starting December 25th (holiday freeze)
- The global exclusion applies to every listed environment in addition to their per-environment windows
- Exclusions always take precedence over allow windows

### Exclusion-Only Mode

If you only want to block deployments during certain periods (e.g. maintenance windows) but allow them at all other times, define only `exclude` windows with no `allow` windows:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: promotion-window
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/production
      exclude:
        - cron: "0 2 * * 0"
          duration: 4h
          description: Sunday maintenance window
```

This configuration:

- Blocks production deployments during the Sunday 02:00-06:00 UTC maintenance window
- Allows deployments at all other times

### `spec.key`

`spec.key` is the gate name your PromotionStrategy checks in `proposedCommitStatuses`. This field is required -- set it to match the key referenced in your PromotionStrategy.

### Requiring the key only where the window applies

In a PromotionStrategy, keys under the top-level `spec.proposedCommitStatuses` / `spec.activeCommitStatuses` are required by **every** environment; keys under `spec.environments[].proposedCommitStatuses` / `spec.environments[].activeCommitStatuses` are required by that environment only. A required key whose CommitStatus is missing is not skipped -- the promotion waits on it.

So the branches listed on `ScheduledCommitStatus.spec.environments` must be the same environments whose PromotionStrategy entries require `spec.key`:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      proposedCommitStatuses:
        - key: promotion-window
    - branch: environment/staging
      proposedCommitStatuses:
        - key: promotion-window
    - branch: environment/production
      # promotion-window not required here, so the window never blocks production
```

Declaring `promotion-window` at the top level instead makes production require it too, and production then waits forever for a CommitStatus this controller never creates.

### Integrating with PromotionStrategy

`spec.key` must match the `proposedCommitStatuses` entry you declare on the PromotionStrategy. When the ScheduledCommitStatus lists only a subset of branches, declare that key under those environments only, as in [Requiring the key only where the window applies](#requiring-the-key-only-where-the-window-applies). Declare it at the top level only when every environment in the strategy is listed on the ScheduledCommitStatus.

The gate is a proposed commit status: it controls whether a pending change may merge into each environment that declares the key.

### Complete Example with Multiple Gates

Combine promotion windows with other commit status checks:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  activeCommitStatuses:
    - key: argocd-health
  environments:
    - branch: environment/development
      activeCommitStatuses:
        - key: timer
      proposedCommitStatuses:
        - key: promotion-window
    - branch: environment/staging
      activeCommitStatuses:
        - key: timer
      proposedCommitStatuses:
        - key: promotion-window
    - branch: environment/production
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: timer
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      duration: 1h
    - branch: environment/staging
      duration: 4h
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  key: promotion-window
  promotionStrategyRef:
    name: webservice-tier-1
  timezone: America/New_York
  exclude:
    - cron: "0 0 25 12 *"
      duration: 48h
      description: Holiday deployment freeze
  environments:
    - branch: environment/development
      allow:
        - cron: "0 9 * * 1-5"
          duration: 8h
          description: Business hours Mon-Fri
    - branch: environment/staging
      allow:
        - cron: "0 10 * * 2"
          duration: 6h
          description: Tuesday deployment window
```

This configuration requires:

- Argo CD health checks passing in all environments (`argocd-health` is at the root; ArgoCDCommitStatus writes a CommitStatus for each environment)
- 1-hour soak in development and 4-hour soak in staging before promoting *out* of those environments (`timer` is declared only where TimedCommitStatus lists the branch)
- Development proposed commits only during business hours (Mon-Fri 09:00-17:00 ET)
- Staging proposed commits only on Tuesdays 10:00-16:00 ET
- A 48-hour deployment freeze over Christmas for the listed window environments
- Production is not gated by `promotion-window` or soak *on production itself*. Promotions *into* production still wait on staging's soak and health via the ordering gate, because those are active statuses on staging.

## Global vs Per-Environment Windows

Windows can be defined at two levels:

- **Global** (`spec.allow` / `spec.exclude`): Applied to all listed environments
- **Per-environment** (`spec.environments[].allow` / `spec.environments[].exclude`): Applied only to that environment

Global and per-environment windows are **merged** with OR semantics for allow windows (any match allows) and OR semantics for exclude windows (any match blocks). Exclusions always take precedence over allow windows.

An environment with no per-environment windows is valid as long as global windows are defined. The CEL validation rule enforces that each environment has at least one allow or exclude window, either per-environment or global.

### Environment Fields

Each entry in `spec.environments` requires:

- **`branch`** (required) -- The environment branch to gate. Must match a branch defined in the referenced PromotionStrategy -- the controller validates this and sets Ready=False if a branch is not found. Branch is the list map key for Strategic Merge Patch (e.g. Kustomize overlays), allowing specific entries to be targeted for patching without replacing the entire list.

Branch names must be unique across environments within the same ScheduledCommitStatus.

### CronWindow Fields

Each entry in `allow` and `exclude` window lists accepts:

- **`cron`** (required) -- A 5-field cron expression defining when the window starts.
- **`duration`** (required) -- How long the window remains active after each cron trigger (Go duration string).
- **`description`** (optional) -- Human-readable explanation of the window (e.g. "Business hours Mon-Fri", "Holiday freeze"). Useful for understanding what is blocking or allowing a promotion.
- **`timezone`** (optional) -- IANA timezone name for evaluating this specific cron expression. Overrides `spec.timezone` for this window only. See [Timezones](#timezones).

## Cron Expressions

The `cron` field accepts standard 5-field cron expressions:

```text
+------------------- minute (0-59)
| +--------------- hour (0-23)
| | +----------- day of month (1-31)
| | | +------- month (1-12)
| | | | +--- day of week (0-6, Sun=0)
| | | | |
* * * * *
```

Examples:

| Expression       | Meaning                              |
|------------------|--------------------------------------|
| `0 9 * * 1-5`   | Monday-Friday at 09:00               |
| `0 10 * * 2`    | Every Tuesday at 10:00               |
| `0 0 1 * *`     | First day of every month at midnight |
| `0 0 25 12 *`   | December 25th at midnight            |
| `30 6 * * *`    | Every day at 06:30                   |

## Duration Formats

The `duration` field accepts standard Go duration strings:

- `30m` -- 30 minutes
- `1h` -- 1 hour
- `2h30m` -- 2 hours and 30 minutes
- `8h` -- 8 hours
- `48h` -- 48 hours

## Timezones

Timezone configuration follows a global-plus-per-window merge pattern:

- **`spec.timezone`** -- Global default timezone for all cron expressions (defaults to `UTC` if omitted)
- **`cronWindow.timezone`** -- Per-window override, applied to that specific cron expression only

If neither is set, `UTC` is used. A per-window `timezone` overrides the global `spec.timezone` for that window only. This applies equally to global windows (`spec.allow`/`spec.exclude`) and per-environment windows — all windows resolve timezone the same way.

This pattern allows defining windows in different timezones within the same environment. For example, an environment might have a European exclusion window evaluated in `Europe/Paris` and a US exclusion window evaluated in `America/New_York`:

```yaml
environments:
  - branch: environment/production
    exclude:
      - cron: "0 2 * * 0"
        duration: 4h
        timezone: Europe/Paris
        description: EU maintenance window
      - cron: "0 2 * * 0"
        duration: 4h
        timezone: America/New_York
        description: US maintenance window
```

Common IANA timezone values:

| Timezone              | Description        |
|-----------------------|--------------------|
| `UTC`                 | Coordinated Universal Time |
| `America/New_York`    | US Eastern         |
| `America/Los_Angeles` | US Pacific         |
| `Europe/London`       | UK                 |
| `Europe/Paris`        | Central Europe     |
| `Asia/Tokyo`          | Japan              |

## Behavior Details

### Precedence Rules

1. **Exclusions override allow windows.** If the current time is inside any exclusion window (global or per-environment), the promotion is blocked regardless of allow windows.
2. **Allow windows use OR semantics.** If the current time is inside any allow window (global or per-environment), the promotion is allowed (unless excluded).
3. **No allow windows = exclusion-only mode.** When no allow windows are defined, promotions are allowed at all times except during exclusion windows.
4. **Unlisted environments get no CommitStatus.** The controller does not write a status for branches omitted from `spec.environments`. If the PromotionStrategy still requires that `spec.key` for such an environment -- including through a top-level `proposedCommitStatuses` entry, which applies to every environment -- the promotion stays `pending` (`Waiting for status to be reported`) and never merges. Omit the key from environments you do not want gated.

### Intelligent Reconciliation

The controller uses precise requeuing based on window transition times rather than fixed-interval polling:

- When inside a window, the controller requeues when the window closes
- When outside all windows, the controller requeues when the next window opens
- When inside an exclusion, the controller requeues when the exclusion ends

This approach minimizes reconciliation overhead while ensuring timely phase transitions.

### Window Closure Precision

Window closure is **not** a precise operation. The controller detects that a window has closed on its next reconciliation loop, so the actual phase transition lags by the order of seconds under normal operation. If the controller is down or a reconciliation error occurs, a window may remain "stuck" in its current phase until the next successful reconcile.

This is by design — the controller is eventually consistent, not real-time. For most use cases (business hours, deployment freezes) this lag is negligible. If sub-second precision becomes necessary in the future, a `validUntil` field on the CommitStatus resource could allow the consumer to expire a gate independently of the controller.

### Status Fields

The ScheduledCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - branch: environment/development
      sha: abc123def456
      phase: success
      active:
        allow: "0 9 * * 1-5"
        transition: "2026-07-08T17:00:00Z"
      next:
        allow: "0 9 * * 1-5"
        transition: "2026-07-08T17:00:00Z"
    - branch: environment/staging
      sha: def789abc012
      phase: pending
      next:
        allow: "0 10 * * 2"
        transition: "2026-07-08T10:00:00Z"
```

Fields:

- `branch` -- The environment branch being gated
- `sha` -- The proposed commit SHA in this environment
- `phase` -- Current gate status (`pending` or `success`)
- `active` -- Details of the currently active window driving the phase (nil when no specific window is active, e.g. exclusion-only mode with no active exclusion):
    - `allow` -- Cron expression of the active allow window
    - `exclude` -- Cron expression of the active exclusion window
    - `transition` -- When the current window ends
- `next` -- Details of the next expected window transition (used by UIs for countdown timers and by the controller for precise requeuing):
    - `allow` -- Cron expression of the allow window involved in the next transition
    - `exclude` -- Cron expression of the exclusion window involved in the next transition
    - `transition` -- When the next phase change is expected

## Troubleshooting

### Gate Stuck in Pending

If a scheduled gate remains in pending status:

1. If the description is `Waiting for status to be reported`, the PromotionStrategy requires the key for that environment but no CommitStatus exists for the SHA. Either list the environment branch on the ScheduledCommitStatus, or remove the key from that environment's `proposedCommitStatuses` if it should be ungated
2. Check if the current time is outside all allow windows for the environment
3. Check if the current time is inside an exclusion window
4. Verify the timezone is correct -- cron expressions are evaluated in the resolved timezone (per-window override, then `spec.timezone`, then UTC)
5. Inspect the status for `active.exclude` and `next.transition` fields
6. Check the `Ready` condition -- cron parsing errors, invalid timezones, unknown branches, and PromotionStrategy key/environment mismatches surface in its message

### Gate Not Created

If no CommitStatus is created:

1. Verify the PromotionStrategy reference is correct (`spec.promotionStrategyRef.name`)
2. Ensure the environment branch names match exactly between the ScheduledCommitStatus and the PromotionStrategy -- the controller validates this and sets Ready=False with the mismatched branch names in the message
3. Check Ready=False for `requires key ... for environments not listed`: every PromotionStrategy environment that requires `spec.key` must be listed on the ScheduledCommitStatus
4. Check that the PromotionStrategy has been reconciled and has status populated (compare `metadata.generation` to `status.observedGeneration`)
5. Verify the environment has an active hydrated commit

### Checking Current Status

Use kubectl to inspect the ScheduledCommitStatus:

```bash
kubectl get scheduledcommitstatus webservice-tier-1 -o yaml
```

Example output (abbreviated):

```yaml
status:
  observedGeneration: 1
  conditions:
    - type: Ready
      status: "True"
      reason: ReconciliationSuccess
  environments:
    - branch: environment/development
      sha: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
      phase: success
      active:
        allow: "0 9 * * 1-5"
        transition: "2026-07-08T17:00:00Z"
      next:
        allow: "0 9 * * 1-5"
        transition: "2026-07-08T17:00:00Z"
    - branch: environment/staging
      sha: f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5
      phase: pending
      next:
        allow: "0 10 * * 2"
        transition: "2026-07-08T10:00:00Z"
```

Check the `status.environments` section for detailed information about each environment's window state, including which window is currently active and when the next transition will occur.
