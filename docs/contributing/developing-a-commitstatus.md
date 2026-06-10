# Commit Status Controller Best Practices

This document outlines best practices for implementing custom commit status controllers in GitOps Promoter.

## Videos

- [GitOps Promoter Commit Status Controllers Overview](https://www.youtube.com/watch?v=Usi38ly1pe0) - An introduction to commit status controllers and their role in GitOps Promoter.

## Required Labels

All commit status controllers should set the following standard labels on the `CommitStatus` resources they create. Use `utils.CommitStatusStandardLabels(parent, branch, key)` to set all three at once.

### 1. Commit Status Label

```go
commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = "your-controller-key"
```

**Purpose:** The value must equal the gate `key` from the parent CR's `spec.key` (and the same `key` in the PromotionStrategy's `activeCommitStatuses` or `proposedCommitStatuses`). Controllers set this automatically; end users configure `spec.key` on the gate CR, not this label.

**Usage in PromotionStrategy:**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  activeCommitStatuses:
    - key: argocd-health  # same as ArgoCDCommitStatus.spec.key
    - key: timer          # same as TimedCommitStatus.spec.key
```

### 2. Environment Label

```go
commitStatus.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
```

**Purpose:** This label identifies which environment/branch the commit status applies to. It enables efficient filtering and querying of commit statuses by environment.

**Value:** The environment branch name (e.g., `"environment/development"`, `"environment/staging"`), converted to a Kubernetes-safe label value using `utils.KubeSafeLabel()`.

**Benefits:**
- Easy filtering: `kubectl get commitstatus -l promoter.argoproj.io/environment=environment-development`
- Efficient lookups in controllers
- Clear organizational structure

### 3. Use a Unique Commit Status Key in Shared Active Branch Mode

If multiple PromotionStrategies share the same active branch (via `PromotionStrategy.spec.activePath`), commit statuses
for different apps can target the same active commit SHA. In this setup, controllers should use distinct
`CommitStatusLabel` keys per app/controller domain (for example `argocd-health-payments`), so gating remains isolated.

## Existing Controllers

### ArgoCDCommitStatus Controller

Uses `ArgoCDCommitStatus.spec.key` (default `argocd-health`).

### TimedCommitStatus Controller

Uses `TimedCommitStatus.spec.key` (default `timer`).

## Additional Best Practices

### Owner References

Always set controller references for proper garbage collection:

```go
if err := ctrl.SetControllerReference(owner, &commitStatus, r.Scheme); err != nil {
    return fmt.Errorf("failed to set controller reference: %w", err)
}
```

This ensures that when the parent resource (e.g., ArgoCDCommitStatus, TimedCommitStatus) is deleted, all associated CommitStatus resources are automatically cleaned up.

### Naming Convention

#### SCM status name (`CommitStatus.spec.name`)

Built-in controllers use `{spec.key}/{environment-branch}` for the name shown in the SCM (for example `argocd-health/environment/staging`). The `key` is the same value the PromotionStrategy references in `activeCommitStatuses` or `proposedCommitStatuses`.

```go
commitStatus.Spec.Name = key + "/" + branch
commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = key
```

#### Kubernetes resource name

Use `utils.CommitStatusResourceName` for the CommitStatus **resource** name (distinct from `spec.name`):

```go
resourceName := utils.CommitStatusResourceName(ctx, parent, branch)
```

`parent` may omit `TypeMeta.Kind` (common on typed objects from client `Get` in tests); the helper resolves Kind from the runtime scheme via `apiutil.GVKForObject` and panics only if resolution fails.

The kebab-case gate stem is derived from the parent Kind (for example `TimedCommitStatus` → `timed`, `WebRequestCommitStatus` → `web-request`, `ArgoCDCommitStatus` → `argo-cd`). The same stem is used for the resource-name suffix and for the parent-gate label key (`promoter.argoproj.io/<stem>-commit-status`).

The helper applies `KubeSafeUniqueName` to `parent.metadata.name-branch-<stem>`.

Example: `my-app-environment-development-timed-<hash>` (or `...-argo-cd-<hash>` for Argo CD gates)

Set the standard CommitStatus labels with `utils.CommitStatusStandardLabels(parent, branch, key)` (parent gate, environment, and commit-status key).

#### Orphan cleanup

At the end of each reconcile, call `utils.CleanupOrphanedCommitStatuses` with the current valid `[]*CommitStatus` slice. The helper derives the parent gate label from the owner's Kind and removes owned CommitStatuses that are no longer in the valid set.

### Custom Labels

You can add additional labels specific to your controller, but the standard labels above are recommended:

```go
commitStatus.Labels["my-controller.example.com/custom-info"] = "value"
```

## Why These Labels Matter

1. **Discoverability**: Users and tools can easily find all commit statuses created by a specific controller
2. **Debugging**: When troubleshooting, you can quickly identify which controller created a commit status
3. **Filtering**: Efficient queries like "show me all timer gates for the staging environment"
4. **Consistency**: Standard labels create a predictable API across all commit status controllers
5. **Integration**: Other controllers and tools can rely on these labels for their logic

## Designing Good Commit Status Descriptions

The `Description` field in a CommitStatus is displayed to users in their SCM provider (GitHub, GitLab, etc.). Well-crafted descriptions improve user experience by clearly communicating what's happening and reducing confusion.

### Use Action-Oriented Language

Use active, present-tense language to convey the current state of the system. This creates a sense that the system is continuously monitoring and reporting status, not just recording historical events.

**Philosophy:** Commit statuses should describe "what is happening now" rather than "what happened." This applies to all phases:
- **Pending:** Use progressive verbs (-ing) to show active work
- **Success:** Use present tense to describe the current healthy state  
- **Failure:** Use present tense to describe the current problem

### Guidelines by Phase

#### Pending Phase

Use present participles (verbs ending with -ing) to indicate active monitoring or processing:

- ✅ **Use:** "Waiting for approval"
  - ❌ **Avoid:** "Approval pending"
- ✅ **Use:** "Checking application health"
  - ❌ **Avoid:** "Health check in progress"
- ✅ **Use:** "Syncing to environment"
  - ❌ **Avoid:** "Sync scheduled"
- ✅ **Use:** "Monitoring deployment status"
  - ❌ **Avoid:** "Deployment queued"

#### Success Phase

Use present tense to describe the current state:

- ✅ **Use:** "All applications are healthy"
  - ❌ **Avoid:** "Health checks passed"
- ✅ **Use:** "Approval is granted"
  - ❌ **Avoid:** "Approved successfully"
- ✅ **Use:** "Time requirement is met"
  - ❌ **Avoid:** "Timer completed"
- ✅ **Use:** "Environment is synced"
  - ❌ **Avoid:** "Sync finished"

#### Failure Phase

Use present tense to describe the current problem:

- ✅ **Use:** "Applications are degraded"
  - ❌ **Avoid:** "Health check failed"
- ✅ **Use:** "Approval is denied"
  - ❌ **Avoid:** "Approval was rejected"
- ✅ **Use:** "Deployment is unhealthy: pods not ready"
  - ❌ **Avoid:** "Deployment failed"
- ✅ **Use:** "Sync is timing out"
  - ❌ **Avoid:** "Sync timed out"

### Additional Tips

1. **Be specific**: Include relevant details when failures occur (e.g., "Failed health check: 2/5 replicas ready")
2. **Stay concise**: Keep descriptions under 100 characters when possible
3. **Avoid jargon**: Use terminology that's clear to all users, not just Kubernetes experts
4. **Be consistent**: Use similar phrasing across your controller for related states
5. **Include context**: When helpful, mention the environment or application name

### Example Implementation

```go
// Pending phase - active monitoring
commitStatus.Spec.Phase = promoterv1alpha1.CommitPhasePending
commitStatus.Spec.Description = fmt.Sprintf("Waiting for %d/%d applications to sync", synced, total)

// Success phase - current state
commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
commitStatus.Spec.Description = fmt.Sprintf("All %d applications are healthy", total)

// Failure phase - current problem
commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseFailure
commitStatus.Spec.Description = fmt.Sprintf("Applications are degraded: %s", errorDetail)
```

## Triggering Reconciliation of ChangeTransferPolicies

When your commit status controller detects important state transitions (e.g., a gate transitioning from pending to success), you may want to trigger immediate reconciliation of the affected ChangeTransferPolicy to minimize promotion latency.

### The Pattern

Use the `EnqueueCTP` function to trigger reconciliation of the **specific ChangeTransferPolicy** for the environment that changed. This approach directly enqueues a reconcile request without modifying the CTP object.

#### Controller Setup

Add the `EnqueueCTP` field to your reconciler struct:

```go
type MyCommitStatusReconciler struct {
    client.Client
    Scheme      *runtime.Scheme
    Recorder    record.EventRecorder
    SettingsMgr *settings.Manager

    // EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
    EnqueueCTP CTPEnqueueFunc
}
```

#### Triggering Reconciliation

Create a method to trigger CTP reconciliation when state transitions:

```go
func (r *MyCommitStatusReconciler) touchChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string) {
    logger := log.FromContext(ctx)

    for _, envBranch := range transitionedEnvironments {
        // Generate the ChangeTransferPolicy name using the same logic as the PromotionStrategy controller
        ctpName := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(ps.Name, envBranch))

        logger.Info("Triggering ChangeTransferPolicy reconciliation",
            "changeTransferPolicy", ctpName,
            "branch", envBranch)

        // Use the enqueue function to trigger reconciliation
        if r.EnqueueCTP != nil {
            r.EnqueueCTP(ps.Namespace, ctpName)
        }
    }
}
```

#### Wiring in main.go

Pass the enqueue function when creating your controller:

```go
if err := (&controller.MyCommitStatusReconciler{
    Client:      localManager.GetClient(),
    Scheme:      localManager.GetScheme(),
    Recorder:    localManager.GetEventRecorderFor("MyCommitStatus"),
    SettingsMgr: settingsMgr,
    EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
}).SetupWithManager(processSignalsCtx, localManager); err != nil {
    panic(fmt.Errorf("unable to create MyCommitStatus controller: %w", err))
}
```

## Validation

To verify your controller sets labels correctly, check a created CommitStatus:

```bash
kubectl get commitstatus <name> -o yaml
```

You should see:

```yaml
metadata:
  labels:
    promoter.argoproj.io/commit-status: "your-key"
    promoter.argoproj.io/environment: "environment-development"
```
