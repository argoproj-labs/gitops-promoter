# Utilities for External Commit Status Controllers

This document lists the internal utility functions that external commit status controllers would need to copy or adapt. These functions are currently in `internal/` and cannot be imported directly.

## Overview

External commit status controllers don't need to interact with Git/SCM providers directly. They create `CommitStatus` resources, and the built-in `CommitStatus` controller handles pushing statuses to GitHub/GitLab/Forgejo.

Your controller needs to:
1. Create/update `CommitStatus` resources with proper naming, labels, and owner references
2. Optionally trigger ChangeTransferPolicy reconciliation when states change
3. Handle standard reconciliation patterns (conditions, events)

---

## 1. Resource Naming

### `KubeSafeUniqueName`

Creates deterministic, collision-free Kubernetes resource names by normalizing the input and appending a hash.

**Source:** `internal/utils/utils.go`

```go
import (
    "context"
    "hash/fnv"
    "regexp"
    "strconv"
    "strings"

    "sigs.k8s.io/controller-runtime/pkg/log"
)

var m1 = regexp.MustCompile("[^a-zA-Z0-9]+")

// KubeSafeUniqueName Creates a safe name by replacing all non-alphanumeric characters with a hyphen 
// and truncating to a max of 255 characters, then appending a hash of the name.
func KubeSafeUniqueName(ctx context.Context, name string) string {
    name = m1.ReplaceAllString(name, "-")
    name = strings.ToLower(name)

    h := fnv.New32a()
    _, err := h.Write([]byte(name))
    if err != nil {
        log.FromContext(ctx).Error(err, "Failed to write to hash")
    }
    hash := strconv.FormatUint(uint64(h.Sum32()), 16)

    if name[len(name)-1] == '-' {
        name = name[:len(name)-1]
    }
    name = name + "-" + hash
    return TruncateString(name, 255-len(hash)-1)
}
```

**Usage:**
```go
commitStatusName := KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-mycontroller", parentName, branch))
```

---

### `KubeSafeLabel`

Creates valid Kubernetes label values (max 63 characters). Truncates from the beginning to preserve unique suffixes.

**Source:** `internal/utils/utils.go`

```go
// KubeSafeLabel Creates a safe label by truncating from the beginning of 'name' to a max of 63 characters.
// If the name starts with a hyphen it will be removed.
// We truncate from beginning so that we can keep the unique hash at the end of the name.
func KubeSafeLabel(name string) string {
    if name == "" {
        return ""
    }
    name = m1.ReplaceAllString(name, "-")
    name = TruncateStringFromBeginning(name, 63)
    if name[0] == '-' {
        name = name[1:]
    }
    return name
}
```

**Usage:**
```go
commitStatus.Labels[promoterv1alpha1.EnvironmentLabel] = KubeSafeLabel(branch)
```

---

### String Truncation Helpers

```go
// TruncateString truncates a string to a specified length from the end.
func TruncateString(str string, length int) string {
    if length <= 0 {
        return ""
    }
    truncated := ""
    count := 0
    for _, char := range str {
        truncated += string(char)
        count++
        if count >= length {
            break
        }
    }
    return truncated
}

// TruncateStringFromBeginning truncates from front of string.
// For example, if the string is "abcdefg" and length is 3, it will return "efg".
func TruncateStringFromBeginning(str string, length int) string {
    if length <= 0 {
        return ""
    }
    if len(str) <= length {
        return str
    }
    return str[len(str)-length:]
}
```

---

## 2. ChangeTransferPolicy Name Generation

For triggering CTP reconciliation when your commit status transitions.

**Source:** `internal/utils/utils.go`

```go
// GetChangeTransferPolicyName returns a name for the ChangeTransferPolicy 
// based on the promotion strategy name and environment branch.
func GetChangeTransferPolicyName(promotionStrategyName, environmentBranch string) string {
    return fmt.Sprintf("%s-%s", promotionStrategyName, environmentBranch)
}
```

**Usage:**
```go
ctpName := KubeSafeUniqueName(ctx, GetChangeTransferPolicyName(ps.Name, envBranch))
```

---

## 3. Condition Types and Reasons

**Source:** `internal/types/conditions/conditions.go`

```go
// CommonType is a type of condition.
type CommonType string

// CommonReason is a reason for a condition.
type CommonReason string

// Condition types
const (
    // Ready is the condition type for a resource that is ready.
    Ready CommonType = "Ready"
)

// Reasons that apply to all CRDs
const (
    // ReconciliationError is the reason for an error during reconciliation.
    ReconciliationError CommonReason = "ReconciliationError"
    // ReconciliationSuccess is the reason for a successful reconciliation.
    ReconciliationSuccess CommonReason = "ReconciliationSuccess"
)

// Reasons specific to commit status controllers
const (
    // CommitStatusesNotReady is the reason when child commit statuses are not ready.
    CommitStatusesNotReady CommonReason = "CommitStatusesNotReady"
)
```

---

## 4. Event Constants

**Source:** `internal/types/constants/events.go`

```go
const (
    // CommitStatusSetReason indicates that a commit status has been set.
    CommitStatusSetReason = "CommitStatusSet"

    // OrphanedCommitStatusDeletedReason indicates that an orphaned CommitStatus has been deleted.
    OrphanedCommitStatusDeletedReason = "OrphanedCommitStatusDeleted"
    // OrphanedCommitStatusDeletedMessage is the message for a deleted orphaned CommitStatus.
    OrphanedCommitStatusDeletedMessage = "Deleted orphaned CommitStatus %s"
)
```

---

## 5. Reconciliation Helpers

### `StatusConditionUpdater` Interface

**Source:** `internal/utils/utils.go`

```go
import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// StatusConditionUpdater defines the interface for objects that can have their status conditions updated
type StatusConditionUpdater interface {
    client.Object
    GetConditions() *[]metav1.Condition
}
```

---

### `HandleReconciliationResult`

Standardizes error handling, condition updates, and event recording at the end of reconciliation.

**Source:** `internal/utils/utils.go`

```go
import (
    "context"
    "fmt"
    "runtime/debug"
    "time"

    k8serrors "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/tools/record"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleReconciliationResult handles reconciliation results for any object with status conditions.
func HandleReconciliationResult(
    ctx context.Context,
    startTime time.Time,
    obj StatusConditionUpdater,
    client client.Client,
    recorder record.EventRecorder,
    err *error,
) {
    // Recover from any panic and convert it to an error.
    if r := recover(); r != nil {
        logger := log.FromContext(ctx)
        logger.Error(nil, "recovered from panic in reconciliation", "panic", r, "trace", string(debug.Stack()))
        *err = fmt.Errorf("panic in reconciliation: %v", r)
    }

    logger := log.FromContext(ctx)

    logger.Info(fmt.Sprintf("Reconciling %s End", obj.GetObjectKind().GroupVersionKind().Kind), "duration", time.Since(startTime))
    if obj.GetName() == "" && obj.GetNamespace() == "" {
        // This happens when the Get in the Reconcile function returns "not found." It's expected and safe to skip.
        logger.V(4).Info(obj.GetObjectKind().GroupVersionKind().Kind + " not found, skipping reconciliation")
        return
    }

    conditions := obj.GetConditions()
    readyCondition := meta.FindStatusCondition(*conditions, string(Ready))
    if readyCondition == nil {
        // If the condition hasn't already been set by the caller, assume success.
        readyCondition = &metav1.Condition{
            Type:               string(Ready),
            Status:             metav1.ConditionTrue,
            Reason:             string(ReconciliationSuccess),
            Message:            "Reconciliation successful",
            ObservedGeneration: obj.GetGeneration(),
            LastTransitionTime: metav1.NewTime(time.Now()),
        }
    }

    if *err == nil {
        eventType := "Normal"
        if readyCondition.Status == metav1.ConditionFalse {
            eventType = "Warning"
        }
        recorder.Eventf(obj, eventType, readyCondition.Reason, readyCondition.Message)
        if updateErr := updateReadyCondition(ctx, obj, client, conditions, readyCondition.Status, readyCondition.Reason, readyCondition.Message); updateErr != nil {
            *err = fmt.Errorf("failed to update status with success condition: %w", updateErr)
        }
        return
    }

    if !k8serrors.IsConflict(*err) {
        recorder.Eventf(obj, "Warning", string(ReconciliationError), "Reconciliation failed: %v", *err)
    }
    if updateErr := updateReadyCondition(ctx, obj, client, conditions, metav1.ConditionFalse, string(ReconciliationError), fmt.Sprintf("Reconciliation failed: %s", *err)); updateErr != nil {
        *err = fmt.Errorf("failed to update status with error condition with error %q: %w", *err, updateErr)
    }
}

func updateReadyCondition(ctx context.Context, obj StatusConditionUpdater, client client.Client, conditions *[]metav1.Condition, status metav1.ConditionStatus, reason, message string) error {
    condition := metav1.Condition{
        Type:               string(Ready),
        Status:             status,
        Reason:             reason,
        Message:            message,
        ObservedGeneration: obj.GetGeneration(),
    }

    if changed := meta.SetStatusCondition(conditions, condition); changed {
        if updateErr := client.Status().Update(ctx, obj); updateErr != nil {
            return fmt.Errorf("failed to update status condition: %w", updateErr)
        }
    }
    return nil
}
```

**Usage:**
```go
func (r *MyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
    startTime := time.Now()
    var myResource MyResourceType
    defer HandleReconciliationResult(ctx, startTime, &myResource, r.Client, r.Recorder, &err)
    
    // ... reconciliation logic ...
}
```

---

### `InheritNotReadyConditionFromObjects`

Propagates Ready conditions from child CommitStatus objects to the parent controller resource.

**Source:** `internal/utils/utils.go`

```go
import (
    "fmt"
    "slices"
    "strings"
    "time"

    "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InheritNotReadyConditionFromObjects sets the Ready condition of the parent to False 
// if any of the child objects are not ready. This will override any existing Ready condition on the parent.
//
// All child objects must be non-nil.
func InheritNotReadyConditionFromObjects[T StatusConditionUpdater](parent StatusConditionUpdater, notReadyReason CommonReason, childObjs ...T) {
    condition := metav1.Condition{
        Type:               string(Ready),
        Status:             metav1.ConditionUnknown,
        Reason:             string(notReadyReason),
        ObservedGeneration: parent.GetGeneration(),
        LastTransitionTime: metav1.NewTime(time.Now()),
    }

    // Sort child objects by name for consistent results
    sortedChildObjs := slices.Clone(childObjs)
    slices.SortFunc(sortedChildObjs, func(a, b T) int {
        return strings.Compare(a.GetName(), b.GetName())
    })

    for _, childObj := range sortedChildObjs {
        childObjKind := childObj.GetObjectKind().GroupVersionKind().Kind
        childObjReady := meta.FindStatusCondition(*childObj.GetConditions(), string(Ready))

        if childObjReady == nil {
            condition.Message = fmt.Sprintf("%s %q Ready condition is missing", childObjKind, childObj.GetName())
            meta.SetStatusCondition(parent.GetConditions(), condition)
            return
        }
        if childObjReady.ObservedGeneration != childObj.GetGeneration() {
            condition.Message = fmt.Sprintf("%s %q Ready condition is not up to date", childObjKind, childObj.GetName())
            meta.SetStatusCondition(parent.GetConditions(), condition)
            return
        }
        if childObjReady.Status != metav1.ConditionTrue {
            condition.Message = fmt.Sprintf("%s %q is not Ready because %q: %s", childObjKind, childObj.GetName(), childObjReady.Reason, childObjReady.Message)
            condition.Status = childObjReady.Status
            meta.SetStatusCondition(parent.GetConditions(), condition)
            return
        }
    }
}
```

**Usage:**
```go
// After creating/updating CommitStatus objects
InheritNotReadyConditionFromObjects(&myResource, CommitStatusesNotReady, commitStatuses...)
```

---

## 6. Template Rendering (Optional)

If your controller needs URL templating like `ArgoCDCommitStatus`.

**Source:** `internal/utils/template.go`

```go
import (
    "bytes"
    "fmt"
    "net/url"
    "text/template"

    sprig "github.com/go-task/slim-sprig/v3"
)

var sanitizedSprigFuncMap = sprig.GenericFuncMap()

func init() {
    delete(sanitizedSprigFuncMap, "env")
    delete(sanitizedSprigFuncMap, "expandenv")
    delete(sanitizedSprigFuncMap, "getHostByName")
    sanitizedSprigFuncMap["urlQueryEscape"] = url.QueryEscape
}

// RenderStringTemplate renders a string template with the provided data.
func RenderStringTemplate(templateStr string, data any, options ...string) (string, error) {
    tmpl, err := template.New("").Funcs(sanitizedSprigFuncMap).Parse(templateStr)
    if err != nil {
        return "", fmt.Errorf("failed to parse template: %w", err)
    }

    // Apply options to the template
    for _, option := range options {
        tmpl = tmpl.Option(option)
    }

    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("failed to execute template: %w", err)
    }

    return buf.String(), nil
}
```

---

## What You Don't Need

External controllers **do not need** the SCM integration code from `internal/scms/`. The workflow is:

1. Your controller creates/updates `CommitStatus` resources
2. The built-in `CommitStatus` controller watches those and pushes statuses to GitHub/GitLab/Forgejo

Your controller just needs to set the `CommitStatus.Spec` fields correctly:

```go
commitStatus.Spec = promoterv1alpha1.CommitStatusSpec{
    RepositoryReference: ps.Spec.RepositoryReference,  // From the PromotionStrategy
    Sha:                 sha,                           // The commit to set status on
    Name:                "mycontroller/" + envBranch,   // The status name
    Phase:               phase,                         // pending, success, or failure
    Description:         message,                       // Human-readable message
    Url:                 optionalUrl,                   // Optional link for more details
}
```

---

## Complete Example

See the existing controllers for complete examples:
- `internal/controller/timedcommitstatus_controller.go` - Simpler example
- `internal/controller/argocdcommitstatus_controller.go` - More complex with multi-cluster support

Key patterns to follow:
1. Set standard labels (`CommitStatusLabel`, `EnvironmentLabel`)
2. Set owner references for garbage collection
3. Use `ctrl.CreateOrUpdate` for idempotent updates
4. Call `InheritNotReadyConditionFromObjects` after managing CommitStatus objects
5. Trigger CTP reconciliation on important state transitions

