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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("WebRequestCommitStatus Controller", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		testServer        *httptest.Server
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-commit-status-test", "default")

		// Configure ProposedCommitStatuses to check for web-request commit status
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "external-approval"},
		}

		setupInitialTestGitRepoOnServer(ctx, name, name)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
	})

	AfterAll(func() {
		By("Cleaning up test resources")
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, promotionStrategy)
		}
		if gitRepo != nil {
			_ = k8sClient.Delete(ctx, gitRepo)
		}
		if scmProvider != nil {
			_ = k8sClient.Delete(ctx, scmProvider)
		}
		if scmSecret != nil {
			_ = k8sClient.Delete(ctx, scmSecret)
		}
	})

	Describe("Polling Mode - Success Response", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns success")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"status":   "approved",
				})
			}))

			By("Creating a WebRequestCommitStatus resource with polling mode")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-polling-success",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "external-approval",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate/{{ .ReportedSha }}",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should report success status when HTTP response passes validation", func() {
			By("Waiting for WebRequestCommitStatus to process environments")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-polling-success",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for all environments (dev, staging, production)
				// since ProposedCommitStatuses is set globally on the PromotionStrategy
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Find the dev environment status
				var devEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnvStatus = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnvStatus).ToNot(BeNil(), "Dev environment status should exist")
				g.Expect(devEnvStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Validate status fields are populated
				g.Expect(devEnvStatus.ReportedSha).ToNot(BeEmpty(), "ReportedSha should be populated")
				g.Expect(devEnvStatus.LastRequestTime).ToNot(BeNil(), "LastRequestTime should be populated")
				g.Expect(devEnvStatus.LastResponseStatusCode).ToNot(BeNil(), "LastResponseStatusCode should be populated")
				g.Expect(*devEnvStatus.LastResponseStatusCode).To(Equal(200))

				// Verify CommitStatus was created for dev environment with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-polling-success-"+testBranchDevelopment+"-webrequest")
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

	Describe("Polling Mode - Failure Response", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns failure")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": false,
					"status":   "pending",
				})
			}))

			By("Creating a WebRequestCommitStatus resource with polling mode")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-polling-failure",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "external-approval",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should report pending status when HTTP response fails validation", func() {
			By("Waiting for WebRequestCommitStatus to process environments")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-polling-failure",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for environments
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Find the dev environment status
				var devEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnvStatus = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnvStatus).ToNot(BeNil(), "Dev environment status should exist")
				g.Expect(devEnvStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

				// Verify CommitStatus was created for dev environment with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-polling-failure-"+testBranchDevelopment+"-webrequest")
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

	Describe("Trigger Mode - SHA Change Detection", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var requestCount int

		BeforeEach(func() {
			requestCount = 0

			By("Creating a test HTTP server that counts requests")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			By("Creating a WebRequestCommitStatus resource with trigger mode")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-trigger-mode",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "external-approval",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							// Only trigger when SHA changes from what we tracked
							TriggerExpression: `{trigger: ReportedSha != TriggerData["trackedSha"], trackedSha: ReportedSha}`,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should trigger HTTP request on first reconcile and store expression data", func() {
			By("Waiting for first HTTP request to be made")
			Eventually(func(g Gomega) {
				g.Expect(requestCount).To(BeNumerically(">=", 1), "Should have made at least one HTTP request")

				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-trigger-mode",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for environments
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Find an environment with trigger data stored
				var foundEnvWithData bool
				for _, envStatus := range wrcs.Status.Environments {
					if envStatus.Phase == string(promoterv1alpha1.CommitPhaseSuccess) &&
						envStatus.TriggerData != nil {
						// Parse the trigger data
						var trigData map[string]any
						if err := json.Unmarshal(envStatus.TriggerData.Raw, &trigData); err == nil {
							if trackedSha, ok := trigData["trackedSha"].(string); ok && trackedSha != "" {
								foundEnvWithData = true
								break
							}
						}
					}
				}
				g.Expect(foundEnvWithData).To(BeTrue(), "At least one environment should have trigger data stored")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying subsequent reconciles do not trigger HTTP requests (same SHA)")
			initialCount := requestCount
			Consistently(func(g Gomega) {
				// Request count should not increase significantly since SHA hasn't changed
				// Allow some buffer for multiple environments
				g.Expect(requestCount).To(BeNumerically("<=", initialCount+3), "Should not make many additional HTTP requests for same SHA")
			}, 10*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	Describe("Template Rendering", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var requestsByEnv map[string]struct {
			headers http.Header
			url     string
			body    string
		}
		var requestMu sync.Mutex

		BeforeEach(func() {
			requestsByEnv = make(map[string]struct {
				headers http.Header
				url     string
				body    string
			})

			By("Creating a test HTTP server that captures request details")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestMu.Lock()
				// Extract environment from URL path
				urlPath := r.URL.Path
				bodyBytes := make([]byte, 1024)
				n, _ := r.Body.Read(bodyBytes)
				body := string(bodyBytes[:n])

				// Store by environment branch (extracted from URL)
				for _, env := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					if strings.Contains(urlPath, env) {
						requestsByEnv[env] = struct {
							headers http.Header
							url     string
							body    string
						}{
							headers: r.Header.Clone(),
							url:     r.URL.String(),
							body:    body,
						}
						break
					}
				}
				requestMu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			By("Creating a WebRequestCommitStatus resource with templates")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-template-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:                 "external-approval",
					ReportOn:            "proposed",
					DescriptionTemplate: "Checking {{ .Environment.Branch }} at {{ .ReportedSha | trunc 7 }}",
					UrlTemplate:         "https://example.com/status/{{ .ReportedSha }}",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate/{{ .Environment.Branch }}?sha={{ .ReportedSha }}",
						Method:      "POST",
						HeaderTemplates: map[string]string{
							"X-Environment": "{{ .Environment.Branch }}",
							"X-Sha":         "{{ .ReportedSha }}",
						},
						BodyTemplate: `{"branch": "{{ .Environment.Branch }}", "sha": "{{ .ReportedSha }}"}`,
						Timeout:      metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should render templates correctly in URL, headers, and body", func() {
			By("Waiting for HTTP request to be made with rendered templates for dev environment")
			Eventually(func(g Gomega) {
				requestMu.Lock()
				devReq, exists := requestsByEnv[testBranchDevelopment]
				requestMu.Unlock()

				g.Expect(exists).To(BeTrue(), "Should have received request for dev environment")

				// URL should contain the branch
				g.Expect(devReq.url).To(ContainSubstring(testBranchDevelopment))

				// Headers should be set
				g.Expect(devReq.headers.Get("X-Environment")).To(Equal(testBranchDevelopment))
				g.Expect(devReq.headers.Get("X-Sha")).ToNot(BeEmpty())

				// Body should contain rendered values
				g.Expect(devReq.body).To(ContainSubstring(testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus has rendered description and URL")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-template-test-"+testBranchDevelopment+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())

				// Description should be rendered
				g.Expect(cs.Spec.Description).To(ContainSubstring(testBranchDevelopment))

				// URL should be rendered
				g.Expect(cs.Spec.Url).To(ContainSubstring("https://example.com/status/"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Orphaned CommitStatus Cleanup", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			By("Creating a WebRequestCommitStatus resource for all environments")
			// First, update PromotionStrategy to have commit statuses for all environments
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				ps.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{Key: "cleanup-test"},
				}
				err = k8sClient.Update(ctx, &ps)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-cleanup-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "cleanup-test",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)

			// Restore original PromotionStrategy
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				ps.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{Key: "external-approval"},
				}
				err = k8sClient.Update(ctx, &ps)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should cleanup orphaned CommitStatus resources when environments are removed from PromotionStrategy", func() {
			By("Waiting for CommitStatus resources to be created for all environments")
			var devCommitStatusName string
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup-test",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for at least dev environment
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				devCommitStatusName = utils.KubeSafeUniqueName(ctx, name+"-cleanup-test-"+testBranchDevelopment+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      devCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Removing the key from PromotionStrategy")
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				// Remove the cleanup-test key
				ps.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{}
				err = k8sClient.Update(ctx, &ps)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying orphaned CommitStatus resources are deleted")
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      devCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "Orphaned CommitStatus should be deleted")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("HTTP Error Handling", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns 500 error")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprintln(w, "Internal Server Error")
			}))

			By("Creating a WebRequestCommitStatus resource")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-http-error",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "external-approval",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up WebRequestCommitStatus")
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should handle HTTP 500 error gracefully", func() {
			By("Waiting for WebRequestCommitStatus to process and report failure")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-http-error",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for environments
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Find the dev environment status
				var devEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnvStatus = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnvStatus).ToNot(BeNil(), "Dev environment status should exist")
				// Expression evaluates to false because StatusCode != 200
				g.Expect(devEnvStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
				g.Expect(*devEnvStatus.LastResponseStatusCode).To(Equal(500))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

// Test ResponseData feature (trigger mode only)
var _ = Describe("WebRequestCommitStatus Controller - ResponseData", Ordered, func() {
	var (
		name                   string
		promotionStrategy      *promoterv1alpha1.PromotionStrategy
		gitRepo                *promoterv1alpha1.GitRepository
		scmProvider            *promoterv1alpha1.ScmProvider
		scmSecret              *corev1.Secret
		webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		testServer             *httptest.Server
		requestCount           int
		mu                     sync.Mutex
	)

	const (
		testBranchDevelopment = "environment/development"
	)

	ctx := context.Background()

	BeforeAll(func() {
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-responsedata", "default")

		// Override to only use development environment for this test suite
		promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
		}

		promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "responsedata-test"},
		}

		setupInitialTestGitRepoOnServer(ctx, name, name)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
	})

	AfterAll(func() {
		By("Cleaning up test resources")
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, promotionStrategy)
		}
		if gitRepo != nil {
			_ = k8sClient.Delete(ctx, gitRepo)
		}
		if scmProvider != nil {
			_ = k8sClient.Delete(ctx, scmProvider)
		}
		if scmSecret != nil {
			_ = k8sClient.Delete(ctx, scmSecret)
		}
	})

	BeforeEach(func() {
		requestCount = 0
	})

	AfterEach(func() {
		By("Cleaning up test resources")
		if testServer != nil {
			testServer.Close()
		}
		if webRequestCommitStatus != nil {
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		}
	})

	Context("Trigger Mode", func() {
		It("should NOT store response data without responseExpression", func() {
			By("Creating a test HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"data":     "some-data",
				})
			}))

			By("Creating a WebRequestCommitStatus in trigger mode WITHOUT responseExpression")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "responsedata-test",
					ReportOn: "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration:   metav1.Duration{Duration: 10 * time.Second},
							TriggerExpression: "true",
							// NO responseExpression
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying ResponseData is NOT populated without responseExpression")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				devEnv := wrcs.Status.Environments[0]

				// Verify validation succeeded
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Verify ResponseData is nil without responseExpression
				g.Expect(devEnv.ResponseData).To(BeNil(), "ResponseData should be nil without responseExpression")

				// But lastResponseStatusCode should still be populated
				g.Expect(devEnv.LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*devEnv.LastResponseStatusCode).To(Equal(200))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should preserve response data when trigger returns false", func() {
			By("Creating a test HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requestCount++
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"count":    requestCount,
				})
			}))

			By("Creating WebRequestCommitStatus that only triggers once")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "responsedata-test",
					ReportOn: "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							ResponseExpression: `{
								statusCode: Response.StatusCode,
								approved: Response.Body.approved,
								count: Response.Body.count
							}`,
							TriggerExpression: `{
								trigger: TriggerData == nil || TriggerData["triggered"] != true,
								triggered: true
							}`,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Waiting for first request and response data")
			var firstResponseData []byte
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))
				g.Expect(wrcs.Status.Environments[0].ResponseData).NotTo(BeNil())
				firstResponseData = wrcs.Status.Environments[0].ResponseData.Raw
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying response data is preserved on subsequent reconciles")
			Consistently(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				currentResponseData := wrcs.Status.Environments[0].ResponseData.Raw
				g.Expect(currentResponseData).To(Equal(firstResponseData), "ResponseData should be preserved")

				// Verify only one request was made
				g.Expect(requestCount).To(Equal(1), "Should only make one request")
			}, 15*time.Second, 3*time.Second).Should(Succeed())
		})

		It("should use response data in subsequent trigger expressions", func() {
			By("Creating a test HTTP server that returns retry-after")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requestCount++
				count := requestCount
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				// First two requests return "retry", third returns "done"
				status := "retry"
				if count >= 3 {
					status = "done"
				}

				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": status,
					"count":  count,
				})
			}))

			By("Creating WebRequestCommitStatus that uses ResponseData in trigger")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "responsedata-test",
					ReportOn: "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: `Response.StatusCode == 200 && Response.Body.status == "done"`,
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 3 * time.Second},
							ResponseExpression: `{
								statusCode: Response.StatusCode,
								status: Response.Body.status
							}`,
							// Trigger if no response data or if previous response said to retry
							TriggerExpression: `ResponseData == nil || ResponseData.status == "retry"`,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Waiting for validation to eventually succeed")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				devEnv := wrcs.Status.Environments[0]
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Verify we made multiple requests (at least 3)
				g.Expect(requestCount).To(BeNumerically(">=", 3))

				// Verify final response data shows "done"
				g.Expect(devEnv.ResponseData).NotTo(BeNil())
				var responseData map[string]any
				err = json.Unmarshal(devEnv.ResponseData.Raw, &responseData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(responseData["status"]).To(Equal("done"))
			}, 30*time.Second).Should(Succeed())
		})

		It("should extract custom fields using responseExpression", func() {
			By("Creating a test HTTP server with complex response")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Rate-Limit-Remaining", "42")
				w.Header().Set("X-Request-Id", "abc-123")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"nested": map[string]any{
						"field1": "value1",
						"field2": 123,
					},
					"largeData": "this is a lot of data that we don't need to store...",
					"metadata": map[string]any{
						"timestamp": "2024-01-15T10:30:00Z",
						"user":      "system",
					},
				})
			}))

			By("Creating WebRequestCommitStatus with responseExpression")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "responsedata-test",
					ReportOn: "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration:   metav1.Duration{Duration: 10 * time.Second},
							TriggerExpression: "true",
							// Extract only the fields we care about
							ResponseExpression: `{
								statusCode: Response.StatusCode,
								approved: Response.Body.approved,
								nestedField: Response.Body.nested.field1,
								rateLimit: int(Response.Headers["X-Rate-Limit-Remaining"][0]),
								timestamp: Response.Body.metadata.timestamp
							}`,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying only extracted fields are in ResponseData")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				devEnv := wrcs.Status.Environments[0]
				g.Expect(devEnv.ResponseData).NotTo(BeNil())

				// Parse the response data
				var responseData map[string]any
				err = json.Unmarshal(devEnv.ResponseData.Raw, &responseData)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify only the extracted fields are present
				g.Expect(responseData).To(HaveKey("statusCode"))
				g.Expect(responseData).To(HaveKey("approved"))
				g.Expect(responseData).To(HaveKey("nestedField"))
				g.Expect(responseData).To(HaveKey("rateLimit"))
				g.Expect(responseData).To(HaveKey("timestamp"))

				// Verify the values
				g.Expect(responseData["statusCode"]).To(Equal(float64(200)))
				g.Expect(responseData["approved"]).To(Equal(true))
				g.Expect(responseData["nestedField"]).To(Equal("value1"))
				g.Expect(responseData["rateLimit"]).To(Equal(float64(42)))
				g.Expect(responseData["timestamp"]).To(Equal("2024-01-15T10:30:00Z"))

				// Verify fields we didn't extract are NOT present
				g.Expect(responseData).NotTo(HaveKey("body"))
				g.Expect(responseData).NotTo(HaveKey("headers"))
				g.Expect(responseData).NotTo(HaveKey("largeData"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("Polling Mode", func() {
		It("should NOT populate response data", func() {
			By("Creating a test HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Custom-Header", "test-value")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"data":     "some-data",
				})
			}))

			By("Creating a WebRequestCommitStatus in polling mode")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "responsedata-test",
					ReportOn: "active",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying ResponseData is NOT populated in polling mode")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Verify validation succeeded
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Verify ResponseData is nil in polling mode
				g.Expect(wrcs.Status.Environments[0].ResponseData).To(BeNil(), "ResponseData should be nil in polling mode")

				// But lastResponseStatusCode should still be populated
				g.Expect(wrcs.Status.Environments[0].LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*wrcs.Status.Environments[0].LastResponseStatusCode).To(Equal(200))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

// Separate Describe block for the test that doesn't need infrastructure
var _ = Describe("WebRequestCommitStatus Controller - Missing PromotionStrategy", func() {
	Context("When PromotionStrategy is not found", func() {
		const resourceName = "webrequest-status-no-ps"

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
					Key:      "external-approval",
					ReportOn: "proposed",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: "http://example.com/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Expression: "Response.StatusCode == 200",
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
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
})
