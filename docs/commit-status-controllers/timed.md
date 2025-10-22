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

### Complete Example with Schedule-Based Gates

Here's a realistic production configuration using schedule-based gating:

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
    - key: ci-tests
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
    # Development: Deploy anytime after 30 minutes
    - branch: environment/development
      duration: 30m
    
    # Staging: Deploy during business hours after 4-hour soak
    - branch: environment/staging
      duration: 4h
      schedule:
        cron: "0 9-17 * * 1-5"    # Hourly from 9 AM-5 PM weekdays
        window: "30m"              # 30-minute window each hour
    
    # Production: Deploy only during afternoon window after 24-hour soak
    - branch: environment/production
      duration: 24h
      schedule:
        cron: "0 14 * * 1-5"      # 2 PM Monday-Friday
        window: "2h"               # 2 PM - 4 PM deployment window
```

This configuration ensures:
- Development can deploy anytime after a 30-minute soak
- Staging deploys only during business hours (every hour from 9 AM-5 PM weekdays) after a 4-hour soak
- Production deploys only in the afternoon window (2 PM-4 PM weekdays) after a full 24-hour soak
- All environments require passing Argo CD health checks
- All promotions require passing CI tests

## Duration Formats

The `duration` field accepts standard Go duration strings:

- `30s` - 30 seconds
- `5m` - 5 minutes
- `1h` - 1 hour
- `2h30m` - 2 hours and 30 minutes
- `24h` - 24 hours

## Schedule-Based Gating

In addition to duration-based gating, you can configure schedule-based gating using cron expressions. This allows you to restrict promotions to specific time windows, such as business hours or maintenance windows.

### Basic Schedule Configuration

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/production
      schedule:
        cron: "0 9 * * 1-5"       # 9 AM Monday-Friday
        window: "4h"               # 4-hour deployment window
```

This configuration allows promotions to production only during business hours (9 AM - 1 PM on weekdays).

### Schedule Fields

- **`cron`** (required): A cron expression defining when deployment windows begin. Uses standard 5-field cron format:
  - Minute (0-59)
  - Hour (0-23)
  - Day of month (1-31)
  - Month (1-12)
  - Day of week (0-6, Sunday = 0)

- **`window`** (required): How long after the cron trigger the deployment window remains open. Accepts Go duration format (e.g., `30m`, `2h`, `4h`).

### Cron Expression Examples

```yaml
# Every day at 2 AM (maintenance window)
cron: "0 2 * * *"
window: "2h"

# Weekdays at 9 AM (business hours start)
cron: "0 9 * * 1-5"
window: "8h"

# Every 4 hours
cron: "0 */4 * * *"
window: "1h"

# First day of every month at midnight
cron: "0 0 1 * *"
window: "6h"
```

### Combining Duration and Schedule

You can combine both duration and schedule requirements. When both are specified, **BOTH** conditions must be satisfied for the promotion to proceed:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: TimedCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/production
      duration: 24h                # Must soak for 24 hours
      schedule:
        cron: "0 14 * * *"         # 2 PM daily
        window: "2h"                # 2-hour window (2 PM - 4 PM)
```

This ensures:
1. Changes have been running in the previous environment for at least 24 hours
2. The promotion happens during the approved time window (2 PM - 4 PM daily)

If the duration is met but it's outside the schedule window, the gate remains pending. If it's within the schedule window but the duration hasn't been met, the gate also remains pending.

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

### Business Hours Only Deployments

Restrict production deployments to business hours to ensure teams are available to respond to issues:

```yaml
environments:
  - branch: environment/production
    schedule:
      cron: "0 9 * * 1-5"      # 9 AM Monday-Friday
      window: "8h"              # 9 AM - 5 PM window
```

### Maintenance Window Deployments

Schedule deployments during off-peak hours or maintenance windows:

```yaml
environments:
  - branch: environment/production
    schedule:
      cron: "0 2 * * 0"        # 2 AM Sunday
      window: "4h"              # 2 AM - 6 AM window
```

### Combined Soak Time and Schedule

Require both adequate soak time and deployment during approved windows:

```yaml
environments:
  - branch: environment/staging
    duration: 4h                # 4 hour soak in previous env
    schedule:
      cron: "0 9-17 * * 1-5"   # Every hour from 9 AM-5 PM weekdays
      window: "30m"             # 30 minute window after each hour
  - branch: environment/production
    duration: 24h               # 24 hour soak in staging
    schedule:
      cron: "0 14 * * 1-5"     # 2 PM Monday-Friday
      window: "2h"              # 2 PM - 4 PM window
```

This ensures staging deploys only during business hours after a 4-hour soak, and production deploys only in the afternoon after a full 24-hour soak in staging.

## Troubleshooting

### Gate Stuck in Pending

If a time-based gate remains in pending status:

1. **Duration-based gates:**
   - Check if there's a pending promotion in the lower environment
   - Verify the commit time is being tracked correctly in the PromotionStrategy status
   - Ensure the duration hasn't been recently increased
   - Check the TimedCommitStatus status for detailed timing information

2. **Schedule-based gates:**
   - Verify you're within the configured schedule window
   - Check if the cron expression is correct using a cron validator
   - Ensure the `window` is long enough for your deployment process
   - Review the CommitStatus description field for schedule-specific error messages

3. **Combined duration and schedule:**
   - Both conditions must be satisfied - check each independently
   - Verify the schedule window aligns with when your duration requirement is typically met

### Invalid Cron Expression

Invalid cron expressions are rejected at the API level with immediate validation errors. If you receive a validation error when creating or updating a TimedCommitStatus:

1. Validate your cron expression using a cron validator tool
2. Ensure you're using the 5-field format (minute, hour, day-of-month, month, day-of-week)
3. Common mistakes:
   - Using 6 fields (seconds are not supported)
   - Invalid ranges (e.g., hour 24, minute 60)
   - Syntax errors in the expression
   - Cron expression too short (minimum 9 characters)

Valid cron examples:
```yaml
# Good
cron: "0 9 * * 1-5"      # 9 AM weekdays
cron: "*/30 * * * *"     # Every 30 minutes
cron: "0 0,12 * * *"     # Midnight and noon

# Bad - These will be rejected by the API
cron: "0 0 9 * * 1-5"    # 6 fields (has seconds)
cron: "60 9 * * *"       # Invalid minute (60)
cron: "9am * * *"        # Text instead of numbers
cron: "* * *"            # Too few fields
```

### Deployment Window Missed

If promotions are blocked because the schedule window was missed:

1. Check when the next window opens using the cron expression
2. Consider widening the `window` if windows are too narrow
3. Add more frequent cron triggers if needed (e.g., hourly instead of daily)
4. Review if the schedule aligns with your team's workflow

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

To check the CommitStatus for schedule-specific information:

```bash
kubectl get commitstatus -l promoter.argoproj.io/commit-status=timer -o yaml
```

Look at the `spec.description` field for details about why a schedule-based gate is pending.

