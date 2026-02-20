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
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// GetScmProviderAndSecretFromRepositoryReference retrieves the ScmProvider and its associated Secret from a GitRepository reference.
func GetScmProviderAndSecretFromRepositoryReference(ctx context.Context, k8sClient client.Client, controllerNamespace string, repositoryRef promoterv1alpha1.ObjectReference, obj metav1.Object) (promoterv1alpha1.GenericScmProvider, *v1.Secret, error) {
	logger := log.FromContext(ctx)
	gitRepo, err := GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

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
// it will return "efg".
func TruncateStringFromBeginning(str string, length int) string {
	if length <= 0 {
		return ""
	}
	if len(str) <= length {
		return str
	}
	return str[len(str)-length:]
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

// KubeSafeUniqueName Creates a safe name by replacing all non-alphanumeric characters with a hyphen and truncating to a max of 255 characters, then appending a hash of the name.
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

// KubeSafeLabel Creates a safe label buy truncating from the beginning of 'name' to a max of 63 characters, if the name starts with a hyphen it will be removed.
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

// StatusConditionUpdater defines the interface for objects that can have their status conditions updated
type StatusConditionUpdater interface {
	client.Object
	GetConditions() *[]metav1.Condition
}

// HandleReconciliationResult handles reconciliation results for any object with status conditions.
func HandleReconciliationResult(
	ctx context.Context,
	startTime time.Time,
	obj StatusConditionUpdater,
	client client.Client,
	recorder events.EventRecorder,
	err *error,
) {
	// Recover from any panic and convert it to an error.
	// This function is always called as a defer from the Reconcile function, which means recover() will work correctly here.
	//nolint:revive // False positive: recover() works in a deferred function, and this function is always deferred by callers
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

	// If the deletion timestamp is set on the object, bail out early.
	if !obj.GetDeletionTimestamp().IsZero() {
		logger.V(4).Info("resource deleted, skipping handling of the reconciliation result")
		return
	}

	conditions := obj.GetConditions() // GetConditions() is guaranteed to be non-nil for our CRDs.
	readyCondition := meta.FindStatusCondition(*conditions, string(promoterConditions.Ready))
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
		// Error case: set Ready condition to False
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

	// Single status update. This is the only place Status().Update() is called for reconciled resources.
	if updateErr := client.Status().Update(ctx, obj); updateErr != nil {
		if *err == nil {
			*err = fmt.Errorf("failed to update status: %w", updateErr)
		} else {
			//nolint:errorlint // The initial error is intentionally quoted instead of wrapped for clarity.
			*err = fmt.Errorf("failed to update status with error condition with error %q: %w", *err, updateErr)
		}
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
