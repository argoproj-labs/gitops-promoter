# Required Status Check Visibility Controller

## Overview

The Required Status Check Visibility Controller automatically discovers required status checks from GitHub Rulesets (repository rules) and creates CommitStatus resources to provide visibility into what checks are blocking PR merges. This prevents PromotionStrategy from showing "degraded" state (due to failed merge attempts) and instead shows "progressing" state while waiting for required checks to pass.

**Important:** GitHub enforces the required checks via branch protection - this controller simply surfaces them as CommitStatus resources so users can see what GitOps Promoter is waiting on.

## How It Works

1. **Auto-discovery**: The controller queries the GitHub Rulesets API to discover which status checks are required for each target branch
2. **Visibility**: For each required check, it creates a CommitStatus resource with prefix `required-status-check-{checkname}`
3. **Continuous polling**: The controller polls the GitHub Checks API to monitor the status of each required check
4. **State management**: By surfacing required checks as CommitStatus resources, the PromotionStrategy stays in "progressing" state while waiting, avoiding "degraded" from failed merge attempts

## Features

- **Global toggle**: Enable/disable via `showRequiredStatusChecks` on PromotionStrategy (disabled by default)
- **Granular visibility**: One CommitStatus resource per required check (not per ruleset)
- **Dynamic polling**: 1 minute intervals when checks are pending, configurable interval otherwise
- **Automatic cleanup**: Removes orphaned CommitStatus resources when checks are removed from rulesets
- **GitHub-only**: Currently supports GitHub only (designed for future SCM support)

## Configuration

### Enable the Feature

Add `showRequiredStatusChecks: true` to your PromotionStrategy:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  showRequiredStatusChecks: true  # Enable the feature

  repositoryReference:
    name: my-git-repo

  environments:
    - branch: environment/dev
    - branch: environment/staging
    - branch: environment/prod
```

### Configure Polling Interval

The default polling interval is 5 minutes when all checks are passing. You can configure this in ControllerConfiguration:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ControllerConfiguration
metadata:
  name: controller-configuration
  namespace: gitops-promoter
spec:
  requiredStatusCheckCommitStatus:
    workQueue:
      requeueDuration: 5m  # Polling interval when all checks passing
      maxConcurrentReconciles: 5
      rateLimiter:
        exponentialFailure:
          baseDelay: 1s
          maxDelay: 1m
```

**Note:** When any checks are pending, the controller automatically uses a 1 minute polling interval regardless of this configuration.

## GitHub Rulesets Setup

This feature requires GitHub Rulesets (not classic branch protection). Here's how to set it up:

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

## How CommitStatus Resources are Named

Each required check gets its own CommitStatus resource with a predictable naming pattern:

```
required-status-check-{normalized-context}-{hash}
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
        - context: ci-tests
          phase: success
          commitStatusName: required-status-check-ci-tests-abc12345
        - context: security-scan
          phase: pending
          commitStatusName: required-status-check-security-scan-def67890
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

The controller maps GitHub Check Run status to CommitStatus phases:

| GitHub Status | GitHub Conclusion | CommitStatus Phase |
|--------------|-------------------|-------------------|
| `completed` | `success` | `success` |
| `completed` | `neutral` | `success` |
| `completed` | `skipped` | `success` |
| `completed` | `failure` | `failure` |
| `completed` | `cancelled` | `failure` |
| `completed` | `timed_out` | `failure` |
| `queued` | - | `pending` |
| `in_progress` | - | `pending` |

### Aggregated Phase

For each environment, the controller calculates an aggregated phase:

- **failure**: If any required check is failing
- **pending**: If any required check is pending (and none failing)
- **success**: If all required checks are successful

### Dynamic Requeue

The controller uses dynamic requeue intervals:

- **1 minute**: When any checks are pending in any environment
- **Configured interval** (default 5 minutes): When all checks are passing in all environments

This ensures timely updates when checks are actively running while reducing API load when stable.

## Limitations

### GitHub Only

Currently, this feature only works with GitHub repositories. Support for other SCMs (GitLab, Bitbucket, etc.) may be added in the future.

### Rulesets Only

This feature uses the GitHub Rulesets API, not the classic branch protection API. You must use GitHub Rulesets for required checks to be discovered.

### Check Discovery Timing

The controller queries GitHub Rulesets on every reconcile loop. Changes to rulesets are detected within the polling interval (1 minute when pending, configured interval otherwise).

## Troubleshooting

### No CommitStatus Resources Created

**Problem**: `showRequiredStatusChecks` is enabled but no CommitStatus resources are created.

**Solutions**:
1. Verify GitHub Rulesets are configured (not classic branch protection)
2. Check RequiredStatusCheckCommitStatus status for errors:
   ```bash
   kubectl get requiredstatuscheckcommitstatus my-app -o yaml
   ```
3. Verify the PromotionStrategy references a valid GitRepository with GitHub provider
4. Check controller logs:
   ```bash
   kubectl logs -n gitops-promoter -l app.kubernetes.io/name=gitops-promoter | grep RequiredStatusCheckCommitStatus
   ```

### CommitStatus Stuck in Pending

**Problem**: A CommitStatus is stuck in pending state.

**Solutions**:
1. Check if the GitHub check actually exists and is running:
   - Go to the PR on GitHub
   - View the "Checks" tab
2. Verify the check name matches exactly (case-sensitive)
3. Check if the check is excluded in environment configuration

### Wrong Checks Discovered

**Problem**: The controller discovers checks that shouldn't be there, or misses checks that should be there.

**Solutions**:
1. Verify ruleset configuration on GitHub:
   - Settings → Rules → Rulesets
   - Check which rulesets apply to the branch
2. Ensure rulesets target the correct branches

### Rate Limiting

**Problem**: GitHub API rate limits are being hit.

**Solutions**:
1. Increase the `requeueDuration` in ControllerConfiguration to reduce polling frequency
2. Verify the PromotionStrategy is using a GitHub App with sufficient rate limits (not a personal access token)
3. Check metrics to see GitHub API usage:
   ```bash
   kubectl get --raw /metrics | grep github_rate_limit
   ```

## Examples

### Basic Setup

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: github
spec:
  secretRef:
    name: github-secret
  github:
    domain: github.com
    appID: 123456
    installationID: 789012
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: my-app-repo
spec:
  github:
    owner: my-org
    name: my-app
  scmProviderRef:
    name: github
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  showRequiredStatusChecks: true
  repositoryReference:
    name: my-app-repo
  environments:
    - branch: environment/dev
    - branch: environment/staging
    - branch: environment/prod
```

## Architecture

### CRD Hierarchy

```
PromotionStrategy
  ↓ (creates when showRequiredStatusChecks: true)
RequiredStatusCheckCommitStatus
  ↓ (creates one per required check per environment)
CommitStatus (multiple)
```

### Controller Flow

1. **Reconcile RequiredStatusCheckCommitStatus**
   - Check if `showRequiredStatusChecks` is enabled
   - Get all ChangeTransferPolicies for the PromotionStrategy
   - For each environment:
     - Call `GitHub.Repositories.GetRulesForBranch()` to discover required checks
     - For each required check:
       - Call `GitHub.Checks.ListCheckRunsForRef()` to get check status
       - Create/update CommitStatus resource
     - Calculate aggregated phase
   - Cleanup orphaned CommitStatus resources
   - Trigger CTP reconciliation on phase transitions
   - Calculate dynamic requeue duration

2. **PromotionStrategy Controller Integration**
   - When `showRequiredStatusChecks` is true: create RequiredStatusCheckCommitStatus
   - When `showRequiredStatusChecks` is false: delete RequiredStatusCheckCommitStatus
   - RequiredStatusCheckCommitStatus is owned by PromotionStrategy (cascade delete)

## See Also

- [Gating Promotions](../gating-promotions.md) - Overview of commit status gating
- [ArgoCD Commit Status Controller](argocd-commit-status.md) - Health-based commit statuses
- [Timed Commit Status Controller](timed-commit-status.md) - Time-based commit statuses
