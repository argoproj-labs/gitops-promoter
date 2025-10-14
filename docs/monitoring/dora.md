# DORA Metrics Guide

[DORA (DevOps Research and Assessment)](https://dora.dev/) metrics are key performance indicators for software delivery. GitOps Promoter provides Prometheus metrics that enable you to calculate and track these metrics for your deployment pipeline.

## Overview

GitOps Promoter tracks four key metrics that map to DORA's core measurements:

1. **Deployment Frequency**: How often deployments occur
2. **Lead Time for Changes**: Time from commit to production
3. **Change Failure Rate**: Percentage of deployments causing failures
4. **Mean Time to Restore (MTTR)**: Time to recover from failures

These metrics are available in two forms:
- **Prometheus metrics**: For monitoring dashboards and alerting
- **Status fields**: Directly visible in the PromotionStrategy resource status for easy access via kubectl or UIs

## Status Fields

DORA metrics are stored in two places:

1. **ChangeTransferPolicy Status**: Each CTP tracks DORA metrics for its specific environment in `status.doraMetrics`
2. **PromotionStrategy Status**: Aggregates terminal environment metrics in `status.terminalEnvironmentDoraMetrics`

### Accessing CTP Metrics

Each ChangeTransferPolicy tracks metrics for its environment:

```bash
# Get metrics for a specific environment's CTP
kubectl get changetransferpolicy my-app-dev -o jsonpath='{.status.doraMetrics}'

# Get metrics for production environment  
kubectl get changetransferpolicy my-app-production -o jsonpath='{.status.doraMetrics}'
```

### Accessing Terminal Environment Metrics via PromotionStrategy

For convenience, the PromotionStrategy aggregates metrics from the terminal (production) environment:

```bash
# Get terminal environment DORA metrics
kubectl get promotionstrategy my-app -o jsonpath='{.status.terminalEnvironmentDoraMetrics}'
```

Each DoraMetrics object tracks:
- `deploymentCount`: Total number of deployments
- `lastLeadTimeSeconds`: Most recent lead time measurement (Duration type)
- `failureCount`: Total number of failures
- `lastMTTRSeconds`: Most recent mean time to restore measurement (Duration type)
- Internal state fields for tracking lead time start, failure times, etc.

These fields complement the Prometheus metrics and simplify testing and debugging.

## Metrics Produced

### deployments_total

**Type**: Counter  
**DORA Metric**: Deployment Frequency

This counter increments every time a change is merged to the active branch of an environment. Each deployment represents a successful promotion through your GitOps workflow.

**Example PromQL queries**:

```promql
# Deployments per hour to production (terminal environment)
rate(deployments_total{is_terminal="true"}[1h])

# Total deployments to a specific environment
sum(deployments_total{environment="production"})

# Deployment frequency by promotion strategy
sum by (promotion_strategy) (rate(deployments_total[24h]))
```

### dora_lead_time_seconds

**Type**: Gauge  
**DORA Metric**: Lead Time for Changes

This gauge tracks the time (in seconds) from when a DRY commit is created to when it successfully deploys in an environment. The lead time calculation:

- **Starts**: When a new DRY commit enters the promotion pipeline
- **Ends**: When the commit is merged to the active branch AND the active commit status becomes successful
- **Special handling**: If a new commit arrives before the current one completes, the lead time continues tracking from the original commit time (not the new commit time)

**Example PromQL queries**:

```promql
# Average lead time to production in hours
dora_lead_time_seconds{is_terminal="true"} / 3600

# Lead time trend over time
dora_lead_time_seconds{environment="production"}

# Compare lead times across environments
dora_lead_time_seconds
```

**Note**: Since this is a gauge, it represents the lead time of the most recently deployed commit. To track trends over time, scrape and store these values at regular intervals.

### dora_change_failure_rate_total

**Type**: Counter  
**DORA Metric**: Change Failure Rate

This counter increments once per commit SHA when the active commit status enters a failed state. The counter only increments once per commit, ensuring accurate failure tracking even if the status fluctuates.

**Example PromQL queries**:

```promql
# Change failure rate percentage for production
(
  rate(dora_change_failure_rate_total{is_terminal="true"}[24h]) 
  / 
  rate(deployments_total{is_terminal="true"}[24h])
) * 100

# Total failures by environment
sum by (environment) (dora_change_failure_rate_total)

# Failure rate trend over the last 7 days
rate(dora_change_failure_rate_total{environment="production"}[7d])
```

### mean_time_to_restore_seconds

**Type**: Gauge  
**DORA Metric**: Mean Time to Restore

This gauge tracks the time (in seconds) from when an environment enters a failed state to when it returns to a healthy state. The MTTR calculation:

- **Starts**: When the active commit status enters a failed state
- **Ends**: When the active commit status returns to a successful state
- **Tracking**: Only tracks one failure at a time per environment

**Example PromQL queries**:

```promql
# MTTR in hours for production
mean_time_to_restore_seconds{is_terminal="true"} / 3600

# Average MTTR across all environments
avg(mean_time_to_restore_seconds)

# MTTR by promotion strategy
mean_time_to_restore_seconds
```

**Note**: Like lead time, this is a gauge representing the most recent recovery time. Store historical values to track trends.

## Using the `is_terminal` Label

The `is_terminal` label identifies the last environment in your promotion sequence (typically production). This label helps you focus DORA metrics on your production environment without needing to hard-code environment names.

```promql
# All metrics for terminal (production) environment
deployments_total{is_terminal="true"}
dora_lead_time_seconds{is_terminal="true"}
dora_change_failure_rate_total{is_terminal="true"}
mean_time_to_restore_seconds{is_terminal="true"}
```

## Events and Logs

GitOps Promoter produces Kubernetes events and structured log entries for all DORA metric updates:

### Events

- `CommitPromoted`: A commit was promoted (deployed) to an environment
- `LeadTimeRecorded`: Lead time was calculated and recorded
- `ChangeFailureRecorded`: A change failure was detected
- `MTTRRecorded`: Mean time to restore was calculated
- `PromotionInterrupted`: A new commit interrupted an incomplete release

You can view these events using:

```bash
kubectl get events --field-selector involvedObject.kind=PromotionStrategy
```

### Logs

All metric updates are logged at the Info level with structured fields. Look for log entries like:

- `Recorded deployment`
- `Recorded lead time`
- `Recorded change failure`
- `Recorded MTTR`
- `Incomplete release interrupted by new commit`

## DORA Performance Levels

Use these metrics to measure your team's performance against DORA benchmarks:

| Metric | Elite | High | Medium | Low |
|--------|-------|------|--------|-----|
| Deployment Frequency | On-demand (multiple per day) | Between once per week and once per month | Between once per month and once every 6 months | Fewer than once per six months |
| Lead Time for Changes | Less than one hour | Between one day and one week | Between one month and six months | More than six months |
| Change Failure Rate | 0-15% | 16-30% | 31-45% | 46-60% |
| Mean Time to Restore | Less than one hour | Less than one day | Between one day and one week | More than six months |

## Example Dashboard Queries

Here's a complete set of PromQL queries for a DORA metrics dashboard:

```promql
# Deployment Frequency (deployments per day, last 7 days)
sum(increase(deployments_total{is_terminal="true"}[1d])) / 7

# Lead Time for Changes (in hours)
dora_lead_time_seconds{is_terminal="true"} / 3600

# Change Failure Rate (percentage, last 7 days)
(
  sum(increase(dora_change_failure_rate_total{is_terminal="true"}[7d]))
  /
  sum(increase(deployments_total{is_terminal="true"}[7d]))
) * 100

# Mean Time to Restore (in hours)
mean_time_to_restore_seconds{is_terminal="true"} / 3600
```

## Best Practices

1. **Configure Active Commit Statuses**: Ensure your PromotionStrategy has appropriate `activeCommitStatuses` configured to accurately detect successful and failed deployments.

2. **Monitor All Environments**: While the terminal environment is often most important, tracking DORA metrics across all environments can provide valuable insights into your pipeline.

3. **Set Up Alerting**: Create alerts for:
   - High change failure rates
   - Increasing lead times
   - Long MTTR periods

4. **Regular Review**: Review DORA metrics regularly with your team to identify improvement opportunities.

5. **Historical Analysis**: Store metric values in a time-series database (like Prometheus) to track trends over time and measure improvement.

## Troubleshooting

### Metrics Not Updating

- Verify that the PromotionStrategy is reconciling successfully
- Check that commit statuses are configured and being updated
- Review controller logs for any errors
- Ensure Prometheus is scraping the metrics endpoint

### Incomplete Lead Time Tracking

If lead time metrics seem incorrect:
- Check that commit statuses eventually reach a success state
- Verify that the `commitTime` field is populated on commits
- Review events for "PromotionInterrupted" to understand if releases are being interrupted

### Missing Failure Metrics

If change failures aren't being tracked:
- Ensure active commit statuses are configured
- Verify that commit statuses actually enter a "failure" state
- Check the `lastFailedCommitSha` in the PromotionStrategy status to see which commits have been tracked
