# Timed Commit Status Controller

The Timed Commit Status controller provides time-based gating for environment promotions. It ensures that changes "bake" in lower environments for a specified duration before being promoted to higher environments. This is useful for implementing safety practices like "soak time" or "bake time" requirements.

## Overview

The TimedCommitStatus controller monitors the active commits in specified environments and automatically creates CommitStatus resources that gate promotions to subsequent environments based on how long a commit has been running in the current environment.

### How It Works

For each environment configured in a TimedCommitStatus resource:

1. The controller checks how long the current active commit has been running in that environment
2. It compares the elapsed time against the configured required duration
3. It creates/updates a CommitStatus for the **next** environment's proposed SHA
4. The CommitStatus phase is set to:
   - `pending` - If the required duration has not been met, or if there's a pending promotion in the lower environment
   - `success` - If the required duration has been met and no pending promotion exists

This gating mechanism prevents changes from being promoted too quickly through your environment pipeline.

## Example Configurations

### Basic Time-Based Gating

In this example, we configure time-based gates for development and staging environments:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      duration: 1h
    - branch: environment/staging
      duration: 4h
```

This configuration:
- Requires changes to run in `development` for 1 hour before promoting to `staging`
- Requires changes to run in `staging` for 4 hours before promoting to `production`

### Integrating with PromotionStrategy

To use time-based gating, configure your PromotionStrategy to check for the `timed` commit status key:

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
    - branch: environment/staging
      proposedCommitStatuses:
        - key: timed
    - branch: environment/production
      proposedCommitStatuses:
        - key: timed
```

In this configuration:
- Changes can be promoted to `development` immediately (no gates)
- Changes must meet the 1-hour requirement in `development` before being promoted to `staging`
- Changes must meet the 4-hour requirement in `staging` before being promoted to `production`

### Complete Example with Multiple Gates

You can combine time-based gating with other commit status checks:

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
    - branch: environment/staging
      proposedCommitStatuses:
        - key: timed
        - key: manual-approval
    - branch: environment/production
      proposedCommitStatuses:
        - key: timed
        - key: manual-approval
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      duration: 2h
    - branch: environment/staging
      duration: 8h
```

This configuration requires:
- Argo CD health checks to pass in all environments (active commit status)
- 2-hour soak time in development before promoting to staging
- 8-hour soak time in staging before promoting to production
- Manual approval for both staging and production promotions

## Duration Formats

The `duration` field accepts standard Go duration strings:

- `30s` - 30 seconds
- `5m` - 5 minutes
- `1h` - 1 hour
- `2h30m` - 2 hours and 30 minutes
- `24h` - 24 hours

## Behavior Details

### Pending Promotions

If a lower environment has a pending promotion (i.e., proposed SHA != active SHA), the time-based gate will report `pending` for the next environment. This ensures that:

1. Pending changes in lower environments are merged before new changes can be promoted higher
2. The promotion pipeline remains orderly and predictable
3. You don't accidentally promote past an untested change

### Commit Time Tracking

The controller uses the `CommitTime` from the PromotionStrategy's environment status. This timestamp represents when the commit was deployed to the environment. The controller calculates elapsed time from this timestamp.

q### Intelligent Reconciliation

The TimedCommitStatus controller uses an intelligent requeue strategy to provide timely status updates and minimize promotion latency:

**Frequent Status Updates**: When there are pending time gates (i.e., commits waiting to meet their required duration), the controller reconciles every **1 minute** to provide regular status updates. This means:
- The `timeElapsed` field in the status updates every minute, giving you a live view of progress
- You can monitor how much time remains before a gate is satisfied
- When no time gates are pending, the controller uses the default reconciliation interval (typically 1 hour) to reduce overhead

**Automatic PromotionStrategy Triggering**: When a time gate transitions from `pending` to `success`, the controller automatically triggers a PromotionStrategy reconciliation by updating its `promoter.argoproj.io/reconcile-at` annotation. This ensures:
- The PromotionStrategy picks up newly satisfied time gates immediately
- Pull requests can be auto-merged without waiting for the PromotionStrategy's next scheduled reconciliation
- The overall promotion latency is minimized

**Example Timeline**: With a PromotionStrategy that reconciles every hour and a 15-minute time gate:
- **0 min**: Commit deployed to development, time gate starts as `pending`
- **1-14 min**: TimedCommitStatus reconciles every minute, updating `timeElapsed` in status
- **15 min**: Time gate transitions to `success`, PromotionStrategy is triggered immediately
- **15 min**: PromotionStrategy reconciles, sees satisfied gate, auto-merges PR (if configured)

Without this optimization, the PR would have to wait until the next scheduled PromotionStrategy reconciliation (potentially 45+ minutes later).

This behavior is automatic and requires no configuration.

### Status Fields

The TimedCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - branch: environment/development
      sha: abc123def456
      commitTime: "2024-01-15T10:00:00Z"
      requiredDuration: 1h
      timeElapsed: 45m30s
      phase: pending
```

Fields:
- `branch` - The environment branch being monitored
- `sha` - The active commit SHA in this environment
- `commitTime` - When the commit was deployed
- `requiredDuration` - The configured required duration
- `timeElapsed` - How long the commit has been running
- `phase` - Current gate status (`pending` or `success`)

## Use Cases

### Soak Time Requirements

Enforce organizational policies that require changes to run in pre-production environments for a minimum duration:

```yaml
environments:
  - branch: environment/preprod
    duration: 24h
```

### Progressive Rollout

Implement a progressive rollout strategy with increasing soak times:

```yaml
environments:
  - branch: environment/canary
    duration: 30m
  - branch: environment/staging
    duration: 4h
  - branch: environment/preprod
    duration: 24h
```

### Business Hours Only

While the controller doesn't directly support "business hours only" logic, you can set longer durations that span typical business hours to ensure changes are observed during working hours:

```yaml
environments:
  - branch: environment/development
    duration: 8h  # Ensures changes are seen during at least one business day
```

## Best Practices

1. **Start Conservative** - Begin with longer durations and reduce them as confidence grows
2. **Match Your Monitoring** - Set durations that give your monitoring systems time to detect issues
3. **Consider Your SLOs** - Align soak times with your error budget and incident detection times
4. **Document Rationale** - Add annotations explaining why specific durations were chosen
5. **Review Regularly** - Adjust durations based on incident history and confidence levels

## Troubleshooting

### Gate Stuck in Pending

If a time-based gate remains in pending status:

1. Check if there's a pending promotion in the lower environment
2. Verify the commit time is being tracked correctly in the PromotionStrategy status
3. Ensure the duration hasn't been recently increased
4. Check the TimedCommitStatus status for detailed timing information

### Gate Not Created

If no CommitStatus is created:

1. Verify the PromotionStrategy reference is correct
2. Ensure the environment branch names match exactly
3. Check that the PromotionStrategy has been reconciled and has status populated
4. Verify there's a next environment in the sequence (the last environment doesn't get a gate)

### Checking Current Status

Use kubectl to inspect the TimedCommitStatus:

```bash
kubectl get timedcommitstatus webservice-tier-1 -o yaml
```

Check the `status.environments` section for detailed information about each environment's timing.

## Related Resources

- [Gating Promotions](../gating-promotions.md) - Overview of how commit statuses gate promotions
- [Argo CD Commit Status Controller](./argocd.md) - Health-based gating using Argo CD
- [PromotionStrategy CRD](../crd-specs.md#promotionstrategy) - Full API specification
- [TimedCommitStatus CRD](../crd-specs.md#timedcommitstatus) - Full API specification

