package utils

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/record"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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

// Truncate from front of string
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

func GetPullRequestName(repoOwner, repoName, pcProposedBranch, pcActiveBranch string) string {
	return fmt.Sprintf("%s-%s-%s-%s", repoOwner, repoName, pcProposedBranch, pcActiveBranch)
}

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

func GetEnvironmentByBranch(promotionStrategy promoterv1alpha1.PromotionStrategy, branch string) (int, *promoterv1alpha1.Environment) {
	for i, environment := range promotionStrategy.Spec.Environments {
		if environment.Branch == branch {
			return i, &environment
		}
	}
	return -1, nil
}

func UpsertChangeTransferPolicyList(slice []promoterv1alpha1.ChangeTransferPolicy, insertList ...[]promoterv1alpha1.ChangeTransferPolicy) []promoterv1alpha1.ChangeTransferPolicy {
	for _, policies := range insertList {
		for _, p := range policies {
			slice = UpsertChangeTransferPolicy(slice, p)
		}
	}
	return slice
}

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
	recorder record.EventRecorder,
	err *error,
) {
	logger := log.FromContext(ctx)

	logger.Info(fmt.Sprintf("Reconciling %s End", obj.GetObjectKind().GroupVersionKind().Kind), "duration", time.Since(startTime))
	if obj.GetName() == "" && obj.GetNamespace() == "" {
		// This happens when the Get in the Reconcile function returns "not found." It's expected and safe to skip.
		logger.V(4).Info(obj.GetObjectKind().GroupVersionKind().Kind + " not found, skipping reconciliation")
		return
	}

	conditions := obj.GetConditions()
	if conditions == nil {
		conditions = &[]metav1.Condition{}
	}

	if *err == nil {
		recorder.Eventf(obj, "Normal", "ReconcileSuccess", "Reconciliation successful")
		if updateErr := updateReadyCondition(ctx, obj, client, conditions, metav1.ConditionTrue, string(promoterConditions.ReconciliationSuccess), "Reconciliation succeeded"); updateErr != nil {
			*err = fmt.Errorf("failed to update status with success condition: %w", updateErr)
		}
		return
	}

	if !k8serrors.IsConflict(*err) {
		recorder.Eventf(obj, "Warning", "ReconcileError", "Reconciliation failed: %v", *err)
	}
	if updateErr := updateReadyCondition(ctx, obj, client, conditions, metav1.ConditionFalse, string(promoterConditions.ReconciliationError), fmt.Sprintf("Reconciliation failed: %s", *err)); updateErr != nil {
		*err = fmt.Errorf("failed to update status with error condition: %w", updateErr)
	}
}

func updateReadyCondition(ctx context.Context, obj StatusConditionUpdater, client client.Client, conditions *[]metav1.Condition, status metav1.ConditionStatus, reason, message string) error {
	condition := metav1.Condition{
		Type:               string(promoterConditions.Ready),
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
