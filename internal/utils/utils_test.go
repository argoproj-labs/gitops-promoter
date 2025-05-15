package utils_test

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	namespace           = "test-namespace"
	controllerNamespace = "controller-namespace"
)

var _ = Describe("getting an ScmProvider from a GitRepository", func() {
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
	Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespacedScmProvider, clusterScmProvider).Build()

	// We only need an object that implements the Object interface, it doesn't have to be a ChangeTransferPolicy
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}

	It("can get namespaced ScmProvider", func(ctx context.Context) {
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "ScmProvider",
					Name: "namespaced-scm-provider",
				},
			},
		}

		got, err := utils.GetScmProviderFromGitRepository(ctx, client, gitRepository, ctp)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(namespacedScmProvider))
	})

	It("can get ClusterScmProvider", func(ctx context.Context) {
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "ClusterScmProvider",
					Name: "cluster-scm-provider",
				},
			},
		}

		got, err := utils.GetScmProviderFromGitRepository(ctx, client, gitRepository, ctp)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(clusterScmProvider))
	})

	It("errors for unsupported SCM provider kind", func(ctx context.Context) {
		gitRepository := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
					Kind: "UnsupportedScmProvider",
					Name: "unsuppoerted-scm-provider",
				},
			},
		}

		_, err := utils.GetScmProviderFromGitRepository(ctx, client, gitRepository, ctp)
		Expect(err).To(MatchError(ContainSubstring("unsupported ScmProvider kind")))
	})
})

var _ = Describe("getting an ScmProvider and Secret from a repositoryRef", func() {
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scm-provider-secret",
			Namespace: namespace,
		},
	}

	scheme := runtime.NewScheme()
	Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1.AddToScheme(scheme)).To(Succeed())

	// We only need an object that implements the Object interface, it doesn't have to be a ChangeTransferPolicy
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}
	repositoryRef := promoterv1alpha1.ObjectReference{Name: "test-repository"}

	It("uses secrets in the same namespace for ScmProvider", func(ctx context.Context) {
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

		gotScmProvider, gotSecret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, client, controllerNamespace, repositoryRef, ctp)
		Expect(err).ToNot(HaveOccurred())
		Expect(gotScmProvider).To(Equal(scmProvider))
		Expect(gotSecret).To(Equal(secret))
	})

	It("uses secrets in the controller namespace for ClusterScmProvider", func(ctx context.Context) {
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

		gotScmProvider, gotSecret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, client, controllerNamespace, repositoryRef, ctp)
		Expect(err).ToNot(HaveOccurred())
		Expect(gotScmProvider).To(Equal(scmProvider))
		Expect(gotSecret).To(Equal(secret))
	})
})
