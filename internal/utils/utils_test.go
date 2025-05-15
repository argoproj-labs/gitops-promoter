package utils_test

import (
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	namespace           = "test-namespace"
	controllerNamespace = "controller-namespace"
)

func TestGetScmProviderFromGitRepository(t *testing.T) {
	t.Parallel()
	namespacedScmProvider := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "namespaced-scm-provider",
			Namespace: namespace,
		},
	}

	clusterScmProvider := &promoterv1alpha1.ClusterScmProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-scm-provider",
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespacedScmProvider, clusterScmProvider).Build()

	// We only need an object that implements the Object interface, it doesn't have to be a ChangeTransferPolicy
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}

	t.Run("can get namespaced ScmProvider", func(t *testing.T) {
		t.Parallel()
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "ScmProvider",
					Name: "namespaced-scm-provider",
				},
			},
		}

		got, err := utils.GetScmProviderFromGitRepository(t.Context(), client, gitRepository, ctp)
		require.NoError(t, err)
		assert.Equal(t, namespacedScmProvider, got)
	})

	t.Run("can get ClusterSCMProvider", func(t *testing.T) {
		t.Parallel()
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "ClusterScmProvider",
					Name: "cluster-scm-provider",
				},
			},
		}

		got, err := utils.GetScmProviderFromGitRepository(t.Context(), client, gitRepository, ctp)
		require.NoError(t, err)
		assert.Equal(t, clusterScmProvider, got)
	})

	t.Run("errors for unsupported SCM provider kind", func(t *testing.T) {
		t.Parallel()
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "UnsupportedScmProvider",
					Name: "unsuppoerted-scm-provider",
				},
			},
		}

		_, err := utils.GetScmProviderFromGitRepository(t.Context(), client, gitRepository, ctp)
		require.ErrorContains(t, err, "unsupported ScmProvider kind")
	})
}

func TestGetScmProviderAndSecretFromRepositoryReference(t *testing.T) {
	t.Parallel()

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scm-provider-secret",
			Namespace: namespace,
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))
	require.NoError(t, v1.AddToScheme(scheme))

	// We only need an object that implements the Object interface, it doesn't have to be a ChangeTransferPolicy
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}
	repositoryRef := promoterv1alpha1.ObjectReference{Name: "test-repository"}

	t.Run("ScmProvider use secret in the same namespace", func(t *testing.T) {
		t.Parallel()
		scmProvider := &promoterv1alpha1.ScmProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scm-provider",
				Namespace: namespace,
			},
			Spec: promoterv1alpha1.ScmProviderSpec{
				SecretRef: &v1.LocalObjectReference{
					Name: "scm-provider-secret",
				},
			},
		}
		gitRepository := &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repositoryRef.Name,
				Namespace: namespace,
			},
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: promoterv1alpha1.ScmProviderKind,
					Name: scmProvider.Name,
				},
			},
		}

		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepository, scmProvider, secret).Build()

		gotScmProvider, gotSecret, err := utils.GetScmProviderAndSecretFromRepositoryReference(t.Context(), client, controllerNamespace, repositoryRef, ctp)
		require.NoError(t, err)
		assert.Equal(t, scmProvider, gotScmProvider)
		assert.Equal(t, secret, gotSecret)
	})

	t.Run("ClusterScmProvider use secrets from other controller namespace", func(t *testing.T) {
		t.Parallel()
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scm-provider-secret",
				Namespace: controllerNamespace,
			},
		}
		scmProvider := &promoterv1alpha1.ClusterScmProvider{
			TypeMeta: metav1.TypeMeta{
				Kind: promoterv1alpha1.ClusterScmProviderKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "scm-provider",
			},
			Spec: promoterv1alpha1.ScmProviderSpec{
				SecretRef: &v1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		}
		gitRepository := &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repositoryRef.Name,
				Namespace: namespace,
			},
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: promoterv1alpha1.ClusterScmProviderKind,
					Name: scmProvider.Name,
				},
			},
		}

		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepository, scmProvider, secret).Build()

		gotScmProvider, gotSecret, err := utils.GetScmProviderAndSecretFromRepositoryReference(t.Context(), client, controllerNamespace, repositoryRef, ctp)
		require.NoError(t, err)
		assert.Equal(t, scmProvider, gotScmProvider)
		assert.Equal(t, secret, gotSecret)
	})
}
