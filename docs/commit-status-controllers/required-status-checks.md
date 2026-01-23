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

## Features

- **Global toggle**: Enable/disable via `showRequiredStatusChecks` on PromotionStrategy (disabled by default)
- **Granular visibility**: One CommitStatus resource per required check (not per protection rule)
- **Dynamic polling**: 1 minute intervals when checks are pending, configurable interval otherwise
- **Automatic cleanup**: Removes orphaned CommitStatus resources when checks are removed from branch protection
- **Multi-SCM ready**: Architecture supports multiple SCM providers (GitHub fully supported, others planned)

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

### GitLab Setup (Planned)

For GitLab repositories (when support is added), configure protected branches with pipeline status requirements.

### Other SCM Providers (Planned)

Support for additional SCM providers (Bitbucket, Azure DevOps, Gitea, Forgejo) is planned. Each will integrate with their respective branch protection mechanisms.

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

#### Other SCM Providers

Phase mappings for GitLab, Bitbucket, and other providers will be documented as support is added.

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

### SCM Provider Support

Currently, this feature only works with GitHub repositories using GitHub Rulesets. Support for additional SCM providers is planned:

- **GitHub**: ✅ Fully supported via Rulesets API (classic branch protection not supported)
- **GitLab**: 🚧 Planned (protected branches with pipeline requirements)
- **Bitbucket**: 🚧 Planned (branch restrictions and required checks)
- **Azure DevOps**: 🚧 Planned (branch policies with status checks)
- **Gitea/Forgejo**: 🚧 Planned (branch protection rules)

The controller is architected to support multiple SCM providers through the `BranchProtectionProvider` interface.

### GitHub-Specific Limitations

- **Rulesets Only**: GitHub classic branch protection is not supported. You must migrate to GitHub Rulesets.
- **Repository Admin Permission Required**: The GitHub App needs "Administration: Read" permission to query rulesets.

### Check Discovery Timing

The controller queries branch protection rules on every reconcile loop. Changes to protection rules are detected within the polling interval (1 minute when pending, configured interval otherwise).

## Troubleshooting

### No CommitStatus Resources Created

**Problem**: `showRequiredStatusChecks` is enabled but no CommitStatus resources are created.

**Solutions**:
1. Verify branch protection is configured in your SCM provider:
   - **GitHub**: Verify Rulesets are configured (not classic branch protection)
   - **Other SCMs**: Verify the SCM provider is supported (currently only GitHub)
2. Check RequiredStatusCheckCommitStatus status for errors:
   ```bash
   kubectl get requiredstatuscheckcommitstatus my-app -o yaml
   ```
3. Verify the PromotionStrategy references a valid GitRepository with a supported SCM provider
4. Check controller logs:
   ```bash
   kubectl logs -n gitops-promoter -l app.kubernetes.io/name=gitops-promoter | grep RequiredStatusCheckCommitStatus
   ```

### CommitStatus Stuck in Pending

**Problem**: A CommitStatus is stuck in pending state.

**Solutions**:
1. Check if the status check actually exists and is running in your SCM provider:
   - **GitHub**: Go to the PR → "Checks" tab
   - **GitLab**: Go to the MR → "Pipelines" tab
   - Verify the check/pipeline is actually running
2. Verify the check name matches exactly (case-sensitive)
3. Check if the check is excluded in environment configuration
4. Review controller logs for polling errors

### Wrong Checks Discovered

**Problem**: The controller discovers checks that shouldn't be there, or misses checks that should be there.

**Solutions**:
1. Verify branch protection configuration in your SCM provider:
   - **GitHub**: Settings → Rules → Rulesets (check which rulesets apply to the branch)
   - **GitLab**: Settings → Repository → Protected branches
   - Ensure protection rules target the correct branches
2. Verify the branch name matches exactly (case-sensitive)
3. Check for inherited organization/group-level protection rules

### Rate Limiting

**Problem**: SCM provider API rate limits are being hit.

**Solutions**:
1. Increase the `requeueDuration` in ControllerConfiguration to reduce polling frequency
2. Verify your SCM provider authentication has sufficient rate limits:
   - **GitHub**: Use a GitHub App (not a personal access token) for higher rate limits
   - **GitLab**: Verify your token/app has adequate rate limits
3. Monitor API usage in controller logs
4. Check metrics for rate limit information (if available):
   ```bash
   kubectl get --raw /metrics | grep rate_limit
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
     - Call `BranchProtectionProvider.DiscoverRequiredChecks()` to discover required checks from branch protection rules
     - For each required check:
       - Call `BranchProtectionProvider.PollCheckStatus()` to get check status
       - Create/update CommitStatus resource
     - Calculate aggregated phase
   - Cleanup orphaned CommitStatus resources
   - Trigger CTP reconciliation on phase transitions
   - Calculate dynamic requeue duration

2. **PromotionStrategy Controller Integration**
   - When `showRequiredStatusChecks` is true: create RequiredStatusCheckCommitStatus
   - When `showRequiredStatusChecks` is false: delete RequiredStatusCheckCommitStatus
   - RequiredStatusCheckCommitStatus is owned by PromotionStrategy (cascade delete)

### SCM Provider Interface

The controller uses the `BranchProtectionProvider` interface to support multiple SCM providers:

```go
type BranchProtectionProvider interface {
    // Discover required checks from branch protection rules
    DiscoverRequiredChecks(ctx context.Context, repo *GitRepository, branch string) ([]BranchProtectionCheck, error)

    // Poll current status of a specific check
    PollCheckStatus(ctx context.Context, repo *GitRepository, sha string, checkName string) (CommitStatusPhase, error)
}
```

This abstraction enables support for different SCM providers without changing controller logic.

## See Also

- [Gating Promotions](../gating-promotions.md) - Overview of commit status gating
- [ArgoCD Commit Status Controller](argocd-commit-status.md) - Health-based commit statuses
- [Timed Commit Status Controller](timed-commit-status.md) - Time-based commit statuses
