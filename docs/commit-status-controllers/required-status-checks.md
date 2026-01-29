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
2. **Visibility**: For each required check, it creates a CommitStatus resource with the format `{provider}-{checkname}` (e.g., `github-e2e-test`)
3. **Continuous polling**: The controller polls your SCM provider's status check API to monitor the status of each required check
4. **State management**: By surfacing required checks as CommitStatus resources, the PromotionStrategy stays in "progressing" state while waiting, avoiding "degraded" from failed merge attempts

## Configuration

### Configure Polling Intervals

The controller is always enabled and automatically discovers required checks for all PromotionStrategies. You can tune the polling intervals and caching behavior:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: controller-configuration
  namespace: gitops-promoter
spec:
  requiredStatusCheckCommitStatus:
    # Caching configuration (reduces API calls significantly)
    requiredCheckCacheTTL: "24h"        # Cache required check discovery (default: 24h)
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
4. **Cached discovery**: Requeue scheduled at cache expiry time (default 24h)


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
{provider}-{normalized-name}-{hash}
```

The provider prefix identifies which SCM system the check comes from (e.g., `github`, `gitlab`, `bitbucket`).

For example:
- GitHub check: `ci-tests` → CommitStatus: `github-ci-tests-abc12345`
- GitHub check: `security/scan` → CommitStatus: `github-security-scan-def67890`

The CommitStatus label (used in ChangeTransferPolicy selectors) follows the format `{provider}-{checkname}` (or `{provider}-{checkname}-{appID}` when multiple checks have the same name):
- GitHub check: `ci-tests` → Label: `github-ci-tests`
- GitLab check: `build` → Label: `gitlab-build`
- GitHub check: `smoke` from GitHub App 15368 → Label: `github-smoke-15368` (when duplicates exist)

**Duplicate Detection:**

When multiple required checks have the same name but must come from different GitHub Apps, the controller automatically appends the GitHub App ID to make them unique:
- Check `smoke` from any app → `github-smoke` (if no duplicates)
- Check `smoke` from GitHub App 15368 → `github-smoke-15368` (if duplicates exist)
- Check `smoke` from GitHub App 98765 → `github-smoke-98765` (if duplicates exist)

This allows you to require the same check name from different sources (e.g., `lint` from both GitHub Actions and a custom GitHub App).

You can list all required check CommitStatus resources with:

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
          key: github-ci-tests
          phase: success
        - name: security-scan
          key: github-security-scan
          phase: pending
        - name: smoke
          key: github-smoke-15368
          phase: success
```

**Note**: The `key` field shows the computed label used in ChangeTransferPolicy selectors. When multiple checks have the same name, the GitHub App ID is appended (e.g., `github-smoke-15368`).

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
kubectl describe commitstatus github-ci-tests-abc12345
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
4. **Cached discovery**: Next requeue scheduled at cache expiry time (default 24 hours)

This provides fine-grained control over API usage while maintaining responsiveness.

## Performance and Caching

### Controller-Level Caching

The controller implements caching to reduce SCM API calls.

**What is cached:**

The cache stores the **list of required check names** discovered from your SCM provider's branch protection rules (e.g., GitHub Rulesets). This is the answer to the question: "What checks are required for this branch?"

- Cached data: Check names from branch protection API (e.g., `["ci-tests", "security-scan", "e2e-tests"]`)
- Cache key: Repository (domain/owner/name) + branch name
- Empty results are cached: If a branch has no protection rules, the empty result is cached

**What is NOT cached:**

The actual **status** of those checks (pending/success/failure) is NOT cached - the controller polls the SCM provider's commit status API for current status at the intervals defined by `pendingCheckInterval` and `terminalCheckInterval`.

**Cache behavior:**

- **Shared cache**: Cached at the controller level, shared across all PromotionStrategies
- **TTL-based expiration**: Cache entries expire after `requiredCheckCacheTTL` (default 24 hours)
- **Size-based eviction**: When cache exceeds `requiredCheckCacheMaxSize` (default 1000), oldest entries are evicted

**Cache Invalidation:**

The cache is stored in controller memory and expires automatically after the TTL. There are two ways to invalidate the cache:

1. **Wait for TTL expiration** (recommended):
   - Cache entries automatically expire after `requiredCheckCacheTTL`
   - This is the intended behavior and requires no manual intervention

2. **Restart the controller** (immediate, but disruptive):
   ```bash
   kubectl rollout restart deployment promoter-controller-manager -n promoter-system
   ```
   - This clears all in-memory cache and forces fresh discovery
   - Note: This will briefly interrupt all controller operations

### Recommended Settings

**For frequently changing rulesets** (development/testing):
```yaml
requiredCheckCacheTTL: "5m"   # Shorter TTL for faster discovery
pendingCheckInterval: "30s"   # More frequent polling
```

**For stable rulesets** (production):
```yaml
requiredCheckCacheTTL: "24h"      # Longer TTL for better API efficiency
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
- **Cached results**: Reused for `requiredCheckCacheTTL` duration (default 24 hours)
- **Watch-triggered updates**: Changes to PromotionStrategy, CTP, or CommitStatus trigger reconciliation
- **Periodic refresh**: Safety net ensures reconciliation every `safetyNetInterval` (default 1 hour)

Changes to branch protection rules are detected within the cache TTL or when triggered by watches.

## Troubleshooting

### Feature Not Working

**Problem**: No CommitStatus resources are created for required checks.

**Solutions**:
1. **Verify branch protection is configured** in your SCM provider:
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
