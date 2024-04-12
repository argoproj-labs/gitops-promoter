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

func GetScmProviderAndSecret(ctx context.Context, k8sClient client.Client, repositoryRef promoterv1alpha1.RepositoryRef, obj metav1.Object) (*promoterv1alpha1.ScmProvider, *v1.Secret, error) {
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
			return nil, nil, err
		}

		logger.Error(err, "failed to get ScmProvider", "namespace", namespace, "name", objectKey.Name)
		return nil, nil, err
	}

	var secret v1.Secret
	objectKey = client.ObjectKey{
		Namespace: scmProvider.Namespace,
		Name:      scmProvider.Spec.SecretRef.Name,
	}
	err = k8sClient.Get(ctx, objectKey, &secret)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Secret from ScmProvider not found", "namespace", namespace, "name", objectKey.Name)
			return nil, nil, err
		}

		logger.Error(err, "failed to get Secret from ScmProvider", "namespace", namespace, "name", objectKey.Name)
		return nil, nil, err
	}

	return &scmProvider, &secret, nil
}
