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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

//go:embed testdata/WebRequestCommitStatus.yaml
var testWebRequestCommitStatusYAML string

//go:embed testdata/WebRequestCommitStatus_promotionstrategy_ctx.yaml
var testWebRequestCommitStatusPromotionStrategyCtxYAML string

var _ = Describe("WebRequestCommitStatus Controller", Ordered, func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the WebRequestCommitStatus resource", func() {
			err := unmarshalYamlStrict(testWebRequestCommitStatusYAML, &promoterv1alpha1.WebRequestCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should unmarshal promotionstrategy context WebRequestCommitStatus fixture", func() {
			err := unmarshalYamlStrict(testWebRequestCommitStatusPromotionStrategyCtxYAML, &promoterv1alpha1.WebRequestCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

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

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate/{{ .ReportedSha }}",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
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
				g.Expect(devEnvStatus.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
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
				g.Expect(devEnvStatus.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))

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

		Describe("Polling Mode - Interval Short-Circuit", func() {
			var (
				webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
				requestCount           int
				requestMu              sync.Mutex
			)

			BeforeEach(func() {
				requestCount = 0
				By("Creating a test HTTP server that counts requests and never returns approved (so we hit interval short-circuit path, not 'already successful SHA')")
				testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestMu.Lock()
					requestCount++
					requestMu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"approved": false,
						"status":   "pending",
					})
				}))

				By("Creating a WebRequestCommitStatus in polling mode with long interval")
				webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name + "-polling-interval-shortcircuit",
						Namespace: "default",
					},
					Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
						PromotionStrategyRef: promoterv1alpha1.ObjectReference{
							Name: name,
						},
						Key:      "external-approval",
						ReportOn: constants.CommitRefProposed,
						HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
							URLTemplate: testServer.URL + "/validate/{{ .ReportedSha }}",
							Method:      "GET",
							Timeout:     metav1.Duration{Duration: 10 * time.Second},
						},
						Success: promoterv1alpha1.SuccessSpec{
							When: promoterv1alpha1.WhenSpec{
								Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
							},
						},
						Mode: promoterv1alpha1.ModeSpec{
							Polling: &promoterv1alpha1.PollingModeSpec{
								// Long interval so a second reconcile triggered by PS update is still "within interval"
								Interval: metav1.Duration{Duration: 10 * time.Minute},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
			})

			AfterEach(func() {
				if testServer != nil {
					testServer.Close()
				}
				_ = k8sClient.Delete(ctx, webRequestCommitStatus)
			})

			// Success = with short-circuit code in place, a second reconcile within the polling interval must not call the HTTP server.
			// Server returns approved: false so validation stays Pending and we exercise the interval short-circuit path (not "already successful SHA").
			It("should skip HTTP request when reconcile runs within polling interval", func() {
				By("Waiting for first reconcile to complete (one HTTP request per environment) and set LastRequestTime; phase stays Pending (approved: false)")
				var initialRequestCount int
				Eventually(func(g Gomega) {
					var wrcs promoterv1alpha1.WebRequestCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      name + "-polling-interval-shortcircuit",
						Namespace: "default",
					}, &wrcs)
					g.Expect(err).NotTo(HaveOccurred())
					// Wait until ALL environments have been processed (not just >= 1), so that every
					// environment has made its first HTTP request before we snapshot initialRequestCount.
					// The PromotionStrategy has 3 environments (dev, staging, production). If we only wait
					// for >= 1, staging and production may not have fired yet, and their first requests
					// would then race with the Consistently block and falsely increment the count.
					g.Expect(len(wrcs.Status.Environments)).To(Equal(3), "all three environments must be processed before snapshotting the request count")
					for i := range wrcs.Status.Environments {
						g.Expect(wrcs.Status.Environments[i].LastRequestTime).ToNot(BeNil(), "LastRequestTime should be set after first request")
						g.Expect(wrcs.Status.Environments[i].Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "validation fails (approved: false) so phase stays Pending")
					}
					requestMu.Lock()
					initialRequestCount = requestCount
					requestMu.Unlock()
					g.Expect(initialRequestCount).To(BeNumerically(">=", 3), "Should have made at least one HTTP request per environment (3 total) before snapshotting")
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Triggering another reconcile by updating the WebRequestCommitStatus (annotation)")
				Eventually(func(g Gomega) {
					var wrcs promoterv1alpha1.WebRequestCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      name + "-polling-interval-shortcircuit",
						Namespace: "default",
					}, &wrcs)
					g.Expect(err).NotTo(HaveOccurred())
					if wrcs.Annotations == nil {
						wrcs.Annotations = make(map[string]string)
					}
					wrcs.Annotations["test-reconcile-trigger"] = strconv.FormatInt(time.Now().UnixNano(), 10)
					err = k8sClient.Update(ctx, &wrcs)
					g.Expect(err).NotTo(HaveOccurred())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Success: no additional HTTP request for 10s — controller must not hit the server on second reconcile within interval")
				Consistently(func(g Gomega) {
					requestMu.Lock()
					count := requestCount
					requestMu.Unlock()
					g.Expect(count).To(Equal(initialRequestCount), "controller must not call HTTP server when reconcile runs within polling interval (short-circuit success)")
				}, 10*time.Second).Should(Succeed())
			})
		})

		It("should only update lastSuccessfulSha when validation succeeds", func() {
			By("Creating a test HTTP server that starts failing then succeeds")
			var requestCount int
			var mu sync.Mutex
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requestCount++
				count := requestCount
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				// First 2 requests: not approved (validation should fail)
				// After that: approved (validation should succeed)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": count > 2,
					"status":   map[bool]string{true: "approved", false: "pending"}[count > 2],
				})
			}))

			By("Creating WebRequestCommitStatus in polling mode")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-polling-lastsuccessfulsha",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: promotionStrategy.Name,
					},
					Key: "external-approval",
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL,
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 2 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Waiting for first requests (pending phase) and verifying lastSuccessfulSha is empty")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-polling-lastsuccessfulsha",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				var devEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnvStatus = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnvStatus).ToNot(BeNil())
				g.Expect(devEnvStatus.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				// lastSuccessfulSha should still be empty when in pending phase
				g.Expect(devEnvStatus.LastSuccessfulSha).To(BeEmpty(), "lastSuccessfulSha should be empty when validation has not succeeded")
				g.Expect(devEnvStatus.ReportedSha).NotTo(BeEmpty(), "reportedSha should be set")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for validation to succeed and lastSuccessfulSha to be set")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-polling-lastsuccessfulsha",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())

				var devEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnvStatus = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnvStatus).ToNot(BeNil())
				g.Expect(devEnvStatus.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				// lastSuccessfulSha should now be set to reportedSha after success
				g.Expect(devEnvStatus.LastSuccessfulSha).To(Equal(devEnvStatus.ReportedSha), "lastSuccessfulSha should match reportedSha after success")
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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							// Only trigger when SHA changes from what we tracked
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "ReportedSha != TriggerOutput[\"trackedSha\"]",
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: ReportedSha }`},
							},
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
					if envStatus.Phase == promoterv1alpha1.CommitPhaseSuccess &&
						envStatus.TriggerOutput != nil {
						// Parse the trigger data
						var trigData map[string]any
						if err := json.Unmarshal(envStatus.TriggerOutput.Raw, &trigData); err == nil {
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
					ReportOn:            constants.CommitRefProposed,
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
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
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
				g.Expect(devEnvStatus.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(*devEnvStatus.LastResponseStatusCode).To(Equal(500))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

// Test ResponseOutput feature (trigger mode only)
var _ = Describe("WebRequestCommitStatus Controller - ResponseOutput", Ordered, func() {
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

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

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
		It("should NOT store response data without response.output.expression", func() {
			By("Creating a test HTTP server")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"data":     "some-data",
				})
			}))

			By("Creating a WebRequestCommitStatus in trigger mode WITHOUT response.output.expression")
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
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 10 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
							},
							// NO response field
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying response output is NOT populated without response.output.expression")
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
				g.Expect(devEnv.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

				// Verify response output is nil without response.output.expression
				g.Expect(devEnv.ResponseOutput).To(BeNil(), "response output should be nil without response.output.expression")

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
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "TriggerOutput == nil || TriggerOutput[\"triggered\"] != true",
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ triggered: true }`},
							},
							Response: &promoterv1alpha1.ResponseOutputSpec{
								Output: promoterv1alpha1.OutputSpec{
									Expression: `{
								statusCode: Response.StatusCode,
								approved: Response.Body.approved,
								count: Response.Body.count
							}`,
								},
							},
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
				g.Expect(wrcs.Status.Environments[0].ResponseOutput).NotTo(BeNil())
				firstResponseData = wrcs.Status.Environments[0].ResponseOutput.Raw
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

				currentResponseData := wrcs.Status.Environments[0].ResponseOutput.Raw
				g.Expect(currentResponseData).To(Equal(firstResponseData), "response output should be preserved")

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

			By("Creating WebRequestCommitStatus that uses ResponseOutput in trigger")
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
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: `Response.StatusCode == 200 && Response.Body.status == "done"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 3 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								// Trigger if no response data or if previous response said to retry
								Expression: `ResponseOutput == nil || ResponseOutput.status == "retry"`,
							},
							Response: &promoterv1alpha1.ResponseOutputSpec{
								Output: promoterv1alpha1.OutputSpec{
									Expression: `{
								statusCode: Response.StatusCode,
								status: Response.Body.status
							}`,
								},
							},
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
				g.Expect(devEnv.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

				// Verify we made multiple requests (at least 3)
				g.Expect(requestCount).To(BeNumerically(">=", 3))

				// Verify final response data shows "done"
				g.Expect(devEnv.ResponseOutput).NotTo(BeNil())
				var responseData map[string]any
				err = json.Unmarshal(devEnv.ResponseOutput.Raw, &responseData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(responseData["status"]).To(Equal("done"))
			}, 30*time.Second).Should(Succeed())
		})

		It("should extract custom fields using response.output.expression", func() {
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

			By("Creating WebRequestCommitStatus with response.output.expression")
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
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 10 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
							},
							// Extract only the fields we care about
							Response: &promoterv1alpha1.ResponseOutputSpec{
								Output: promoterv1alpha1.OutputSpec{
									Expression: `{
								statusCode: Response.StatusCode,
								approved: Response.Body.approved,
								nestedField: Response.Body.nested.field1,
								rateLimit: int(Response.Headers["X-Rate-Limit-Remaining"][0]),
								timestamp: Response.Body.metadata.timestamp
							}`,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying only extracted fields are in response output")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				devEnv := wrcs.Status.Environments[0]
				g.Expect(devEnv.ResponseOutput).NotTo(BeNil())

				// Parse the response data
				var responseData map[string]any
				err = json.Unmarshal(devEnv.ResponseOutput.Raw, &responseData)
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
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webRequestCommitStatus)).To(Succeed())

			By("Verifying response output is NOT populated in polling mode")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))

				// Verify validation succeeded
				g.Expect(wrcs.Status.Environments[0].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

				// Verify response output is nil in polling mode
				g.Expect(wrcs.Status.Environments[0].ResponseOutput).To(BeNil(), "response output should be nil in polling mode")

				// But lastResponseStatusCode should still be populated
				g.Expect(wrcs.Status.Environments[0].LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*wrcs.Status.Environments[0].LastResponseStatusCode).To(Equal(200))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Scm - use PromotionStrategy SCM credentials", func() {
		var (
			scmAuthCtx               context.Context
			scmAuthName              string
			scmAuthScmSecret         *corev1.Secret
			scmAuthScmProvider       *promoterv1alpha1.ScmProvider
			scmAuthGitRepo           *promoterv1alpha1.GitRepository
			scmAuthPromotionStrategy *promoterv1alpha1.PromotionStrategy
			webRequestCommitStatus   *promoterv1alpha1.WebRequestCommitStatus
			scmAuthTestServer        *httptest.Server
			scmAuthLastRequest       *http.Request
			scmAuthLastRequestMu     sync.Mutex
		)

		BeforeEach(func() {
			scmAuthLastRequestMu.Lock()
			scmAuthLastRequest = nil
			scmAuthLastRequestMu.Unlock()
			scmAuthCtx = context.Background()

			By("Creating a test HTTP server for Scm test")
			scmAuthTestServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				scmAuthLastRequestMu.Lock()
				scmAuthLastRequest = r
				scmAuthLastRequestMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))

			serverParsed, err := url.Parse(scmAuthTestServer.URL)
			Expect(err).NotTo(HaveOccurred())
			serverHost := serverParsed.Host

			By("Creating unique SCM resources with Fake domain set to test server host")
			scmAuthName, scmAuthScmSecret, scmAuthScmProvider, scmAuthGitRepo, _, _, scmAuthPromotionStrategy = promotionStrategyResource(scmAuthCtx, "webrequest-responsedata-scmauth", "default")
			scmAuthScmProvider.Spec.Fake = &promoterv1alpha1.Fake{Domain: serverHost}
			scmAuthPromotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
			}
			scmAuthPromotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "responsedata-test"},
			}
			setupInitialTestGitRepoOnServer(scmAuthCtx, scmAuthGitRepo)
			Expect(k8sClient.Create(scmAuthCtx, scmAuthScmSecret)).To(Succeed())
			Expect(k8sClient.Create(scmAuthCtx, scmAuthScmProvider)).To(Succeed())
			Expect(k8sClient.Create(scmAuthCtx, scmAuthGitRepo)).To(Succeed())
			Expect(k8sClient.Create(scmAuthCtx, scmAuthPromotionStrategy)).To(Succeed())

			By("Creating a WebRequestCommitStatus with authentication.scm (Fake provider = no auth applied)")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scmAuthName + "-scmauth",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: scmAuthName,
					},
					Key:      "responsedata-test",
					ReportOn: constants.CommitRefActive,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: scmAuthTestServer.URL + "/check/{{ .ReportedSha }}",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.ok == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(scmAuthCtx, webRequestCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmAuthTestServer != nil {
				scmAuthTestServer.Close()
				scmAuthTestServer = nil
			}
			if webRequestCommitStatus != nil {
				_ = k8sClient.Delete(scmAuthCtx, webRequestCommitStatus)
				webRequestCommitStatus = nil
			}
			if scmAuthPromotionStrategy != nil {
				_ = k8sClient.Delete(scmAuthCtx, scmAuthPromotionStrategy)
				scmAuthPromotionStrategy = nil
			}
			if scmAuthGitRepo != nil {
				_ = k8sClient.Delete(scmAuthCtx, scmAuthGitRepo)
				scmAuthGitRepo = nil
			}
			if scmAuthScmProvider != nil {
				_ = k8sClient.Delete(scmAuthCtx, scmAuthScmProvider)
				scmAuthScmProvider = nil
			}
			if scmAuthScmSecret != nil {
				_ = k8sClient.Delete(scmAuthCtx, scmAuthScmSecret)
				scmAuthScmSecret = nil
			}
		})

		It("should make HTTP request and report success when using Scm with Fake SCM provider", func() {
			By("Waiting for WebRequestCommitStatus to process environments with Scm")
			Eventually(func(g Gomega) {
				var wrcs promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(scmAuthCtx, types.NamespacedName{
					Name:      scmAuthName + "-scmauth",
					Namespace: "default",
				}, &wrcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(wrcs.Status.Environments)).To(BeNumerically(">=", 1))
				var devEnv *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnv = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnv).ToNot(BeNil())
				g.Expect(devEnv.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(devEnv.LastResponseStatusCode).ToNot(BeNil())
				g.Expect(*devEnv.LastResponseStatusCode).To(Equal(200))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying Fake SCM provider applied no auth headers to the request")
			scmAuthLastRequestMu.Lock()
			lastReq := scmAuthLastRequest
			scmAuthLastRequestMu.Unlock()
			Expect(lastReq).ToNot(BeNil())
			Expect(lastReq.Header.Get("Authorization")).To(BeEmpty(), "Fake provider should not set Authorization")
			Expect(lastReq.Header.Get("Private-Token")).To(BeEmpty(), "Fake provider should not set PRIVATE-TOKEN")
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
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: "http://example.com/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
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

// SCM domain host validation tests — verifies that when authentication is configured,
// the rendered URL host must match the SCM provider's allowed domains.
// Each test uses unique resources (via promotionStrategyResource) so no shared patch/reset is needed.
var _ = Describe("WebRequestCommitStatus Controller - SCM Host Validation", func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		testServer        *httptest.Server
		wrcs              *promoterv1alpha1.WebRequestCommitStatus
	)

	Context("when the URL host matches the SCM provider domain", func() {
		BeforeEach(func() {
			ctx = context.Background()
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			serverURL := testServer.URL
			serverParsed, err := url.Parse(serverURL)
			Expect(err).NotTo(HaveOccurred())
			serverHost := serverParsed.Host

			By("Creating unique SCM resources with Fake domain set to test server host")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-scm-host-validation", "default")
			scmProvider.Spec.Fake = &promoterv1alpha1.Fake{Domain: serverHost}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
			}
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "scm-host-check"},
			}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
				testServer = nil
			}
			if wrcs != nil {
				_ = k8sClient.Delete(ctx, wrcs)
				wrcs = nil
			}
			if promotionStrategy != nil {
				_ = k8sClient.Delete(ctx, promotionStrategy)
				promotionStrategy = nil
			}
			if gitRepo != nil {
				_ = k8sClient.Delete(ctx, gitRepo)
				gitRepo = nil
			}
			if scmProvider != nil {
				_ = k8sClient.Delete(ctx, scmProvider)
				scmProvider = nil
			}
			if scmSecret != nil {
				_ = k8sClient.Delete(ctx, scmSecret)
				scmSecret = nil
			}
		})

		It("should make the HTTP request when the URL host matches the SCM provider domain", func() {
			By("Creating a WebRequestCommitStatus with Scm")
			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-host-match",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "scm-host-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 5 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

			By("Expecting the WRCS to process the environment and reach success")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-host-match",
					Namespace: "default",
				}, &fetched)).To(Succeed())

				g.Expect(fetched.Status.Environments).ToNot(BeEmpty())
				var devStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range fetched.Status.Environments {
					if fetched.Status.Environments[i].Branch == testBranchDevelopment {
						devStatus = &fetched.Status.Environments[i]
						break
					}
				}
				g.Expect(devStatus).ToNot(BeNil(), "dev environment status should exist")
				g.Expect(devStatus.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("when the URL host does not match the SCM provider domain", func() {
		BeforeEach(func() {
			ctx = context.Background()
			By("Creating unique SCM resources with Fake domain that does not match the test server")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-scm-host-validation", "default")
			scmProvider.Spec.Fake = &promoterv1alpha1.Fake{Domain: "allowed.example.com"}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
			}
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "scm-host-check"},
			}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
				testServer = nil
			}
			if wrcs != nil {
				_ = k8sClient.Delete(ctx, wrcs)
				wrcs = nil
			}
			if promotionStrategy != nil {
				_ = k8sClient.Delete(ctx, promotionStrategy)
				promotionStrategy = nil
			}
			if gitRepo != nil {
				_ = k8sClient.Delete(ctx, gitRepo)
				gitRepo = nil
			}
			if scmProvider != nil {
				_ = k8sClient.Delete(ctx, scmProvider)
				scmProvider = nil
			}
			if scmSecret != nil {
				_ = k8sClient.Delete(ctx, scmSecret)
				scmSecret = nil
			}
		})

		It("should set Ready=False when the URL host does not match the SCM provider domain", func() {
			By("Creating a WebRequestCommitStatus with Scm pointing at the test server")
			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-host-mismatch",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "scm-host-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						// URL points at the test server (127.0.0.1:PORT), not allowed.example.com
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 5 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

			By("Expecting the WRCS to surface a Ready=False condition with an SCM host validation error")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-host-mismatch",
					Namespace: "default",
				}, &fetched)).To(Succeed())

				var readyCondition *metav1.Condition
				for i := range fetched.Status.Conditions {
					if fetched.Status.Conditions[i].Type == "Ready" {
						readyCondition = &fetched.Status.Conditions[i]
						break
					}
				}
				g.Expect(readyCondition).ToNot(BeNil(), "Ready condition should be set")
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Message).To(ContainSubstring("SCM host validation failed"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Context PromotionStrategy", Ordered, func() {
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

		By("Setting up test git repository and resources for context=promotionstrategy tests")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-ctx-ps-test", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "ps-context-check"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

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

	Describe("Polling Mode - Boolean Success Expression", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns approved=true")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-bool-success",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should report success for all environments using PromotionStrategyContext status", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil(), "PromotionStrategyContext should be populated")
				g.Expect(fetched.Status.PromotionStrategyContext.LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*fetched.Status.PromotionStrategyContext.LastResponseStatusCode).To(Equal(200))
				g.Expect(fetched.Status.PromotionStrategyContext.LastRequestTime).NotTo(BeNil())

				// Per-environment status slice should be empty in promotionstrategy context
				g.Expect(fetched.Status.Environments).To(BeEmpty(), "Environments should be empty in promotionstrategy context")

				// CommitStatuses should be created for each applicable environment
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
					csName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+branch+"-webrequest")
					var cs promoterv1alpha1.CommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      csName,
						Namespace: "default",
					}, &cs)
					g.Expect(err).NotTo(HaveOccurred(), "CommitStatus for %s should exist", branch)
					g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "CommitStatus for %s should be success", branch)
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Polling Mode - Boolean Pending Expression", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns approved=false")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": false,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-bool-pending",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should report pending for all environments when expression returns false", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				g.Expect(fetched.Status.Environments).To(BeEmpty())

				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhasePending), "PhasePerBranch for %s should be pending", branch)
					csName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+branch+"-webrequest")
					var cs promoterv1alpha1.CommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      csName,
						Namespace: "default",
					}, &cs)
					g.Expect(err).NotTo(HaveOccurred(), "CommitStatus for %s should exist", branch)
					g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "CommitStatus for %s should be pending", branch)
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Polling Mode - Per-Branch Phase Expression", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns per-branch approval data")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"environments": map[string]string{
						testBranchDevelopment: "approved",
						testBranchStaging:     "pending",
						testBranchProduction:  "pending",
					},
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-perbranch",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: `{defaultPhase: "pending", environments: [` +
								`{branch: "` + testBranchDevelopment + `", phase: Response.Body.environments["` + testBranchDevelopment + `"] == "approved" ? "success" : "pending"},` +
								`{branch: "` + testBranchStaging + `", phase: Response.Body.environments["` + testBranchStaging + `"] == "approved" ? "success" : "pending"},` +
								`{branch: "` + testBranchProduction + `", phase: Response.Body.environments["` + testBranchProduction + `"] == "approved" ? "success" : "pending"}` +
								`]}`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should set different phases per branch when expression returns per-branch object", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch).NotTo(BeNil(),
					"PhasePerBranch should be populated")
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[testBranchDevelopment]).To(
					Equal(promoterv1alpha1.CommitPhaseSuccess), "dev should be success")
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[testBranchStaging]).To(
					Equal(promoterv1alpha1.CommitPhasePending), "staging should be pending")
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[testBranchProduction]).To(
					Equal(promoterv1alpha1.CommitPhasePending), "production should be pending")

				devCSName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+testBranchDevelopment+"-webrequest")
				var devCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: devCSName, Namespace: "default"}, &devCS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(devCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

				stagingCSName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+testBranchStaging+"-webrequest")
				var stagingCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: stagingCSName, Namespace: "default"}, &stagingCS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(stagingCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))

				prodCSName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+testBranchProduction+"-webrequest")
				var prodCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: prodCSName, Namespace: "default"}, &prodCS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(prodCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Trigger Mode - TriggerOutput", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that always returns success")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-trigger",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
								Output: &promoterv1alpha1.OutputSpec{
									Expression: `{lastCheckTime: "checked"}`,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should trigger HTTP request and store trigger output in PromotionStrategyContext", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())

				g.Expect(fetched.Status.PromotionStrategyContext.TriggerOutput).NotTo(BeNil(),
					"TriggerOutput should be populated")

				var triggerData map[string]any
				err = json.Unmarshal(fetched.Status.PromotionStrategyContext.TriggerOutput.Raw, &triggerData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(triggerData["lastCheckTime"]).To(Equal("checked"))

				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
					csName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+branch+"-webrequest")
					var cs promoterv1alpha1.CommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
					g.Expect(err).NotTo(HaveOccurred(), "CommitStatus for %s should exist", branch)
					g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Trigger Mode - Response Output", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns response data")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved":   true,
					"approver":   "test-user",
					"approvedAt": "2024-01-15T10:00:00Z",
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-response-output",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
							},
							Response: &promoterv1alpha1.ResponseOutputSpec{
								Output: promoterv1alpha1.OutputSpec{
									Expression: `{approver: Response.Body.approver, approvedAt: Response.Body.approvedAt}`,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should extract and store response output in PromotionStrategyContext", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
				}
				g.Expect(fetched.Status.PromotionStrategyContext.ResponseOutput).NotTo(BeNil(),
					"ResponseOutput should be populated in trigger mode with response.output")

				var responseData map[string]any
				err = json.Unmarshal(fetched.Status.PromotionStrategyContext.ResponseOutput.Raw, &responseData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(responseData["approver"]).To(Equal("test-user"))
				g.Expect(responseData["approvedAt"]).To(Equal("2024-01-15T10:00:00Z"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Polling Mode - Skip Optimization When All Successful", func() {
		var (
			wrcs         *promoterv1alpha1.WebRequestCommitStatus
			requestCount int
			requestMu    sync.Mutex
		)

		BeforeEach(func() {
			requestCount = 0
			By("Creating a test HTTP server that counts requests and returns success")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestMu.Lock()
				requestCount++
				requestMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-skip-opt",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 2 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should skip HTTP requests after all environments succeed for their current SHAs", func() {
			By("Waiting for the initial HTTP request to succeed")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
				}
				g.Expect(fetched.Status.PromotionStrategyContext.LastSuccessfulShas).NotTo(BeEmpty(),
					"LastSuccessfulShas should be populated after success")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Capturing request count after initial success")
			requestMu.Lock()
			initialCount := requestCount
			requestMu.Unlock()
			Expect(initialCount).To(BeNumerically(">=", 1), "at least one HTTP request should have been made")
			Expect(initialCount).To(BeNumerically("<=", 4),
				"context=promotionstrategy should not fan out to per-environment HTTP calls; a few reconciles may occur before success")

			By("Observing that HTTP request count stays flat across polling intervals (skip optimization)")
			Consistently(func(g Gomega) {
				requestMu.Lock()
				c := requestCount
				requestMu.Unlock()
				g.Expect(c).To(BeNumerically("<=", initialCount+1),
					"HTTP requests should be skipped after all environments succeed (initial=%d)", initialCount)
			}, 6*time.Second).Should(Succeed())
		})
	})

	Describe("HTTP Error Handling", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server that returns 500")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "internal server error",
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-http-error",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "ps-context-check",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: testServer.URL + "/validate",
						Method:      "GET",
						Timeout:     metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenSpec{
							Expression: "Response.StatusCode == 200",
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, wrcs)
		})

		It("should handle HTTP 500 error gracefully and report pending", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      wrcs.Name,
					Namespace: "default",
				}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
						Equal(promoterv1alpha1.CommitPhasePending), "PhasePerBranch for %s should be pending", branch)
				}
				g.Expect(fetched.Status.PromotionStrategyContext.LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*fetched.Status.PromotionStrategyContext.LastResponseStatusCode).To(Equal(500))

				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					csName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+branch+"-webrequest")
					var cs promoterv1alpha1.CommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
					g.Expect(err).NotTo(HaveOccurred(), "CommitStatus for %s should exist", branch)
					g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
						"CommitStatus for %s should reflect pending when HTTP validation fails", branch)
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Context PromotionStrategy Zero Applicable Environments", Ordered, func() {
	var (
		ctx                context.Context
		name               string
		scmSecret          *corev1.Secret
		scmProvider        *promoterv1alpha1.ScmProvider
		gitRepo            *promoterv1alpha1.GitRepository
		promotionStrategy  *promoterv1alpha1.PromotionStrategy
		testServer         *httptest.Server
		wrcs               *promoterv1alpha1.WebRequestCommitStatus
		httpRequestCount   int
		httpRequestCountMu sync.Mutex
	)

	BeforeAll(func() {
		ctx = context.Background()

		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-ctx-ps-zero-app", "default")
		// List a commit-status key that our WebRequestCommitStatus will not use, so no environment is applicable.
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "other-promotion-gate"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

		httpRequestCount = 0
		testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpRequestCountMu.Lock()
			httpRequestCount++
			httpRequestCountMu.Unlock()
			http.Error(w, "HTTP should not be invoked when there are zero applicable environments", http.StatusInternalServerError)
		}))
	})

	AfterAll(func() {
		if wrcs != nil {
			_ = k8sClient.Delete(ctx, wrcs)
		}
		if testServer != nil {
			testServer.Close()
		}
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

	It("should clear promotionstrategy context and skip HTTP when no environments match the WRCS key", func() {
		wrcs = &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-zero-app-env",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  "webrequest-key-not-on-promotionstrategy",
				ReportOn:             constants.CommitRefProposed,
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: testServer.URL + "/check",
					Method:      "GET",
					Timeout:     metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenSpec{
						Expression: "Response.StatusCode == 200",
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{Duration: 5 * time.Second},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched promoterv1alpha1.WebRequestCommitStatus
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)).To(Succeed())
			g.Expect(fetched.Status.PromotionStrategyContext).To(BeNil(),
				"processContextPromotionStrategy clears promotionStrategyContext when applicableEnvs is empty")
			g.Expect(fetched.Status.Environments).To(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		httpRequestCountMu.Lock()
		hits := httpRequestCount
		httpRequestCountMu.Unlock()
		Expect(hits).To(Equal(0), "HTTP must not run when getApplicableEnvironments returns no environments")
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Context PromotionStrategy ReportOn Active", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		testServer        *httptest.Server
		webRequestCS      *promoterv1alpha1.WebRequestCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up PromotionStrategy with activeCommitStatuses and a single environment")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-ctx-ps-active", "default")
		promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
		}
		promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "ps-active-ctx-check"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

		testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approved": true,
			})
		}))
	})

	AfterAll(func() {
		if webRequestCS != nil {
			_ = k8sClient.Delete(ctx, webRequestCS)
		}
		if testServer != nil {
			testServer.Close()
		}
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

	It("should use active hydrated SHAs for CommitStatus when reportOn is active", func() {
		var activeSha string
		Eventually(func(g Gomega) {
			var ps promoterv1alpha1.PromotionStrategy
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &ps)).To(Succeed())
			g.Expect(ps.Status.Environments).NotTo(BeEmpty())
			found := false
			for i := range ps.Status.Environments {
				if ps.Status.Environments[i].Branch != testBranchDevelopment {
					continue
				}
				activeSha = ps.Status.Environments[i].Active.Hydrated.Sha
				found = true
				break
			}
			g.Expect(found).To(BeTrue(), "development branch should appear in PromotionStrategy status")
			g.Expect(activeSha).NotTo(BeEmpty(), "active hydrated SHA must exist before creating WebRequestCommitStatus")
		}, constants.EventuallyTimeout).Should(Succeed())

		webRequestCS = &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-ctx-ps-active",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  "ps-active-ctx-check",
				ReportOn:             constants.CommitRefActive,
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: testServer.URL + "/validate",
					Method:      "GET",
					Timeout:     metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenSpec{
						Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, webRequestCS)).To(Succeed())

		Eventually(func(g Gomega) {
			var fetched promoterv1alpha1.WebRequestCommitStatus
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: webRequestCS.Name, Namespace: "default"}, &fetched)).To(Succeed())
			g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
			g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[testBranchDevelopment]).To(
				Equal(promoterv1alpha1.CommitPhaseSuccess))

			csName := utils.KubeSafeUniqueName(ctx, webRequestCS.Name+"-"+testBranchDevelopment+"-webrequest")
			var cs promoterv1alpha1.CommitStatus
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)).To(Succeed())
			g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			g.Expect(cs.Spec.Sha).To(Equal(activeSha))
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Context Switching", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		testServer        *httptest.Server
		wrcs              *promoterv1alpha1.WebRequestCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources for context-switching tests")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-ctx-switch-test", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "ctx-switch-check"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

		By("Creating a test HTTP server that returns approved=true")
		testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approved": true,
			})
		}))
	})

	AfterAll(func() {
		if testServer != nil {
			testServer.Close()
		}
		if wrcs != nil {
			_ = k8sClient.Delete(ctx, wrcs)
		}
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

	It("should cleanly transition status when switching between environments and promotionstrategy contexts", func() {
		By("Creating a WRCS with default (environments) context")
		wrcs = &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-ctx-switch",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  "ctx-switch-check",
				ReportOn:             constants.CommitRefProposed,
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: testServer.URL + "/validate",
					Method:      "GET",
					Timeout:     metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenSpec{
						Expression: "Response.StatusCode == 200 && Response.Body.approved == true",
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

		By("Waiting for environments context status to be populated")
		Eventually(func(g Gomega) {
			var fetched promoterv1alpha1.WebRequestCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(fetched.Status.Environments)).To(Equal(3), "all PromotionStrategy environments should have status entries")
			g.Expect(fetched.Status.PromotionStrategyContext).To(BeNil(), "PromotionStrategyContext should be nil in environments context")

			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				var envSt *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range fetched.Status.Environments {
					if fetched.Status.Environments[i].Branch == branch {
						envSt = &fetched.Status.Environments[i]
						break
					}
				}
				g.Expect(envSt).NotTo(BeNil(), "environment status should exist for branch %s", branch)
				g.Expect(envSt.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "branch %s should succeed", branch)
			}
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Switching context to promotionstrategy")
		Eventually(func(g Gomega) {
			var latest promoterv1alpha1.WebRequestCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &latest)
			g.Expect(err).NotTo(HaveOccurred())
			latest.Spec.Mode.Context = promoterv1alpha1.ContextPromotionStrategy
			g.Expect(k8sClient.Update(ctx, &latest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Waiting for promotionstrategy context status to be populated and environments to be cleared")
		Eventually(func(g Gomega) {
			var fetched promoterv1alpha1.WebRequestCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil(), "PromotionStrategyContext should be populated")
			g.Expect(fetched.Status.Environments).To(BeEmpty(), "Environments should be cleared after switching to promotionstrategy context")

			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch[branch]).To(
					Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
				csName := utils.KubeSafeUniqueName(ctx, wrcs.Name+"-"+branch+"-webrequest")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred(), "CommitStatus for %s should exist", branch)
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "CommitStatus for %s should be success", branch)
			}
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Switching context back to environments (default)")
		Eventually(func(g Gomega) {
			var latest promoterv1alpha1.WebRequestCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &latest)
			g.Expect(err).NotTo(HaveOccurred())
			latest.Spec.Mode.Context = ""
			g.Expect(k8sClient.Update(ctx, &latest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Waiting for environments status to be repopulated and promotionstrategy context to be cleared")
		Eventually(func(g Gomega) {
			var fetched promoterv1alpha1.WebRequestCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(fetched.Status.PromotionStrategyContext).To(BeNil(), "PromotionStrategyContext should be nil after switching back to environments context")
			g.Expect(len(fetched.Status.Environments)).To(Equal(3), "all environments should be repopulated after switching back")

			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				var envSt *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range fetched.Status.Environments {
					if fetched.Status.Environments[i].Branch == branch {
						envSt = &fetched.Status.Environments[i]
						break
					}
				}
				g.Expect(envSt).NotTo(BeNil(), "environment status should exist for branch %s after switching back", branch)
				g.Expect(envSt.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "branch %s should succeed after switching back", branch)
			}
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})
