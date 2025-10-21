# Timed Commit Status Controller

The Timed Commit Status controller provides time-based gating for environment promotions. It ensures that changes "bake" in environments for a specified duration. This is useful for implementing safety practices like "soak time" or "bake time" requirements.

## Overview

The TimedCommitStatus controller monitors the active commits in specified environments and automatically creates CommitStatus resources that act as active commit status gates based on how long a commit has been running in each environment.

### How It Works

For each environment configured in a TimedCommitStatus resource:

1. The controller checks how long the current active commit has been running in that environment
2. It compares the elapsed time against the configured required duration
3. It creates/updates a CommitStatus for the **current** environment's active SHA
4. The CommitStatus phase is set to:
   - `pending` - If the required duration has not been met, or if there's a pending promotion in the environment
   - `success` - If the required duration has been met and no pending promotion exists

This gating mechanism prevents changes from being promoted from environments too quickly.

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
- Requires changes to run in `development` for 1 hour before they can be promoted out
- Requires changes to run in `staging` for 4 hours before they can be promoted out

### Integrating with PromotionStrategy

To use time-based gating, configure your PromotionStrategy to check for the `timer` commit status key as an active commit status:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  activeCommitStatuses:
    - key: timer
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

In this configuration:
- Changes must meet the 1-hour requirement in `development` before being promoted to `staging`
- Changes must meet the 4-hour requirement in `staging` before being promoted to `production`
- The timed gate applies globally as an active commit status, preventing promotions when time requirements are not met

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
    - key: timer
  proposedCommitStatuses:
    - key: manual-approval
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
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
    - branch: environment/production
      duration: 24h
```

This configuration requires:
- Argo CD health checks to pass in all environments (active commit status)
- 2-hour soak time in development before promoting to staging
- 8-hour soak time in staging before promoting to production
- 24-hour soak time in production (just informational does not block any promotion, because no next environment)
- Manual approval before any promotion (proposed commit status)

## Duration Formats

The `duration` field accepts standard Go duration strings:

- `30s` - 30 seconds
- `5m` - 5 minutes
- `1h` - 1 hour
- `2h30m` - 2 hours and 30 minutes
- `24h` - 24 hours

## Behavior Details

### Timer Reset on New Commits

When a new commit is merged to an environment, the timer automatically resets. The controller tracks the active commit's SHA and commit time, so:

1. A new commit becomes active with a fresh `CommitTime`
2. The controller calculates elapsed time from the new commit time (effectively resetting to 0)
3. The time gate shows `pending` until the new commit meets the duration requirement
4. This ensures every commit must satisfy the soak time requirement before being promoted

This automatic reset behavior means you don't need any special configuration - the controller naturally enforces that each commit runs for the required duration.

### Commit Time Tracking

The controller uses the `CommitTime` from the PromotionStrategy's environment status. This timestamp represents when the commit was deployed to the environment. The controller calculates elapsed time from this timestamp.

### Intelligent Reconciliation

The TimedCommitStatus controller uses an intelligent requeue strategy to provide timely status updates and minimize promotion latency:

**Frequent Status Updates**: When there are pending time gates where the required duration has **not yet been met**, the controller reconciles every **1 minute** to provide regular status updates. This means:
- The `atMostDurationRemaining` field in the status updates every minute, giving you a live view of progress toward meeting the required duration
- You can monitor how much time remains before a gate is satisfied
- Once all time gates have either met their required duration, the controller uses the configured reconciliation interval (default is 1 hour) to reduce overhead


### Status Fields

The TimedCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - branch: environment/development
      sha: abc123def456
      commitTime: "2024-01-15T10:00:00Z"
      requiredDuration: 1h
      atMostDurationRemaining: 14m30s
      phase: pending
```

Fields:
- `branch` - The environment branch being monitored
- `sha` - The active commit SHA in this environment
- `commitTime` - When the commit was deployed
- `requiredDuration` - The configured required duration
- `atMostDurationRemaining` - Maximum time remaining until the gate is satisfied (0 when phase is success)
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
4. Verify the environment has an active commit

### Checking Current Status

Use kubectl to inspect the TimedCommitStatus:

```bash
kubectl get timedcommitstatus webservice-tier-1 -o yaml
```

Check the `status.environments` section for detailed information about each environment's timing.

