# Required Check Visibility Controller

## Overview

The Required Check Visibility Controller automatically discovers required checks from your SCM provider's branch protection rules and creates CommitStatus resources to provide visibility into what checks are blocking PR/MR merges. This prevents PromotionStrategy from showing "degraded" state (due to failed merge attempts) and instead shows "progressing" state while waiting for required checks to pass.

**Important:** Your SCM provider enforces the required checks via branch protection - this controller simply surfaces them as CommitStatus resources so users can see what GitOps Promoter is waiting on.

**SCM Support:**
- ✅ **GitHub** - Fully supported via Rulesets API
- 🚧 **GitLab** - Planned (protected branches)
- 🚧 **Bitbucket** - Planned (branch restrictions)
- 🚧 **Azure DevOps** - Planned (branch policies)
- 🚧 **Gitea/Forgejo** - Planned (branch protection)

## How It Works

1. **Auto-discovery**: The controller queries your SCM provider's branch protection API to discover which checks are required for each target branch
2. **Visibility**: For each required check, it creates a CommitStatus resource with the format `{provider}-{checkname}` (e.g., `github-e2e-test`)
3. **Continuous polling**: The controller polls your SCM provider's check status API to monitor the status of each required check
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
  requiredCheckCommitStatus:
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

**Per-Check Polling Optimization:**

The controller uses intelligent per-check polling to minimize API calls while maintaining responsiveness:

- **Pending checks**: Polled every `pendingCheckInterval` (default 1m) for timely updates
- **Terminal checks** (success/failure): Polled every `terminalCheckInterval` (default 10m) only if not recently checked
- **Independent polling**: Each check is polled based on its own state, not the aggregate state of all checks

This means if you have 5 terminal checks (success/failure) and 1 pending check, only the pending check is polled frequently. The terminal checks are polled every 10 minutes, **reducing API calls by ~83%** in typical scenarios.

**Reconciliation Behavior:**

The controller reconciliation loop runs based on:

1. **Pending checks exist**: Reconciles every `pendingCheckInterval` (default 1m) to poll active checks
2. **All checks terminal**: Reconciles every `terminalCheckInterval` (default 10m) to detect changes
3. **No checks to monitor**: Reconciles every `safetyNetInterval` (default 1h) as safety net for missed watch events
4. **Cached discovery**: Next reconciliation scheduled at cache expiry time (default 24h)


## SCM Provider Setup

### Branch Protection Configuration

This feature requires configuring branch protection rules in your SCM provider to specify which checks must pass before merging. The controller discovers these requirements automatically.

### GitHub Setup

For GitHub repositories, this feature uses GitHub Rulesets (not classic branch protection):

1. **Configure GitHub App Permissions** (required first):
   - Go to your GitHub App settings
   - Under "Repository permissions", ensure these are set to "Read-only":
     - **Metadata** - To query rulesets
     - **Checks** - To monitor modern checks (Check Runs API)
     - **Commit statuses** - To monitor legacy checks (Commit Status API)
   - Save changes and accept the permission update
   - **Important**: Without "Commit statuses: Read", you'll get 403 errors when the controller tries to fallback to the legacy API

2. **Create Branch Protection Ruleset**:
   - Go to your repository on GitHub
   - Navigate to **Settings → Rules → Rulesets**
   - Click **New ruleset** → **New branch ruleset**
   - Configure the ruleset:
     - **Name**: e.g., "Dev Environment Protection"
     - **Target branches**: e.g., `environment/dev`
     - **Branch protections**: Enable "Require checks to pass"
     - **Required checks**: Add the check names (e.g., `ci-tests`, `security-scan`)
   - Save the ruleset

3. **Repeat for each environment branch**

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
kubectl get commitstatus -l promoter.argoproj.io/required-check-commit-status
```

## Monitoring

### Check Status

View the RequiredCheckCommitStatus resource to see discovered checks and their status:

```bash
kubectl get requiredcheckcommitstatus my-app -o yaml
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
          lastPolledAt: "2026-01-29T22:48:49Z"
        - name: security-scan
          key: github-security-scan
          phase: pending
          lastPolledAt: "2026-01-29T22:54:02Z"
        - name: smoke
          key: github-smoke-15368
          phase: success
          lastPolledAt: "2026-01-29T22:48:49Z"
```

**Note**: 
- The `key` field shows the computed label used in ChangeTransferPolicy selectors. When multiple checks have the same name, the GitHub App ID is appended (e.g., `github-smoke-15368`).
- The `lastPolledAt` field shows when each check was last queried from the SCM provider. Terminal checks (success/failure) are polled less frequently than pending checks, reducing API usage.

### List All CommitStatus Resources for Required Checks

```bash
# All required check commit statuses
kubectl get commitstatus -l promoter.argoproj.io/required-check-commit-status

# For a specific environment
kubectl get commitstatus -l promoter.argoproj.io/environment=environment-dev \
  -l promoter.argoproj.io/required-check-commit-status
```

### View Check Details

```bash
kubectl describe commitstatus github-ci-tests-abc12345
```

## Behavior

### Phase Mapping

The controller maps SCM provider check status to CommitStatus phases. The exact mapping depends on the SCM provider.

#### GitHub Phase Mapping

GitHub uses two APIs for check status: the modern **Check Runs API** and the legacy **Commit Status API**. The controller queries both APIs with intelligent fallback.

**Check Runs API (Modern):**

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

**Commit Status API (Legacy):**

| Commit Status State | CommitStatus Phase | Notes |
|--------------------|-------------------|-------|
| `success` | `success` | Check passed |
| `failure` | `failure` | Check failed |
| `error` | `failure` | Check errored |
| `pending` | `pending` | Check pending |

**API Fallback Behavior:**

When a required check has **no integration ID** specified in the ruleset (meaning it can accept status from any GitHub App or action), the controller uses sequential fallback:

1. **First**: Query Check Runs API (modern)
2. **If no check runs found**: Query Commit Status API (legacy)
3. **Check Runs take precedence**: If both exist, use check run status

This ensures compatibility with both modern GitHub Actions/Apps (Check Runs API) and legacy CI systems (Commit Status API) while minimizing API calls.

**When fallback occurs:**
- Ruleset check has **no `integration_id`** → Checks both APIs
- Ruleset check has **specific `integration_id`** → Only checks Check Runs API

#### Other SCM Providers

Phase mappings for GitLab, Bitbucket, and other providers will be documented as support is added.

### Aggregated Phase

For each environment, the controller calculates an aggregated phase:

- **failure**: If any required check is failing
- **pending**: If any required check is pending (and none failing)
- **success**: If all required checks are successful

### Per-Check Polling and Dynamic Requeue

The controller implements two levels of optimization:

**1. Per-Check Polling (within reconciliation)**

During each reconciliation, the controller intelligently decides whether to poll each check:

- **Terminal checks** (success/failure): Only polled if `lastPolledAt` is older than `terminalCheckInterval` (default 10m)
- **Pending checks**: Always polled for timely updates
- **Recently polled terminal checks**: Skipped and previous status is reused

This means if you have 10 checks where 9 are terminal and 1 is pending:
- The 1 pending check is polled every reconciliation
- The 9 terminal checks are only re-polled if they haven't been checked in the last 10 minutes

**2. Dynamic Reconciliation Timing**

The controller adjusts how often it reconciles:

1. **Pending checks exist**: Reconcile every `pendingCheckInterval` (default 1m) for active monitoring
2. **All checks terminal**: Reconcile every `terminalCheckInterval` (default 10m) to detect changes
3. **No checks to monitor**: Reconcile every `safetyNetInterval` (default 1h) as safety net
4. **Cached discovery**: Next reconciliation scheduled at cache expiry time (default 24h)

Together, these optimizations provide fine-grained control over API usage while maintaining responsiveness for pending checks.

## Performance and Caching

### Per-Check Polling Optimization

The controller implements intelligent per-check polling to reduce API calls:

**How it works:**

Each check maintains a `lastPolledAt` timestamp. Before polling a check, the controller checks:
- If the check is **terminal** (success/failure) AND was polled within `terminalCheckInterval`
  - **Skip polling** and reuse the cached status
- If the check is **pending** OR hasn't been polled recently
  - **Poll the check** and update `lastPolledAt`

**Benefits:**

| Scenario | Before Optimization | After Optimization | Savings |
|----------|--------------------|--------------------|---------|
| 5 terminal checks, 1 pending | All 6 polled every 1m | 1 polled every 1m, 5 every 10m | 83% fewer API calls |
| 10 terminal checks | All 10 polled every 1m | All 10 polled every 10m | 90% fewer API calls |
| Mix of 20 checks (18 terminal, 2 pending) | All 20 every 1m | 2 every 1m, 18 every 10m | 90% fewer API calls |

**Example:**

```bash
$ kubectl get requiredcheckcommitstatuses my-app -o yaml
status:
  environments:
    - branch: environment/staging
      requiredChecks:
        - name: lint
          phase: success
          lastPolledAt: "2026-01-29T22:48:49Z"  # Polled 5 min ago, skip for 5 more min
        - name: test
          phase: success
          lastPolledAt: "2026-01-29T22:48:49Z"  # Polled 5 min ago, skip for 5 more min
        - name: e2e
          phase: pending
          lastPolledAt: "2026-01-29T22:54:02Z"  # Polled 16 sec ago, poll every 1m
```

In this example, only `e2e` is polled on the next reconciliation. The terminal checks are skipped until 10 minutes have elapsed since their last poll.

### Controller-Level Caching

The controller implements caching to reduce SCM API calls.

**What is cached:**

The cache stores the **list of required check names** discovered from your SCM provider's branch protection rules (e.g., GitHub Rulesets). This is the answer to the question: "What checks are required for this branch?"

- Cached data: Check names from branch protection API (e.g., `["ci-tests", "security-scan", "e2e-tests"]`)
- Cache key: Repository (domain/owner/name) + branch name
- Empty results are cached: If a branch has no protection rules, the empty result is cached

**What is NOT cached:**

The actual **status** of those checks (pending/success/failure) is NOT cached in the discovery cache. However, the controller uses per-check polling optimization to avoid unnecessary API calls:
- Terminal checks (success/failure) are only re-polled if not queried within `terminalCheckInterval`
- Pending checks are polled every `pendingCheckInterval`
- Each check's `lastPolledAt` timestamp tracks when it was last queried

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
- **Required Permissions**: The GitHub App needs the following repository permissions:
  - **"Metadata: Read"** - Required to query rulesets and branch protection rules
  - **"Checks: Read"** - Required to monitor Check Runs API status (modern checks)
  - **"Commit statuses: Read"** - Required to monitor Commit Status API (legacy checks)

- **API Compatibility**: The controller supports both Check Runs API (modern) and Commit Status API (legacy) with automatic fallback for maximum compatibility.

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

3. **Check RequiredCheckCommitStatus status** for errors:
   ```bash
   kubectl get requiredcheckcommitstatus my-app -o yaml
   ```

4. **Verify the PromotionStrategy references a valid GitRepository** with a supported SCM provider

5. **Check controller logs**:
   ```bash
   kubectl logs -n gitops-promoter -l app.kubernetes.io/name=gitops-promoter | grep RequiredCheckCommitStatus
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
1. Increase the `terminalCheckInterval` in ControllerConfiguration to reduce polling frequency for stable checks
2. Verify your SCM provider authentication has sufficient rate limits:
   - **GitHub**: Use a GitHub App (not a personal access token) for higher rate limits
3. Monitor API usage in controller logs
4. Check metrics for rate limit information (if available):
   ```bash
   kubectl get --raw /metrics | grep rate_limit
   ```

### Terminal Check Not Updating Immediately

**Problem**: A terminal check (success/failure) status doesn't update immediately when viewing the resource.

**This is expected behavior** due to per-check polling optimization:

Terminal checks are only re-polled if they haven't been queried within `terminalCheckInterval` (default 10 minutes). This is intentional to reduce API usage.

**To verify the optimization is working:**
```bash
# Check the lastPolledAt timestamp
kubectl get requiredcheckcommitstatuses my-app -o yaml | grep -A4 "requiredChecks:"

# Terminal checks should have older timestamps than pending checks
```

**Solutions**:
1. **Wait for the next poll**: Terminal checks will be re-polled within `terminalCheckInterval`
2. **Force immediate reconciliation**: Trigger a reconciliation to poll all checks:
   ```bash
   kubectl annotate requiredcheckcommitstatuses my-app trigger="$(date +%s)" --overwrite
   ```
3. **Reduce polling interval**: Set a shorter `terminalCheckInterval` in ControllerConfiguration (not recommended for production)

### Permission Error: 403 Resource Not Accessible

**Problem**: Logs show error: `403 Resource not accessible by integration` when polling commit status.

**Full error message**:
```
failed to poll commit status, defaulting to pending
error: failed to get combined status: GET https://api.github.com/repos/.../commits/.../status: 403 Resource not accessible by integration
```

**Root cause**: The GitHub App is missing the "Commit statuses: Read" permission.

**Solution**:

1. **Go to your GitHub App settings**:
   - Navigate to https://github.com/settings/apps (for personal apps)
   - Or Settings → Developer settings → GitHub Apps (for your user/org)
   - Click on your GitHub App

2. **Update repository permissions**:
   - Scroll to "Repository permissions"
   - Find **"Commit statuses"**
   - Change from "No access" to **"Read-only"**
   - Click "Save changes"

3. **Accept the permission change**:
   - GitHub will ask you to accept the new permissions
   - For organization apps, an org owner must approve

4. **Verify the fix**:
   - Wait for the next reconciliation (usually within 1 minute)
   - Check controller logs - the 403 error should be gone
   - Verify checks are now being detected:
     ```bash
     kubectl get requiredcheckcommitstatus -o yaml
     ```

**Note**: This permission is only needed if your CI/CD system uses the legacy Commit Status API. Most modern GitHub Actions and Apps use the Check Runs API, which only requires "Checks: Read" permission. However, for maximum compatibility (supporting both modern and legacy CI systems), it's recommended to enable both permissions.

### Check Not Found Despite Passing in GitHub

**Problem**: A required check shows as `pending` in the controller but shows as passed in GitHub's UI.

**This may be due to API differences**:

GitHub has two APIs for reporting check status:
- **Check Runs API** (modern) - Used by GitHub Actions and most modern GitHub Apps
- **Commit Status API** (legacy) - Used by older CI systems and some integrations

**Diagnosis**:

1. **Check which API your CI system uses**:
   - View your commit on GitHub
   - Open browser DevTools → Network tab
   - Look for API calls to either:
     - `/repos/.../commits/.../check-runs` (Check Runs API)
     - `/repos/.../commits/.../status` (Commit Status API)

2. **Check if ruleset has integration_id**:
   - Go to repository Settings → Rules → Rulesets
   - View the ruleset for your branch
   - Check if the required check specifies a GitHub App (integration_id)
   - If **no integration_id**: Controller checks both APIs ✅
   - If **has integration_id**: Controller only checks Check Runs API

**Solutions**:

1. **If your CI uses Commit Status API but ruleset specifies integration_id**:
   - Remove the integration_id from the ruleset
   - This allows the controller to check both APIs
   - Note: This also allows any app/action to report the check

2. **If your CI should use Check Runs API but uses Commit Status API**:
   - Update your CI configuration to use Check Runs API
   - Most modern CI systems support this

3. **Verify the check name matches exactly** (case-sensitive)
   - The check name in the ruleset must match the check name reported by your CI

4. **Check controller logs** for fallback behavior:
   ```bash
   kubectl logs -n gitops-promoter -l app.kubernetes.io/name=gitops-promoter | grep "commit status"
   # Look for: "no check runs found, trying commit status API"
   ```
