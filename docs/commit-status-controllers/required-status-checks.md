# Required Status Check Visibility Controller

## Overview

The Required Status Check Visibility Controller automatically discovers required status checks from your SCM provider's branch protection rules and creates CommitStatus resources to provide visibility into what checks are blocking PR/MR merges. This prevents PromotionStrategy from showing "degraded" state (due to failed merge attempts) and instead shows "progressing" state while waiting for required checks to pass.

**Important:** Your SCM provider enforces the required checks via branch protection - this controller simply surfaces them as CommitStatus resources so users can see what GitOps Promoter is waiting on.

**SCM Support:**
- ✅ **GitHub** - Fully supported via Rulesets API
- 🚧 **GitLab** - Planned (protected branches)
- 🚧 **Bitbucket** - Planned (branch restrictions)
- 🚧 **Azure DevOps** - Planned (branch policies)
- 🚧 **Gitea/Forgejo** - Planned (branch protection)

## How It Works

1. **Auto-discovery**: The controller queries your SCM provider's branch protection API to discover which status checks are required for each target branch
2. **Visibility**: For each required check, it creates a CommitStatus resource with prefix `required-status-check-{checkname}`
3. **Continuous polling**: The controller polls your SCM provider's status check API to monitor the status of each required check
4. **State management**: By surfacing required checks as CommitStatus resources, the PromotionStrategy stays in "progressing" state while waiting, avoiding "degraded" from failed merge attempts

## Configuration

### Enable the Feature

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: controller-configuration
  namespace: gitops-promoter
spec:
  requiredStatusCheckCommitStatus:
    enabled: true  # Global toggle for all PromotionStrategies
```

Once enabled, the feature automatically applies to all PromotionStrategies in the cluster.

### Configure Polling Intervals

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: controller-configuration
  namespace: gitops-promoter
spec:
  requiredStatusCheckCommitStatus:
    enabled: true  # Enable the feature

    # Caching configuration (reduces API calls significantly)
    requiredCheckCacheTTL: "15m"        # Cache required check discovery (default: 15m)
    requiredCheckCacheMaxSize: 1000     # Max cache entries (default: 1000)

    # Dynamic polling intervals based on check state
    pendingCheckInterval: "1m"          # Poll when checks are pending (default: 1m)
    terminalCheckInterval: "10m"        # Poll when checks are terminal (default: 10m)
    safetyNetInterval: "1h"             # Safety net reconciliation (default: 1h)

    workQueue:
      requeueDuration: "5m"              # Base interval (overridden by above)
      maxConcurrentReconciles: 5
      rateLimiter:
        exponentialFailure:
          baseDelay: "1s"
          maxDelay: "1m"
```

**Polling Interval Behavior:**

The controller dynamically adjusts polling frequency based on check states:

1. **Pending checks exist**: Polls every `pendingCheckInterval` (default 1m) for timely updates
2. **All checks terminal** (success/failure): Polls every `terminalCheckInterval` (default 10m) to detect changes
3. **No checks to monitor**: Polls every `safetyNetInterval` (default 1h) as safety net for missed watch events
4. **Cached discovery**: Requeue scheduled at cache expiry time (default 15m)


## SCM Provider Setup

### Branch Protection Configuration

This feature requires configuring branch protection rules in your SCM provider to specify which status checks must pass before merging. The controller discovers these requirements automatically.

### GitHub Setup

For GitHub repositories, this feature uses GitHub Rulesets (not classic branch protection):

1. Go to your repository on GitHub
2. Navigate to **Settings → Rules → Rulesets**
3. Click **New ruleset** → **New branch ruleset**
4. Configure the ruleset:
   - **Name**: e.g., "Dev Environment Protection"
   - **Target branches**: e.g., `environment/dev`
   - **Branch protections**: Enable "Require status checks to pass"
   - **Required checks**: Add the check names (e.g., `ci-tests`, `security-scan`)
5. Save the ruleset

Repeat for each environment branch.

**Note:** Classic branch protection is not supported. You must use GitHub Rulesets for required checks to be discovered.

### Other SCM Providers (Planned)

Support for additional SCM providers (GitLab, Bitbucket, Azure DevOps, Gitea, Forgejo) is planned. Each will integrate with their respective branch protection mechanisms.

## How CommitStatus Resources are Named

Each required check gets its own CommitStatus resource with a predictable naming pattern:

```
required-status-check-{normalized-name}-{hash}
```

For example:
- Required check: `ci-tests` → CommitStatus: `required-status-check-ci-tests-abc12345`
- Required check: `security/scan` → CommitStatus: `required-status-check-security-scan-def67890`

You can list them with:

```bash
kubectl get commitstatus -l promoter.argoproj.io/required-status-check-commit-status
```

## Monitoring

### Check Status

View the RequiredStatusCheckCommitStatus resource to see discovered checks and their status:

```bash
kubectl get requiredstatuscheckcommitstatus my-app -o yaml
```

Example output:

```yaml
status:
  environments:
    - branch: environment/dev
      sha: abc123def456
      phase: pending
      requiredChecks:
        - name: ci-tests
          phase: success
        - name: security-scan
          phase: pending
```

### List All CommitStatus Resources for Required Checks

```bash
# All required check commit statuses
kubectl get commitstatus -l promoter.argoproj.io/required-status-check-commit-status

# For a specific environment
kubectl get commitstatus -l promoter.argoproj.io/environment=environment-dev \
  -l promoter.argoproj.io/required-status-check-commit-status
```

### View Check Details

```bash
kubectl describe commitstatus required-status-check-ci-tests-abc12345
```

## Behavior

### Phase Mapping

The controller maps SCM provider check statuses to CommitStatus phases. The exact mapping depends on the SCM provider.

#### GitHub Phase Mapping

| GitHub Status | GitHub Conclusion | CommitStatus Phase | Notes |
|--------------|-------------------|-------------------|-------|
| `completed` | `success` | `success` | Check passed |
| `completed` | `neutral` | `success` | Check passed (neutral) |
| `completed` | `skipped` | `success` | Check skipped |
| `completed` | `action_required` | `pending` | Waiting for manual action |
| `completed` | `failure` | `failure` | Check failed |
| `completed` | `cancelled` | `failure` | Check cancelled |
| `completed` | `timed_out` | `failure` | Check timed out |
| `queued` | - | `pending` | Check queued |
| `in_progress` | - | `pending` | Check running |

#### Other SCM Providers

Phase mappings for GitLab, Bitbucket, and other providers will be documented as support is added.

### Aggregated Phase

For each environment, the controller calculates an aggregated phase:

- **failure**: If any required check is failing
- **pending**: If any required check is pending (and none failing)
- **success**: If all required checks are successful

### Dynamic Requeue

The controller uses dynamic requeuing based on check states:

1. **Pending checks exist**: Requeue in `pendingCheckInterval` (default 1 minute) for active monitoring
2. **All checks terminal**: Requeue in `terminalCheckInterval` (default 10 minutes) to detect changes
3. **No checks to monitor**: Requeue in `safetyNetInterval` (default 1 hour) as safety net
4. **Cached discovery**: Next requeue scheduled at cache expiry time (default 15 minutes)

This provides fine-grained control over API usage while maintaining responsiveness.

## Performance and Caching

### Controller-Level Caching

The controller implements caching to dramatically reduce SCM API calls:

- **Shared cache**: Required check discovery results are cached at the controller level, shared across all PromotionStrategies
- **TTL-based expiration**: Cache entries expire after `requiredCheckCacheTTL` (default 15 minutes)
- **Size-based eviction**: When cache exceeds `requiredCheckCacheMaxSize` (default 1000), oldest entries are evicted

**Cache Invalidation:**

The cache automatically expires after the TTL. To force immediate cache refresh:
```bash
# Delete and recreate the RSCCS resource
kubectl delete requiredstatuscheckcommitstatus <name>
# The PromotionStrategy controller will recreate it automatically
```

### Recommended Settings

**For frequently changing rulesets** (development/testing):
```yaml
requiredCheckCacheTTL: "5m"   # Shorter TTL for faster discovery
pendingCheckInterval: "30s"   # More frequent polling
```

**For stable rulesets** (production):
```yaml
requiredCheckCacheTTL: "30m"      # Longer TTL for better API efficiency
terminalCheckInterval: "15m"      # Less frequent polling when stable
```

**For high-volume deployments** (100+ PromotionStrategies):
```yaml
requiredCheckCacheMaxSize: 5000   # Larger cache to accommodate all environments
maxConcurrentReconciles: 10       # More concurrent reconciliations
```

## Limitations

### GitHub-Specific Limitations

- **Rulesets Only**: GitHub classic branch protection is not supported. You must migrate to GitHub Rulesets.
- **Required Permissions**: The GitHub App needs "Metadata: Read" repository permission to query rulesets and "Checks: Read" to monitor check status.

### Check Discovery Timing

The controller uses a combination of watch-based reconciliation and cached discovery:

- **Initial discovery**: Happens immediately when controller starts or RSCCS resource is created
- **Cached results**: Reused for `requiredCheckCacheTTL` duration (default 15 minutes)
- **Watch-triggered updates**: Changes to PromotionStrategy, CTP, or CommitStatus trigger reconciliation
- **Periodic refresh**: Safety net ensures reconciliation every `safetyNetInterval` (default 1 hour)

Changes to branch protection rules are detected within the cache TTL or when triggered by watches.

## Troubleshooting

### Feature Not Working

**Problem**: Feature seems to be enabled but no CommitStatus resources are created.

**Solutions**:
1. **Verify feature is enabled globally**:
   ```bash
   kubectl get controllerconfiguration controller-configuration -o jsonpath='{.spec.requiredStatusCheckCommitStatus.enabled}'
   # Should output: true
   ```
   If not enabled, update ControllerConfiguration:
   ```yaml
   spec:
     requiredStatusCheckCommitStatus:
       enabled: true
   ```

2. **Verify branch protection is configured** in your SCM provider:
   - **GitHub**: Verify Rulesets are configured (not classic branch protection)
   - **Other SCMs**: Verify the SCM provider is supported (currently only GitHub)

3. **Check RequiredStatusCheckCommitStatus status** for errors:
   ```bash
   kubectl get requiredstatuscheckcommitstatus my-app -o yaml
   ```

4. **Verify the PromotionStrategy references a valid GitRepository** with a supported SCM provider

5. **Check controller logs**:
   ```bash
   kubectl logs -n gitops-promoter -l app.kubernetes.io/name=gitops-promoter | grep RequiredStatusCheckCommitStatus
   ```

### CommitStatus Stuck in Pending

**Problem**: A CommitStatus is stuck in pending state.

**Solutions**:
1. Check if the status check actually exists and is running in your SCM provider:
   - **GitHub**: Go to the PR → "Checks" tab
   - Verify the check/pipeline is actually running
2. Verify the check name matches exactly (case-sensitive)
3. Check if the check is excluded in environment configuration
4. Review controller logs for polling errors

### Wrong Checks Discovered

**Problem**: The controller discovers checks that shouldn't be there, or misses checks that should be there.

**Solutions**:
1. Verify branch protection configuration in your SCM provider:
   - **GitHub**: Settings → Rules → Rulesets (check which rulesets apply to the branch)
   - Ensure protection rules target the correct branches
2. Verify the branch name matches exactly (case-sensitive)
3. Check for inherited organization/group-level protection rules

### Rate Limiting

**Problem**: SCM provider API rate limits are being hit.

**Solutions**:
1. Increase the `requeueDuration` in ControllerConfiguration to reduce polling frequency
2. Verify your SCM provider authentication has sufficient rate limits:
   - **GitHub**: Use a GitHub App (not a personal access token) for higher rate limits
3. Monitor API usage in controller logs
4. Check metrics for rate limit information (if available):
   ```bash
   kubectl get --raw /metrics | grep rate_limit
   ```
