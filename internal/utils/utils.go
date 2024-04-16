package utils

import (
	"context"

	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func GetScmProviderFromRepositoryReference(ctx context.Context, k8sClient client.Client, repositoryRef promoterv1alpha1.RepositoryRef, obj metav1.Object) (*promoterv1alpha1.ScmProvider, error) {
	logger := log.FromContext(ctx)

	var scmProvider promoterv1alpha1.ScmProvider
	var namespace string
	if repositoryRef.ScmProviderRef.Namespace != "" {
		namespace = repositoryRef.ScmProviderRef.Namespace
	} else {
		namespace = obj.GetNamespace()
	}
	objectKey := client.ObjectKey{
		Namespace: namespace,
		Name:      repositoryRef.ScmProviderRef.Name,
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

func GetScmProviderAndSecretFromRepositoryReference(ctx context.Context, k8sClient client.Client, repositoryRef promoterv1alpha1.RepositoryRef, obj metav1.Object) (*promoterv1alpha1.ScmProvider, *v1.Secret, error) {
	logger := log.FromContext(ctx)

	scmProvider, err := GetScmProviderFromRepositoryReference(ctx, k8sClient, repositoryRef, obj)
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
