# Scheduled Commit Status Controller

The Scheduled Commit Status controller provides time window based gating for environment promotions. It ensures that changes can only be promoted during specific scheduled deployment windows. This is useful for implementing deployment policies like "business hours only" or "maintenance window" requirements.

## Overview

The ScheduledCommitStatus controller monitors proposed commits for specified environments and automatically creates CommitStatus resources that act as proposed commit status gates based on whether the current time falls within configured deployment windows.

### How It Works

For each environment configured in a ScheduledCommitStatus resource:

1. The controller checks if the current time is within a configured deployment window
2. A deployment window is defined by a cron schedule (when it starts) and a duration (how long it lasts)
3. It creates/updates a CommitStatus for the **proposed** environment's hydrated SHA
4. The CommitStatus phase is set to:
   - `success` - If the current time is within the deployment window (allows promotions)
   - `pending` - If the current time is outside the deployment window (blocks promotions)

## Example Configurations

### Basic Business Hours Deployment

In this example, we configure deployment windows for business hours only:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      schedule:
        cron: "0 9 * * 1-5"       # 9 AM Monday-Friday
        window: "8h"               # 9 AM - 5 PM deployment window
        timezone: "America/New_York"
    
    - branch: environment/production
      schedule:
        cron: "0 14 * * 1-5"      # 2 PM Monday-Friday
        window: "2h"               # 2 PM - 4 PM deployment window
        timezone: "America/New_York"
```

This configuration:
- Allows deployments to `development` only between 9 AM - 5 PM EST on weekdays
- Allows deployments to `production` only between 2 PM - 4 PM EST on weekdays
- Blocks all deployments outside these windows

### Integrating with PromotionStrategy

To use scheduled gating, configure your PromotionStrategy to check for the `schedule` commit status key as a proposed commit status:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  proposedCommitStatuses:
    - key: schedule
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

In this configuration:
- Changes can only be promoted when the current time is within the configured deployment window
- The scheduled gate applies globally as a proposed commit status, preventing promotions when outside deployment windows
- PRs will remain open but unmerged until the deployment window opens

### Maintenance Window Deployments

Configure deployments to only occur during maintenance windows:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: database-service
spec:
  promotionStrategyRef:
    name: database-service
  environments:
    - branch: environment/production
      schedule:
        cron: "0 2 * * 0"         # 2 AM Sunday
        window: "4h"               # 2 AM - 6 AM maintenance window
        timezone: "UTC"
```

This allows production deployments only during the Sunday morning maintenance window.

### Multi-Region Deployment Windows

Configure different deployment windows for different regions:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: global-service
spec:
  promotionStrategyRef:
    name: global-service
  environments:
    - branch: environment/us-production
      schedule:
        cron: "0 14 * * 1-5"      # 2 PM EST weekdays
        window: "2h"
        timezone: "America/New_York"
    
    - branch: environment/eu-production
      schedule:
        cron: "0 14 * * 1-5"      # 2 PM CET weekdays
        window: "2h"
        timezone: "Europe/Paris"
    
    - branch: environment/apac-production
      schedule:
        cron: "0 14 * * 1-5"      # 2 PM JST weekdays
        window: "2h"
        timezone: "Asia/Tokyo"
```

This ensures each region has deployments during their local business hours.

### Complete Example with Multiple Gates

You can combine scheduled gating with other commit status checks:

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
    - key: schedule
    - key: manual-approval
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScheduledCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
      schedule:
        cron: "0 6 * * 1-5"       # 6 AM - 10 PM weekdays
        window: "16h"
        timezone: "America/New_York"
    
    - branch: environment/production
      schedule:
        cron: "0 10 * * 1-5"      # 10 AM - 2 PM weekdays
        window: "4h"
        timezone: "America/New_York"
```

This configuration requires:
- Argo CD health checks to pass in all environments (active commit status)
- Minimum soak time requirements (timer active commit status)
- Deployments only during scheduled windows (schedule proposed commit status)
- Manual approval before any promotion (manual-approval proposed commit status)

## Schedule Configuration

### Cron Syntax

The `cron` field uses standard cron syntax with 5 fields:

```
┌───────────── minute (0 - 59)
│ ┌───────────── hour (0 - 23)
│ │ ┌───────────── day of month (1 - 31)
│ │ │ ┌───────────── month (1 - 12)
│ │ │ │ ┌───────────── day of week (0 - 6) (Sunday to Saturday)
│ │ │ │ │
│ │ │ │ │
* * * * *
```

**Common Examples:**

- `0 9 * * 1-5` - 9 AM Monday through Friday
- `0 14 * * *` - 2 PM every day
- `0 2 * * 0` - 2 AM every Sunday
- `30 8 * * 1,3,5` - 8:30 AM Monday, Wednesday, Friday
- `0 */4 * * *` - Every 4 hours
- `0 0 1 * *` - Midnight on the first day of each month

### Window Duration

The `window` field accepts standard Go duration strings:

- `30m` - 30 minutes
- `1h` - 1 hour
- `2h30m` - 2 hours and 30 minutes
- `8h` - 8 hours
- `24h` - 24 hours

### Timezone Configuration

The `timezone` field accepts IANA timezone names:

**Americas:**
- `America/New_York` - Eastern Time
- `America/Chicago` - Central Time
- `America/Denver` - Mountain Time
- `America/Los_Angeles` - Pacific Time
- `America/Sao_Paulo` - Brazil Time

**Europe:**
- `Europe/London` - UK Time
- `Europe/Paris` - Central European Time
- `Europe/Berlin` - Central European Time

**Asia/Pacific:**
- `Asia/Tokyo` - Japan Time
- `Asia/Shanghai` - China Time
- `Asia/Singapore` - Singapore Time
- `Australia/Sydney` - Australian Eastern Time

**Default:** If not specified, defaults to `UTC`.

### Status Fields

The ScheduledCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - branch: environment/production
      sha: abc123def456
      phase: pending
      currentlyInWindow: false
      nextWindowStart: "2024-01-15T14:00:00Z"
      nextWindowEnd: "2024-01-15T16:00:00Z"
```

Fields:
- `branch` - The environment branch being monitored
- `sha` - The proposed hydrated commit SHA for this environment
- `phase` - Current gate status (`pending` or `success`)
- `currentlyInWindow` - Whether the current time is within a deployment window
- `nextWindowStart` - When the next deployment window starts
- `nextWindowEnd` - When the next deployment window ends