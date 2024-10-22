package utils

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func GetScmProviderFromGitRepository(ctx context.Context, k8sClient client.Client, repositoryRef *promoterv1alpha1.GitRepository, obj metav1.Object) (*promoterv1alpha1.ScmProvider, error) {
	logger := log.FromContext(ctx)

	var scmProvider promoterv1alpha1.ScmProvider
	namespace := obj.GetNamespace()

	objectKey := client.ObjectKey{
		Namespace: namespace,
		Name:      repositoryRef.Spec.ScmProviderRef.Name,
	}
	err := k8sClient.Get(ctx, objectKey, &scmProvider, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ScmProvider not found", "namespace", namespace, "name", objectKey.Name)
			return nil, err
		}

		logger.Error(err, "failed to get ScmProvider", "namespace", namespace, "name", objectKey.Name)
		return nil, err
	}

	return &scmProvider, nil
}

// GetGitRepositorytFromObjectKey returns the GitRepository object from the repository reference
func GetGitRepositorytFromObjectKey(ctx context.Context, k8sClient client.Client, objectKey client.ObjectKey) (*promoterv1alpha1.GitRepository, error) {

	var gitRepo promoterv1alpha1.GitRepository
	err := k8sClient.Get(ctx, objectKey, &gitRepo)
	if err != nil {
		return nil, err
	}

	return &gitRepo, nil
}

func GetScmProviderAndSecretFromRepositoryReference(ctx context.Context, k8sClient client.Client, repositoryRef promoterv1alpha1.ObjectReference, obj metav1.Object) (*promoterv1alpha1.ScmProvider, *v1.Secret, error) {
	logger := log.FromContext(ctx)
	gitRepo, err := GetGitRepositorytFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	scmProvider, err := GetScmProviderFromGitRepository(ctx, k8sClient, gitRepo, obj)
	if err != nil {
		return nil, nil, err
	}

	var secret v1.Secret
	objectKey := client.ObjectKey{
		Namespace: scmProvider.Namespace,
		Name:      scmProvider.Spec.SecretRef.Name,
	}
	err = k8sClient.Get(ctx, objectKey, &secret)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Secret from ScmProvider not found", "namespace", scmProvider.Namespace, "name", objectKey.Name)
			return nil, nil, err
		}

		logger.Error(err, "failed to get Secret from ScmProvider", "namespace", scmProvider.Namespace, "name", objectKey.Name)
		return nil, nil, err
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

func GetPullRequestName(ctx context.Context, repoOwner, repoName, pcProposedBranch, pcActiveBranch string) string {
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
	hash := fmt.Sprintf("%x", h.Sum32())

	if name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	name = name + "-" + hash
	return TruncateString(name, 255-len(hash)-1)
}

// KubeSafeLabel Creates a safe label buy truncating from the beginning of 'name' to a max of 63 characters, if the name starts with a hyphen it will be removed.
// We truncate from beginning so that we can keep the unique hash at the end of the name.
func KubeSafeLabel(ctx context.Context, name string) string {
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

func GetEnvironmentsFromStatusInOrder(promotionStrategy promoterv1alpha1.PromotionStrategy) []promoterv1alpha1.EnvironmentStatus {
	environments := []promoterv1alpha1.EnvironmentStatus{}
	for _, specEnvironment := range promotionStrategy.Spec.Environments {
		for _, statusEvents := range promotionStrategy.Status.Environments {
			if specEnvironment.Branch == statusEvents.Branch {
				environments = append(environments, statusEvents)
			}
		}
	}
	return environments
}

func GetPreviousEnvironmentStatusByBranch(promotionStrategy promoterv1alpha1.PromotionStrategy, currentBranch string) (int, *promoterv1alpha1.EnvironmentStatus) {
	environments := GetEnvironmentsFromStatusInOrder(promotionStrategy)
	for i, environment := range environments {
		if environment.Branch == currentBranch {
			if i-1 >= 0 && len(environments) > 0 {
				return i + 1, &environments[i-1]
			}
		}
	}
	return -1, nil
}

func GetEnvironmentStatusByBranch(promotionStrategy promoterv1alpha1.PromotionStrategy, branch string) (int, *promoterv1alpha1.EnvironmentStatus) {
	environments := GetEnvironmentsFromStatusInOrder(promotionStrategy)
	for i, environment := range environments {
		if environment.Branch == branch {
			return i, &environment
		}
	}
	return -1, nil
}

func GetEnvironmentByBranch(promotionStrategy promoterv1alpha1.PromotionStrategy, branch string) (int, *promoterv1alpha1.Environment) {
	for i, environment := range promotionStrategy.Spec.Environments {
		if environment.Branch == branch {
			return i, &environment
		}
	}
	return -1, nil
}

func UpsertEnvironmentStatus(slice []promoterv1alpha1.EnvironmentStatus, i promoterv1alpha1.EnvironmentStatus) []promoterv1alpha1.EnvironmentStatus {
	if len(slice) == 0 {
		slice = append(slice, i)
		return slice
	}
	for index, ele := range slice {
		if ele.Branch == i.Branch {
			return slices.Replace(slice, index, index+1, i)
		}
	}
	return append(slice, i)
}
