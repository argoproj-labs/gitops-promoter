/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	_ "embed"

	v1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/CommitStatus.yaml
var testCommitStatusYAML string

var _ = Describe("CommitStatus Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the CommitStatus resource", func() {
			err := unmarshalYamlStrict(testCommitStatusYAML, &promoterv1alpha1.CommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When validating the CommitStatus spec", func() {
		ctx := context.Background()

		It("should reject a CommitStatus with an empty sha", func() {
			invalidCommitStatus := &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-commit-status",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					Phase: promoterv1alpha1.CommitPhasePending,
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "test-repo",
					},
					Sha:         "",
					Name:        "test-commit-status",
					Description: "Test commit status",
				},
			}
			err := k8sClient.Create(ctx, invalidCommitStatus)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("should be at least 1 chars long"))
		})

		It("should reject a CommitStatus with a sha that is too long", func() {
			invalidCommitStatus := &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-commit-status-long",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					Phase: promoterv1alpha1.CommitPhasePending,
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "test-repo",
					},
					Sha:         "abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678901",
					Name:        "test-commit-status",
					Description: "Test commit status",
				},
			}
			err := k8sClient.Create(ctx, invalidCommitStatus)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("may not be longer than 64"))
		})

		It("should reject a CommitStatus with a sha that contains invalid characters", func() {
			invalidCommitStatus := &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-commit-status-chars",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					Phase: promoterv1alpha1.CommitPhasePending,
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "test-repo",
					},
					Sha:         "xyz123",
					Name:        "test-commit-status",
					Description: "Test commit status",
				},
			}
			err := k8sClient.Create(ctx, invalidCommitStatus)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sha"))
		})

		It("should accept a CommitStatus with a valid sha", func() {
			validCommitStatus := &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-commit-status",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					Phase: promoterv1alpha1.CommitPhasePending,
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "test-repo",
					},
					Sha:         "abcdef1234567890",
					Name:        "test-commit-status",
					Description: "Test commit status",
				},
			}
			err := k8sClient.Create(ctx, validCommitStatus)
			Expect(err).ToNot(HaveOccurred())
			// Clean up
			_ = k8sClient.Delete(ctx, validCommitStatus)
		})
	})

	var gitRepo *promoterv1alpha1.GitRepository
	var scmProvider *promoterv1alpha1.ScmProvider
	var scmSecret *v1.Secret
	var commitStatus *promoterv1alpha1.CommitStatus

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-commit-status"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind CommitStatus")

			scmSecret = &v1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Data: nil,
			}

			scmProvider = &promoterv1alpha1.ScmProvider{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.ScmProviderSpec{
					SecretRef: &v1.LocalObjectReference{Name: resourceName},
					Fake:      &promoterv1alpha1.Fake{},
				},
				Status: promoterv1alpha1.ScmProviderStatus{},
			}

			gitRepo = &promoterv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.GitRepositorySpec{
					Fake: &promoterv1alpha1.FakeRepo{
						Owner: typeNamespacedName.Name,
						Name:  typeNamespacedName.Name,
					},
					ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
						Kind: promoterv1alpha1.ScmProviderKind,
						Name: typeNamespacedName.Name,
					},
				},
			}

			commitStatus = &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					Phase: promoterv1alpha1.CommitPhasePending,
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: typeNamespacedName.Name,
					},
					Sha:         "abcdef1234567890abcdef1234567890abcdef12",
					Name:        "test-commit-status",
					Description: "Test commit status",
				},
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			By("Cleanup the specific resource instance CommitStatus")
			_ = k8sClient.Delete(ctx, commitStatus)
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())

			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})

	Context("When creating a CommitStatus with URL validation", func() {
		ctx := context.Background()

		Context("When validating https URL", func() {
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus

			BeforeEach(func() {
				scmSecret, scmProvider, gitRepo, commitStatus = commitStatusResources(ctx, "test-valid-https-url")
				commitStatus.Spec.Url = "https://example.com/status"

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should accept a valid https URL", func() {
				By("Creating a CommitStatus with a valid https URL")
				// Resource creation is in BeforeEach, validation happens automatically
			})
		})

		Context("When validating http URL", func() {
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus

			BeforeEach(func() {
				scmSecret, scmProvider, gitRepo, commitStatus = commitStatusResources(ctx, "test-valid-http-url")
				commitStatus.Spec.Url = "http://example.com/status"

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should accept a valid http URL", func() {
				By("Creating a CommitStatus with a valid http URL")
				// Resource creation is in BeforeEach, validation happens automatically
			})
		})

		Context("When validating empty URL", func() {
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus

			BeforeEach(func() {
				scmSecret, scmProvider, gitRepo, commitStatus = commitStatusResources(ctx, "test-empty-url")
				// URL is already empty by default, no need to set

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should accept an empty URL", func() {
				By("Creating a CommitStatus with an empty URL")
				// Resource creation is in BeforeEach, validation happens automatically
			})
		})

		Context("When validating invalid URL", func() {
			It("should reject an invalid URL", func() {
				By("Attempting to create a CommitStatus with an invalid URL")

				scmSecret, scmProvider, gitRepo, commitStatus := commitStatusResources(ctx, "test-invalid-url")
				commitStatus.Spec.Url = "not-a-valid-url"

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

				By("Verifying the create operation fails due to CEL validation")
				err := k8sClient.Create(ctx, commitStatus)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must be a valid URL"))

				// Cleanup
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})
		})

		Context("When validating URL scheme", func() {
			It("should reject a URL with an invalid scheme", func() {
				By("Attempting to create a CommitStatus with ftp:// scheme")

				scmSecret, scmProvider, gitRepo, commitStatus := commitStatusResources(ctx, "test-invalid-scheme")
				commitStatus.Spec.Url = "ftp://example.com/status"

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

				By("Verifying the create operation fails due to pattern validation")
				err := k8sClient.Create(ctx, commitStatus)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Or(
					ContainSubstring("must be a valid URL"),
					ContainSubstring("in body should match"),
				))

				// Cleanup
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})
		})
	})
})

// commitStatusResources creates all the resources needed for a CommitStatus test
// Returns: name (with random suffix), scmSecret, scmProvider, gitRepo, commitStatus
// Note: URL is set to empty by default and can be customized in tests
func commitStatusResources(ctx context.Context, name string) (*v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))

	scmSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &v1.LocalObjectReference{Name: name},
			Fake:      &promoterv1alpha1.Fake{},
		},
	}

	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Fake: &promoterv1alpha1.FakeRepo{
				Owner: "test-owner",
				Name:  "test-repo",
			},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: name,
			},
		},
	}

	commitStatus := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:   "abc123",                            // Minimal valid SHA, can be customized by tests
			Name:  "test-status",                       // Can be customized by tests
			Phase: promoterv1alpha1.CommitPhasePending, // Reasonable default, can be customized
			// Description and Url are optional and left empty for tests to set
		},
	}

	return scmSecret, scmProvider, gitRepo, commitStatus
}
