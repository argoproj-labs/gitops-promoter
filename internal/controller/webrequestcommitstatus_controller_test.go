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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("WebRequestCommitStatus Controller", func() {
	Context("When HTTP request returns success", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server

		BeforeEach(func() {
			By("Creating a mock HTTP server that returns approved status")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"status":   "approved",
				})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-success", "default")

			// Configure ProposedCommitStatuses to check for external-approval key
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "external-approval"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled with initial state")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus resource")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "external-approval",
					Description: "External approval check",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL,
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200 && Response.Body.approved == true`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report success status when HTTP response passes expression", func() {
			By("Waiting for WebRequestCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for dev environment
				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Validate status fields are populated
				g.Expect(wrcs.Status.Environments[0].ProposedHydratedSha).ToNot(BeEmpty())
				g.Expect(wrcs.Status.Environments[0].ReportedSha).ToNot(BeEmpty())
				g.Expect(wrcs.Status.Environments[0].LastSuccessfulSha).To(Equal(wrcs.Status.Environments[0].ReportedSha))
				g.Expect(wrcs.Status.Environments[0].Response.StatusCode).ToNot(BeNil())
				g.Expect(*wrcs.Status.Environments[0].Response.StatusCode).To(Equal(200))
				g.Expect(wrcs.Status.Environments[0].ExpressionResult).ToNot(BeNil())
				g.Expect(*wrcs.Status.Environments[0].ExpressionResult).To(BeTrue())

				// Verify CommitStatus was created with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testBranchDevelopment+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When HTTP request returns failure (expression false)", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server

		BeforeEach(func() {
			By("Creating a mock HTTP server that returns not approved status")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": false,
					"status":   "pending",
				})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-pending", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "external-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus resource")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "external-check",
					Description: "External check",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL,
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200 && Response.Body.approved == true`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report pending status when expression evaluates to false", func() {
			By("Waiting for WebRequestCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhasePending))

				// Expression result should be false
				g.Expect(wrcs.Status.Environments[0].ExpressionResult).ToNot(BeNil())
				g.Expect(*wrcs.Status.Environments[0].ExpressionResult).To(BeFalse())

				// Verify CommitStatus was created with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testBranchDevelopment+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When using template variables in URL", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server
		var receivedPaths []string

		BeforeEach(func() {
			receivedPaths = []string{}
			By("Creating a mock HTTP server that captures the SHA from the URL")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Extract SHA from URL path and store it
				receivedPaths = append(receivedPaths, r.URL.Path[1:]) // Remove leading /
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-template", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "template-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus with template URL")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "template-check",
					Description: "Template check",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL + "/{{ .ProposedHydratedSha }}",
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should render template variables in URL correctly", func() {
			By("Waiting for WebRequestCommitStatus to make the HTTP request")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Verify the SHA was correctly templated in the URL by checking that we received paths
				// containing the expected SHA
				g.Expect(len(receivedPaths)).To(BeNumerically(">", 0), "Should have received at least one request")

				// Find the path that matches the first environment's SHA
				expectedSha := wrcs.Status.Environments[0].ProposedHydratedSha
				found := false
				for _, path := range receivedPaths {
					if path == expectedSha {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "Expected to find SHA %s in received paths %v", expectedSha, receivedPaths)
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When using bearer token authentication", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var authSecret *v1.Secret
		var testServer *httptest.Server
		var receivedAuthHeader string

		BeforeEach(func() {
			By("Creating a mock HTTP server that validates bearer token")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedAuthHeader = r.Header.Get("Authorization")
				if receivedAuthHeader == "Bearer test-token-123" {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
				} else {
					w.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
				}
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-auth", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "auth-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			// Create auth secret
			authSecret = &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-auth",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Create(ctx, authSecret)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus with bearer auth")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "auth-check",
					Description: "Auth check",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL,
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HttpAuthentication{
							Bearer: &promoterv1alpha1.BearerAuth{
								SecretRef: promoterv1alpha1.BearerAuthSecretRef{
									Name: name + "-auth",
									Key:  "token",
								},
							},
						},
					},
					Expression: `Response.StatusCode == 200 && Response.Body.approved == true`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, authSecret)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should include bearer token in request header", func() {
			By("Waiting for WebRequestCommitStatus to make authenticated request")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Verify the auth header was sent correctly
				g.Expect(receivedAuthHeader).To(Equal("Bearer test-token-123"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When expression compilation fails", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server

		BeforeEach(func() {
			By("Creating a mock HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-expr-fail", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "expr-fail"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus with invalid expression")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "expr-fail",
					Description: "Expression failure test",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL,
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `invalid syntax @#$%`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report failure status when expression fails to compile", func() {
			By("Waiting for WebRequestCommitStatus to report error in Ready condition")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Expression compilation errors should be reported in Ready condition
				g.Expect(wrcs.Status.Conditions).ToNot(BeEmpty())
				readyCondition := meta.FindStatusCondition(wrcs.Status.Conditions, "Ready")
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Message).To(ContainSubstring("failed to process environment request"))
				g.Expect(readyCondition.Message).To(ContainSubstring("expression compilation failed"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When reportOn is active", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server

		BeforeEach(func() {
			By("Creating a mock HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-active", "default")

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "active-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus with reportOn: active")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "active-check",
					Description: "Active check",
					ReportOn:    "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:     testServer.URL,
						Method:  "GET",
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report on active SHA", func() {
			By("Waiting for WebRequestCommitStatus to report on active SHA")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Verify ReportedSha matches ActiveHydratedSha
				g.Expect(wrcs.Status.Environments[0].ReportedSha).To(Equal(wrcs.Status.Environments[0].ActiveHydratedSha))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When PromotionStrategy is not found", func() {
		const resourceName = "webrequest-no-ps"

		ctx := context.Background()

		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating only a WebRequestCommitStatus resource without PromotionStrategy")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "non-existent",
					},
					Key:         "test-key",
					Description: "Test",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:    "http://localhost:9999",
						Method: "GET",
					},
					Expression: `Response.StatusCode == 200`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should handle missing PromotionStrategy gracefully", func() {
			By("Verifying the WebRequestCommitStatus exists but doesn't process environments")
			Consistently(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Status should be empty since PromotionStrategy doesn't exist
				g.Expect(wrcs.Status.Environments).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})

	Context("When using labels and annotations in templates", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server
		var receivedHeaders http.Header
		var receivedBody map[string]any

		BeforeEach(func() {
			By("Creating a mock HTTP server that captures headers and body")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedHeaders = r.Header.Clone()
				if r.Body != nil {
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &receivedBody)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "ok",
				})
			}))

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-metadata", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "metadata-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus with labels and annotations")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
					Labels: map[string]string{
						"team":     "platform",
						"env-tier": "production",
					},
					Annotations: map[string]string{
						"slack-channel": "#deployments",
						"jira-project":  "DEPLOY",
					},
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "metadata-check",
					Description: "Metadata check",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:    testServer.URL,
						Method: "POST",
						Headers: map[string]string{
							"Content-Type": "application/json",
							"X-Team":       `{{ index .Labels "team" }}`,
							"X-Tier":       `{{ index .Labels "env-tier" }}`,
						},
						Body: `{
							"team": "{{ index .Labels "team" }}",
							"tier": "{{ index .Labels "env-tier" }}",
							"slack": "{{ index .Annotations "slack-channel" }}",
							"jira": "{{ index .Annotations "jira-project" }}"
						}`,
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should render labels and annotations in headers and body", func() {
			By("Waiting for WebRequestCommitStatus to make the HTTP request")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Verify headers contain templated label values
				g.Expect(receivedHeaders.Get("X-Team")).To(Equal("platform"))
				g.Expect(receivedHeaders.Get("X-Tier")).To(Equal("production"))

				// Verify body contains templated label and annotation values
				g.Expect(receivedBody).ToNot(BeNil())
				g.Expect(receivedBody["team"]).To(Equal("platform"))
				g.Expect(receivedBody["tier"]).To(Equal("production"))
				g.Expect(receivedBody["slack"]).To(Equal("#deployments"))
				g.Expect(receivedBody["jira"]).To(Equal("DEPLOY"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When using namespace labels and annotations in templates", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var testServer *httptest.Server
		var receivedHeaders http.Header
		var receivedBody map[string]any

		BeforeEach(func() {
			By("Creating a mock HTTP server that captures headers and body")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedHeaders = r.Header.Clone()
				if r.Body != nil {
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &receivedBody)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "ok",
				})
			}))

			By("Adding labels and annotations to the default namespace")
			var ns v1.Namespace
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "default"}, &ns)
			Expect(err).NotTo(HaveOccurred())

			if ns.Labels == nil {
				ns.Labels = make(map[string]string)
			}
			if ns.Annotations == nil {
				ns.Annotations = make(map[string]string)
			}

			ns.Labels["environment"] = "test"
			ns.Labels["cost-center"] = "engineering"
			ns.Annotations["owner"] = "platform-team"
			ns.Annotations["notification-url"] = "https://notifications.example.com"

			err = k8sClient.Update(ctx, &ns)
			Expect(err).NotTo(HaveOccurred())

			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-ns-metadata", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "namespace-metadata-check"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a WebRequestCommitStatus using namespace metadata")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "namespace-metadata-check",
					Description: "Check using namespace labels: {{ .NamespaceMetadata.Labels.environment }}",
					ReportOn:    "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URL:    testServer.URL,
						Method: "POST",
						Headers: map[string]string{
							"Content-Type":   "application/json",
							"X-Environment":  `{{ .NamespaceMetadata.Labels.environment }}`,
							"X-Cost-Center":  `{{ index .NamespaceMetadata.Labels "cost-center" }}`,
							"X-Notification": `{{ index .NamespaceMetadata.Annotations "notification-url" }}`,
						},
						Body: `{
							"namespace": "{{ .Namespace }}",
							"environment": "{{ .NamespaceMetadata.Labels.environment }}",
							"costCenter": "{{ index .NamespaceMetadata.Labels "cost-center" }}",
							"owner": "{{ .NamespaceMetadata.Annotations.owner }}",
							"notificationUrl": "{{ index .NamespaceMetadata.Annotations "notification-url" }}"
						}`,
						Timeout: metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200`,
					Polling: promoterv1alpha1.PollingSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			testServer.Close()
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)

			By("Cleaning up namespace labels and annotations")
			var ns v1.Namespace
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "default"}, &ns); err == nil {
				delete(ns.Labels, "environment")
				delete(ns.Labels, "cost-center")
				delete(ns.Annotations, "owner")
				delete(ns.Annotations, "notification-url")
				_ = k8sClient.Update(ctx, &ns)
			}
		})

		It("should render namespace labels and annotations in headers and body", func() {
			By("Waiting for WebRequestCommitStatus to make the HTTP request")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(wrcs.Status.Environments).To(HaveLen(3))
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(WebRequestPhaseSuccess))

				// Verify headers contain templated namespace label/annotation values
				g.Expect(receivedHeaders.Get("X-Environment")).To(Equal("test"))
				g.Expect(receivedHeaders.Get("X-Cost-Center")).To(Equal("engineering"))
				g.Expect(receivedHeaders.Get("X-Notification")).To(Equal("https://notifications.example.com"))

				// Verify body contains templated namespace label and annotation values
				g.Expect(receivedBody).ToNot(BeNil())
				g.Expect(receivedBody["namespace"]).To(Equal("default"))
				g.Expect(receivedBody["environment"]).To(Equal("test"))
				g.Expect(receivedBody["costCenter"]).To(Equal("engineering"))
				g.Expect(receivedBody["owner"]).To(Equal("platform-team"))
				g.Expect(receivedBody["notificationUrl"]).To(Equal("https://notifications.example.com"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should render namespace metadata in description template", func() {
			By("Verifying the CommitStatus description uses namespace metadata")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(wrcs.Status.Environments).To(HaveLen(3))

				// Get the CommitStatus and check its description
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testBranchDevelopment+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Description).To(ContainSubstring("test"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
