# Commit Status Controller Best Practices

This document outlines best practices for implementing custom commit status controllers in GitOps Promoter.

## Videos

- [GitOps Promoter Commit Status Controllers Overview](https://www.youtube.com/watch?v=Usi38ly1pe0) - An introduction to commit status controllers and their role in GitOps Promoter.

## Required Labels

All commit status controllers should set the following standard labels on the `CommitStatus` resources they create:

### 1. Commit Status Label

```go
commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = "your-controller-key"
```

**Purpose:** This label identifies which controller created the commit status. The value should match the `key` used in the PromotionStrategy's `proposedCommitStatuses` configuration.

**Examples:**
- `"argocd-health"` - Used by ArgoCDCommitStatus controller
- `"timer"` - Used by TimedCommitStatus controller
- `"manual-approval"` - Could be used by a manual approval controller

**Usage in PromotionStrategy:**
```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  activeCommitStatuses:
    - key: argocd-health  # Matches the label value
    - key: timer          # Matches the label value
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

## Existing Controllers

### ArgoCDCommitStatus Controller

The ArgoCDCommitStatus controller sets:
- `promoter.argoproj.io/commit-status: "argocd-health"`
- `promoter.argoproj.io/environment: <branch>`

See: `internal/controller/argocdcommitstatus_controller.go` lines 557-560

### TimedCommitStatus Controller

The TimedCommitStatus controller sets:
- `promoter.argoproj.io/commit-status: "timer"`
- `promoter.argoproj.io/environment: <branch>`

See: `internal/controller/timedcommitstatus_controller.go` lines 295-296

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

Use a consistent naming pattern for CommitStatus resources:

```go
commitStatusName := utils.KubeSafeUniqueName(ctx, 
    fmt.Sprintf("%s-%s-%s", parentResourceName, branch, controllerType))
```

Example: `my-app-environment-development-timer`

### Custom Labels

You can add additional labels specific to your controller, but the two standard labels above are recommended:

```go
commitStatus.Labels["my-controller.example.com/custom-info"] = "value"
```

## Why These Labels Matter

1. **Discoverability**: Users and tools can easily find all commit statuses created by a specific controller
2. **Debugging**: When troubleshooting, you can quickly identify which controller created a commit status
3. **Filtering**: Efficient queries like "show me all timer gates for the staging environment"
4. **Consistency**: Standard labels create a predictable API across all commit status controllers
5. **Integration**: Other controllers and tools can rely on these labels for their logic

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
        ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

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

