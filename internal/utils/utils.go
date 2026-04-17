package utils

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// GetScmProviderFromGitRepository retrieves the ScmProvider from the GitRepository reference.
func GetScmProviderFromGitRepository(ctx context.Context, k8sClient client.Client, repositoryRef *promoterv1alpha1.GitRepository, obj metav1.Object) (promoterv1alpha1.GenericScmProvider, error) {
	logger := log.FromContext(ctx)

	var provider promoterv1alpha1.GenericScmProvider
	kind := repositoryRef.Spec.ScmProviderRef.Kind
	switch kind {
	case promoterv1alpha1.ClusterScmProviderKind:
		var scmProvider promoterv1alpha1.ClusterScmProvider
		objectKey := client.ObjectKey{
			Name: repositoryRef.Spec.ScmProviderRef.Name,
		}

		err := k8sClient.Get(ctx, objectKey, &scmProvider, &client.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get ClusterScmProvider", "name", objectKey.Name)
			return nil, fmt.Errorf("failed to get ClusterScmProvider: %w", err)
		}
		provider = &scmProvider
	case promoterv1alpha1.ScmProviderKind:
		var scmProvider promoterv1alpha1.ScmProvider
		objectKey := client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      repositoryRef.Spec.ScmProviderRef.Name,
		}

		err := k8sClient.Get(ctx, objectKey, &scmProvider, &client.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get ScmProvider", "namespace", obj.GetNamespace(), "name", objectKey.Name)
			return nil, fmt.Errorf("failed to get ScmProvider: %w", err)
		}
		provider = &scmProvider
	default:
		return nil, fmt.Errorf("unsupported ScmProvider kind: %s", kind)
	}

	if (repositoryRef.Spec.GitHub != nil && provider.GetSpec().GitHub == nil) ||
		(repositoryRef.Spec.GitLab != nil && provider.GetSpec().GitLab == nil) ||
		(repositoryRef.Spec.Forgejo != nil && provider.GetSpec().Forgejo == nil) ||
		(repositoryRef.Spec.Gitea != nil && provider.GetSpec().Gitea == nil) ||
		(repositoryRef.Spec.BitbucketCloud != nil && provider.GetSpec().BitbucketCloud == nil) ||
		(repositoryRef.Spec.AzureDevOps != nil && provider.GetSpec().AzureDevOps == nil) ||
		(repositoryRef.Spec.Fake != nil && provider.GetSpec().Fake == nil) {
		return nil, errors.New("wrong ScmProvider configured for Repository")
	}

	return provider, nil
}

// GetGitRepositoryFromObjectKey returns the GitRepository object from the repository reference
func GetGitRepositoryFromObjectKey(ctx context.Context, k8sClient client.Client, objectKey client.ObjectKey) (*promoterv1alpha1.GitRepository, error) {
	var gitRepo promoterv1alpha1.GitRepository
	err := k8sClient.Get(ctx, objectKey, &gitRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	return &gitRepo, nil
}

// getScmProviderAndSecretFromGitRepository returns the ScmProvider and Secret for the given GitRepository.
// Used by GetScmProviderAndSecretFromRepositoryReference and GetScmProviderSecretAndGitRepositoryFromRepositoryReference.
func getScmProviderAndSecretFromGitRepository(ctx context.Context, k8sClient client.Client, controllerNamespace string, gitRepo *promoterv1alpha1.GitRepository, obj metav1.Object) (promoterv1alpha1.GenericScmProvider, *v1.Secret, error) {
	logger := log.FromContext(ctx)
	scmProvider, err := GetScmProviderFromGitRepository(ctx, k8sClient, gitRepo, obj)
	if err != nil {
		return nil, nil, err
	}

	var secretNamespace string
	if scmProvider.GetObjectKind().GroupVersionKind().Kind == promoterv1alpha1.ClusterScmProviderKind {
		secretNamespace = controllerNamespace
	} else {
		secretNamespace = scmProvider.GetNamespace()
	}

	var secret v1.Secret
	objectKey := client.ObjectKey{
		Namespace: secretNamespace,
		Name:      scmProvider.GetSpec().SecretRef.Name,
	}
	err = k8sClient.Get(ctx, objectKey, &secret)
	if err != nil {
		kind := scmProvider.GetObjectKind().GroupVersionKind().Kind
		if k8serrors.IsNotFound(err) {
			logger.Info("Secret not found for "+kind, "namespace", secretNamespace, "name", objectKey.Name)
			return nil, nil, fmt.Errorf("secret from %s not found: %w", kind, err)
		}

		logger.Error(err, "failed to get Secret from "+kind, "namespace", secretNamespace, "name", objectKey.Name)
		return nil, nil, fmt.Errorf("failed to get Secret from %s: %w", kind, err)
	}

	return scmProvider, &secret, nil
}

// GetScmProviderAndSecretFromRepositoryReference retrieves the ScmProvider and its associated Secret from a GitRepository reference.
func GetScmProviderAndSecretFromRepositoryReference(ctx context.Context, k8sClient client.Client, controllerNamespace string, repositoryRef promoterv1alpha1.ObjectReference, obj metav1.Object) (promoterv1alpha1.GenericScmProvider, *v1.Secret, error) {
	gitRepo, err := GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}
	return getScmProviderAndSecretFromGitRepository(ctx, k8sClient, controllerNamespace, gitRepo, obj)
}

// GetScmProviderSecretAndGitRepositoryFromRepositoryReference retrieves the ScmProvider, its Secret, and the GitRepository
// from a repository reference in a single GitRepository GET. Use when the GitRepository is also needed (e.g. scm
// that requires repo owner for GitHub installation resolution).
func GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx context.Context, k8sClient client.Client, controllerNamespace string, repositoryRef promoterv1alpha1.ObjectReference, obj metav1.Object) (promoterv1alpha1.GenericScmProvider, *v1.Secret, *promoterv1alpha1.GitRepository, error) {
	gitRepo, err := GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}
	scmProvider, secret, err := getScmProviderAndSecretFromGitRepository(ctx, k8sClient, controllerNamespace, gitRepo, obj)
	if err != nil {
		return nil, nil, nil, err
	}
	return scmProvider, secret, gitRepo, nil
}

// TruncateString truncates a string to a specified length. If the length is less than or equal to 0, it returns an
// empty string.
func TruncateString(str string, length int) string {
	if length <= 0 {
		return ""
	}
	var truncated strings.Builder
	count := 0
	for _, char := range str {
		truncated.WriteRune(char)
		count++
		if count >= length {
			break
		}
	}
	return truncated.String()
}

// TruncateStringFromBeginning truncates from front of string. For example, if the string is "abcdefg" and length is 3,
// it will return "efg". Truncation is by rune so the result is always valid UTF-8.
func TruncateStringFromBeginning(str string, length int) string {
	if length <= 0 {
		return ""
	}
	runes := []rune(str)
	if len(runes) <= length {
		// Return string(runes), not str: decoding replaces invalid UTF-8 with U+FFFD; the
		// original bytes may not be valid UTF-8 (e.g. a lone continuation byte).
		return string(runes)
	}
	return string(runes[len(runes)-length:])
}

var m1 = regexp.MustCompile("[^a-zA-Z0-9]+")

// GetPullRequestName returns a name for the pull request based on the repository owner, repository name, proposed branch, and active branch.
// This combination should make the PR name unique.
func GetPullRequestName(repoOwner, repoName, pcProposedBranch, pcActiveBranch string) string {
	return fmt.Sprintf("%s-%s-%s-%s", repoOwner, repoName, pcProposedBranch, pcActiveBranch)
}

// GetChangeTransferPolicyName returns a name for the ChangeTransferPolicy based on the promotion strategy name and environment branch.
func GetChangeTransferPolicyName(promotionStrategyName, environmentBranch string) string {
	return fmt.Sprintf("%s-%s", promotionStrategyName, environmentBranch)
}

// KubeSafeUniqueName returns a DNS-1123 subdomain-safe unique name: lowercase, non-alphanumerics become '-',
// then a rune budget is reserved for "-"+FNV hash (hash of the full sanitized string) under DNS1123 max length.
//
// When trimming and truncation leave no usable stem (for example input that is only spaces or punctuation, so
// sanitization is all hyphens), the stem falls back to "x" so the result is still "x-<hash>" instead of "-<hash>",
// which would violate DNS1123 (names must start and end with an alphanumeric character). The same fallback
// applies if the stem would end up empty after TruncateString + TrimRight.
func KubeSafeUniqueName(ctx context.Context, name string) string {
	s := strings.ToLower(m1.ReplaceAllString(name, "-"))
	h := fnv.New32a()
	if _, err := h.Write([]byte(s)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to write to hash")
	}
	hash := strconv.FormatUint(uint64(h.Sum32()), 16)
	limit := validation.DNS1123SubdomainMaxLength
	if len(hash)+1 > limit {
		return TruncateString(hash, limit)
	}
	budget := limit - len(hash) - 1 // runes for stem before the final "-<hash>"
	stem := strings.Trim(s, "-")
	if stem == "" {
		stem = "x"
	}
	stem = TruncateString(stem, budget)
	stem = strings.TrimRight(stem, "-")
	if stem == "" {
		stem = "x"
	}
	return stem + "-" + hash
}

// KubeSafeLabel Creates a safe label buy truncating from the beginning of 'name' to a max of 63 characters, if the name starts with a hyphen it will be removed.
// We truncate from beginning so that we can keep the unique hash at the end of the name.
func KubeSafeLabel(name string) string {
	if name == "" {
		return ""
	}
	name = m1.ReplaceAllString(name, "-")
	name = TruncateStringFromBeginning(name, 63)
	for len(name) > 0 && name[0] == '-' {
		name = name[1:]
	}
	return name
}

// GetEnvironmentByBranch returns the index and the Environment object for a given branch in the PromotionStrategy.
func GetEnvironmentByBranch(promotionStrategy promoterv1alpha1.PromotionStrategy, branch string) (int, *promoterv1alpha1.Environment) {
	for i, environment := range promotionStrategy.Spec.Environments {
		if environment.Branch == branch {
			return i, &environment
		}
	}
	return -1, nil
}

// UpsertChangeTransferPolicyList adds or updates a list of ChangeTransferPolicies in the slice.
func UpsertChangeTransferPolicyList(slice []promoterv1alpha1.ChangeTransferPolicy, insertList ...[]promoterv1alpha1.ChangeTransferPolicy) []promoterv1alpha1.ChangeTransferPolicy {
	for _, policies := range insertList {
		for _, p := range policies {
			slice = UpsertChangeTransferPolicy(slice, p)
		}
	}
	return slice
}

// UpsertChangeTransferPolicy adds or updates a ChangeTransferPolicy in the slice.
func UpsertChangeTransferPolicy(policies []promoterv1alpha1.ChangeTransferPolicy, policy promoterv1alpha1.ChangeTransferPolicy) []promoterv1alpha1.ChangeTransferPolicy {
	if len(policies) == 0 {
		policies = append(policies, policy)
		return policies
	}
	for index, ele := range policies {
		if ele.Name == policy.Name {
			return slices.Replace(policies, index, index+1, policy)
		}
	}
	return append(policies, policy)
}

// AreCommitStatusesPassing checks if all commit statuses in the provided slice are in the success phase.
func AreCommitStatusesPassing(commitStatuses []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) bool {
	for _, status := range commitStatuses {
		if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
			return false
		}
	}
	return true
}

// StatusConditionUpdater defines the interface for objects that can have their status conditions updated.
// Implementers must also expose SetObservedGeneration so the generic reconciliation helper can stamp
// status.observedGeneration before the SSA status patch. This is the primary mechanism for detecting
// stale status writes: SSA with ForceOwnership performs no optimistic-concurrency check, so a stale
// cached reconcile could otherwise silently overwrite a newer status. Consumers should compare
// status.observedGeneration with metadata.generation.
type StatusConditionUpdater interface {
	client.Object
	GetConditions() *[]metav1.Condition
	SetObservedGeneration(generation int64)
}

// HandleReconciliationResult handles reconciliation results for any object with status conditions.
// It applies the object's status subresource via Server-Side Apply under the provided
// fieldOwner with ForceOwnership. If the full-status apply is rejected (e.g. by an
// OpenAPI schema or CEL validation rule on some status field), a second SSA patch that
// populates only status.conditions is attempted under the same fieldOwner so the
// Ready=False condition describing the failure still reaches the user. A successful
// subsequent reconcile naturally re-owns conditions alongside the rest of the status,
// leaving no managedFields drift.
//
// If result is non-nil and this function sets an error (e.g. status apply failed), it
// clears any Requeue/RequeueAfter in *result so the caller does not return both a
// requeue and an error.
func HandleReconciliationResult(
	ctx context.Context,
	startTime time.Time,
	obj StatusConditionUpdater,
	c client.Client,
	recorder events.EventRecorder,
	fieldOwner string,
	result *reconcile.Result,
	err *error,
) {
	// Recover from any panic and convert it to an error.
	// This function is always called as a defer from the Reconcile function, which means recover() will work correctly here.
	//nolint:revive // False positive: recover() works in a deferred function, and this function is always deferred by callers
	if r := recover(); r != nil {
		logger := log.FromContext(ctx)
		logger.Error(nil, "recovered from panic in reconciliation", "panic", r, "trace", string(debug.Stack()))
		*err = fmt.Errorf("panic in reconciliation: %v", r)
		if result != nil {
			*result = reconcile.Result{}
		}
	}

	logger := log.FromContext(ctx)

	logger.Info(fmt.Sprintf("Reconciling %s End", obj.GetObjectKind().GroupVersionKind().Kind), "duration", time.Since(startTime))
	if obj.GetName() == "" && obj.GetNamespace() == "" {
		// This happens when the Get in the Reconcile function returns "not found." It's expected and safe to skip.
		logger.V(4).Info(obj.GetObjectKind().GroupVersionKind().Kind + " not found, skipping reconciliation")
		return
	}

	conditions := obj.GetConditions() // GetConditions() is guaranteed to be non-nil for our CRDs.
	readyCondition := meta.FindStatusCondition(*conditions, string(promoterConditions.Ready))
	// Only this helper writes Reason=ReconciliationError. Finding one here means it's a stale
	// leftover from a previous failed reconcile; discard it so a successful reconcile can
	// install a fresh success condition below. Without this, SSA would keep re-applying the
	// stale condition forever because the deferred helper reads conditions back from the
	// in-memory object (which still carries whatever was persisted by the previous reconcile).
	if readyCondition != nil && readyCondition.Reason == string(promoterConditions.ReconciliationError) {
		readyCondition = nil
	}
	if readyCondition == nil {
		// If the condition hasn't already been set by the caller, assume success.
		readyCondition = &metav1.Condition{
			Type:               string(promoterConditions.Ready),
			Status:             metav1.ConditionTrue,
			Reason:             string(promoterConditions.ReconciliationSuccess),
			Message:            "Reconciliation successful",
			ObservedGeneration: obj.GetGeneration(),
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
	}

	// Set the Ready condition based on reconciliation result
	if *err == nil {
		// Success case: set Ready condition if not already set
		meta.SetStatusCondition(conditions, *readyCondition)
		eventType := "Normal"
		if readyCondition.Status == metav1.ConditionFalse {
			eventType = "Warning"
		}
		recorder.Eventf(obj, nil, eventType, readyCondition.Reason, "Reconciling", readyCondition.Message)
	} else {
		// Error case: set Ready condition to False. Conflict errors from Update calls
		// elsewhere in the reconcile flow are expected and transient, so don't spam events
		// for those; the retry will emit the event if the condition persists.
		if !k8serrors.IsConflict(*err) {
			recorder.Eventf(obj, nil, "Warning", string(promoterConditions.ReconciliationError), "Reconciling", "Reconciliation failed: %v", *err)
		}
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:               string(promoterConditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             string(promoterConditions.ReconciliationError),
			Message:            fmt.Sprintf("Reconciliation failed: %s", *err),
			ObservedGeneration: obj.GetGeneration(),
		})
	}

	// Stamp status.observedGeneration before building the apply configuration so the SSA
	// patch records which spec generation produced this status. SSA with ForceOwnership
	// has no optimistic-concurrency guard, so observedGeneration is the only signal a
	// consumer can use to tell whether a status reflects the current spec.
	obj.SetObservedGeneration(obj.GetGeneration())

	// Build the full status apply configuration and SSA-patch the status subresource.
	// This function should be the only place status is written for reconciled resources.
	fullCfg, buildErr := statusApplyConfig(obj, false)
	if buildErr != nil {
		if *err == nil {
			*err = fmt.Errorf("failed to build status apply configuration: %w", buildErr)
		} else {
			//nolint:errorlint // The initial error is intentionally quoted instead of wrapped for clarity.
			*err = fmt.Errorf("failed to build status apply configuration with error %q: %w", *err, buildErr)
		}
		if result != nil {
			*result = reconcile.Result{}
		}
		return
	}

	patchErr := c.Status().Patch(ctx, obj, ApplyPatch{ApplyConfig: fullCfg},
		client.FieldOwner(fieldOwner), client.ForceOwnership)
	if patchErr == nil {
		return
	}

	// Object was deleted concurrently; nothing to write.
	if k8serrors.IsNotFound(patchErr) {
		logger.V(4).Info("status apply skipped, object no longer exists", "error", patchErr)
		return
	}

	// Full status apply failed. Try a fallback that applies only status.conditions so the
	// Ready condition explaining the failure still lands on the object. SSA on /status is
	// atomic, so a validation rejection (OpenAPI or CEL) on any other status field loses
	// the Ready condition we just set in memory.
	//
	// The fallback uses a DISTINCT FieldOwner so it only claims ownership of
	// status.conditions. Under SSA, when a manager re-applies and drops fields it
	// previously owned, those fields are deleted from the live object unless another
	// manager owns them. If we used the same FieldOwner here, this conditions-only patch
	// would wipe status.proposed, status.active, status.history, etc. — a severe UX
	// regression. Using a separate owner leaves those fields untouched under the main
	// owner until the next successful full apply reclaims conditions via ForceOwnership.
	//
	// Also note: this patch deliberately does NOT include status.observedGeneration.
	// Keeping the stored top-level observedGeneration pinned to the last successful
	// reconcile is the canonical "status is stale" signal; the Ready condition's own
	// ObservedGeneration field records the generation that was attempted.
	fallbackFieldOwner := fieldOwner + "-fallback"
	logger.V(4).Info("full status apply failed, attempting conditions-only apply",
		"error", patchErr, "fallbackFieldOwner", fallbackFieldOwner)

	// Rewrite the in-memory Ready condition to describe the apply failure. If the
	// reconcile already produced an error, we keep that as the primary cause; otherwise
	// the status apply failure is the cause.
	fallbackCondition := metav1.Condition{
		Type:               string(promoterConditions.Ready),
		Status:             metav1.ConditionFalse,
		Reason:             string(promoterConditions.ReconciliationError),
		ObservedGeneration: obj.GetGeneration(),
	}
	if *err != nil {
		fallbackCondition.Message = fmt.Sprintf("Reconciliation failed: %s", *err)
	} else {
		fallbackCondition.Message = fmt.Sprintf("Reconciliation succeeded but failed to apply status: %s", patchErr)
	}
	meta.SetStatusCondition(conditions, fallbackCondition)

	condCfg, buildErr := statusApplyConfig(obj, true)
	if buildErr != nil {
		if *err == nil {
			*err = fmt.Errorf("failed to apply status (%w), and building conditions-only apply configuration also failed: %w", patchErr, buildErr)
		} else {
			//nolint:errorlint // The initial error is intentionally quoted instead of wrapped for clarity.
			*err = fmt.Errorf("failed to apply status with error condition regarding error %q (%w), and building conditions-only apply configuration also failed: %w", *err, patchErr, buildErr)
		}
		if result != nil {
			*result = reconcile.Result{}
		}
		return
	}

	//nolint:forcetypeassert // Type assertion is guaranteed to succeed for all CRDs in this codebase.
	fallbackObj := obj.DeepCopyObject().(StatusConditionUpdater)
	fallbackErr := c.Status().Patch(ctx, fallbackObj, ApplyPatch{ApplyConfig: condCfg},
		client.FieldOwner(fallbackFieldOwner), client.ForceOwnership)
	if fallbackErr != nil {
		if k8serrors.IsNotFound(fallbackErr) {
			logger.V(4).Info("conditions-only status apply skipped, object no longer exists", "error", fallbackErr)
			return
		}
		// Fallback also failed, report both errors
		if *err == nil {
			*err = fmt.Errorf("failed to apply status (original error: %w), and applying only the Ready condition also failed: %w", patchErr, fallbackErr)
		} else {
			//nolint:errorlint // The initial error is intentionally quoted instead of wrapped for clarity.
			*err = fmt.Errorf("failed to apply status with error condition regarding error %q (original status apply error: %w), and applying only the Ready condition also failed: %w", *err, patchErr, fallbackErr)
		}
		if result != nil {
			*result = reconcile.Result{}
		}
		return
	}

	// Fallback succeeded, but report the original status apply failure
	logger.Info("Successfully applied only the Ready condition after full status apply failed")
	if *err == nil {
		*err = fmt.Errorf("failed to apply full status (but applying only the Ready condition succeeded): %w", patchErr)
	} else {
		//nolint:errorlint // The initial error is intentionally quoted instead of wrapped for clarity.
		*err = fmt.Errorf("failed to apply status with error condition regarding error %q (but applying only the Ready condition succeeded): %w", *err, patchErr)
	}
	if result != nil {
		*result = reconcile.Result{}
	}
}

// InheritNotReadyConditionFromObjects sets the Ready condition of the parent to False if any of the child objects are not ready.
// This will override any existing Ready condition on the parent.
//
// All child objects must be non-nil.
func InheritNotReadyConditionFromObjects[T StatusConditionUpdater](parent StatusConditionUpdater, notReadyReason promoterConditions.CommonReason, childObjs ...T) {
	// This function does not nil-check the result of GetConditions() for either the parent or child objects. This is
	// because all our CRDs have non-nilable status.conditions fields. The return value of GetConditions() is a pointer
	// only to facilitate mutations on the referenced slice.
	// If we ever make status.conditions nilable, we will need to add nil checks here.

	condition := metav1.Condition{
		Type:               string(promoterConditions.Ready),
		Status:             metav1.ConditionUnknown,
		Reason:             string(notReadyReason),
		ObservedGeneration: parent.GetGeneration(),
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	// We always take the first non-ready condition we find. So sort the child objects by name to get more consistent
	// results. We sort a copy of the slice to avoid mutating the caller's slice.
	sortedChildObjs := slices.Clone(childObjs)
	slices.SortFunc(sortedChildObjs, func(a, b T) int {
		return strings.Compare(a.GetName(), b.GetName())
	})

	for _, childObj := range sortedChildObjs {
		childObjKind := childObj.GetObjectKind().GroupVersionKind().Kind
		childObjReady := meta.FindStatusCondition(*childObj.GetConditions(), string(promoterConditions.Ready))

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
			condition.Status = childObjReady.Status // Could be False or Unknown, inherit the child's status.
			meta.SetStatusCondition(parent.GetConditions(), condition)
			return
		}
	}
}
