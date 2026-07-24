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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

//go:embed testdata/WebRequestCommitStatus.yaml
var testWebRequestCommitStatusYAML string

//go:embed testdata/WebRequestCommitStatus_promotionstrategy_ctx.yaml
var testWebRequestCommitStatusPromotionStrategyCtxYAML string

const (
	// httpRequestCountStableWindow is how long the HTTP request count must
	// hold steady before expectHTTPRequestCountStabilizes declares the
	// controller has stopped firing HTTP. Long enough to span at least one
	// trigger-mode requeue interval (the typical RequeueDuration in WRCS
	// specs is 5s; the longest is 10s) so a regression that fires HTTP on
	// every requeue resets the timer at least once and the assertion fails.
	// Short enough to keep test runtime in check.
	httpRequestCountStableWindow = 6 * time.Second
	// httpRequestCountStableBudget caps how long expectHTTPRequestCountStabilizes
	// is willing to wait for the count to stabilize before failing. A
	// regression that keeps re-firing HTTP on every requeue cycle never
	// satisfies the stable-window check; the budget is the maximum time such
	// a regression can hide before the test fails.
	httpRequestCountStableBudget = 15 * time.Second
)

// expectHTTPRequestCountStabilizes asserts the HTTP-request counter eventually
// stops growing for at least httpRequestCountStableWindow. The timer resets
// whenever the counter increases, so a few stragglers (cache lag, SSA retry,
// etc.) don't fail the test as long as the controller ultimately converges to
// no further HTTP. Use this in place of strict
// `Consistently(... Equal(snapshot))` whenever the bug being guarded against
// is "controller never stops firing HTTP", not "controller fires exactly N
// times".
//
// Why we don't assert exact counts: WebRequestCommitStatus dedupes HTTP via
// state persisted in `status` on the previous reconcile (TriggerOutput,
// ResponseOutput, LastRequestTime, LastSuccessfulShas). That state is written
// via Server-Side Apply at the end of a reconcile and read from the
// controller's informer cache at the start of the next reconcile, so the
// controller offers AT-LEAST-ONCE HTTP delivery, not exactly-once — see the
// "Delivery semantics" section in docs/gating-promotions/built-in-gates/web-request-commit-status/index.md
// and the godoc on TriggerModeSpec. A correct controller still converges to no
// further HTTP under steady inputs; a regression where dedup is broken keeps
// firing on every requeue and never stabilizes.
//
// The helper acquires the mutex around each sample so it works with the
// shared counter pattern used throughout this file:
//
//	mu.Lock(); n := count; mu.Unlock()
//
// It returns the final observed count so callers can use it for further
// assertions (e.g. "should be close to the snapshot").
func expectHTTPRequestCountStabilizes(mu *sync.Mutex, count *int, failureContext string) int {
	GinkgoHelper()
	mu.Lock()
	last := *count
	mu.Unlock()
	stableSince := time.Now()

	Eventually(func(g Gomega) {
		mu.Lock()
		c := *count
		mu.Unlock()

		if c != last {
			last = c
			stableSince = time.Now()
		}

		g.Expect(time.Since(stableSince)).To(BeNumerically(">=", httpRequestCountStableWindow),
			"HTTP request count must stop growing under sustained reconciliation. "+
				"Last growth was %v ago; current count %d. %s "+
				"A correct controller dedupes via persisted status and may emit a few "+
				"stragglers under transient cache lag, but must converge to no further "+
				"HTTP. Continuous growth indicates dedup is broken (e.g. controller "+
				"reads the resource from a cache that is consistently stale relative "+
				"to the prior reconcile's status SSA).",
			time.Since(stableSince), c, failureContext)
	}, httpRequestCountStableBudget, 100*time.Millisecond).Should(Succeed())

	mu.Lock()
	final := *count
	mu.Unlock()
	return final
}

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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
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
						URLTemplate:    testServer.URL + `/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
				commitStatusName := utils.CommitStatusResourceName(ctx, &wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying a CommitStatusPhaseChanged event was emitted")
			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				g.Expect(hasEventWithReasonAndMessage(eventList, name+"-polling-success", constants.CommitStatusPhaseChangedReason, "to success")).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Polling Mode - Unreachable URL", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			By("Creating a test HTTP server and closing it immediately so the URL refuses connections")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			unreachableURL := testServer.URL
			testServer.Close()
			testServer = nil

			By("Creating a WebRequestCommitStatus resource pointing at the unreachable URL")
			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-unreachable-url",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:      "external-approval",
					ReportOn: constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    unreachableURL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 5 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil && Response.StatusCode == 200`,
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
			_ = k8sClient.Delete(ctx, webRequestCommitStatus)
		})

		It("should emit a WebRequestFailed event when the HTTP request cannot be made", func() {
			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				g.Expect(hasEventWithReason(eventList, name+"-unreachable-url", constants.WebRequestFailedReason)).To(BeTrue())
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
				commitStatusName := utils.CommitStatusResourceName(ctx, &wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should only update lastSuccessfulSha when validation succeeds", func() {
			By("Creating a test HTTP server that starts failing then succeeds")
			var lssRequestCount int
			var lssMu sync.Mutex
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lssMu.Lock()
				lssRequestCount++
				count := lssRequestCount
				lssMu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				// With 3 environments, the first round produces 3 requests. Use a threshold
				// of 6 so that at least 2 full rounds (6 requests) stay pending before any
				// environment sees approval. This avoids flakiness from a global counter.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": count > 6,
					"status":   map[bool]string{true: "approved", false: "pending"}[count > 6],
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
						URLTemplate:    testServer.URL,
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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

	Describe("Polling Mode - Interval Short-Circuit", func() {
		var (
			shortCircuitWRCS *promoterv1alpha1.WebRequestCommitStatus
			scRequestCount   int
			scRequestMu      sync.Mutex
		)

		BeforeEach(func() {
			scRequestCount = 0
			By("Creating a test HTTP server that counts requests and never returns approved (so we hit interval short-circuit path, not 'already successful SHA')")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				scRequestMu.Lock()
				scRequestCount++
				scRequestMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": false,
					"status":   "pending",
				})
			}))

			By("Creating a WebRequestCommitStatus in polling mode with long interval")
			shortCircuitWRCS = &promoterv1alpha1.WebRequestCommitStatus{
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
						URLTemplate:    testServer.URL + `/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 10 * time.Minute},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, shortCircuitWRCS)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
			_ = k8sClient.Delete(ctx, shortCircuitWRCS)
		})

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
				g.Expect(len(wrcs.Status.Environments)).To(Equal(3), "all three environments must be processed before snapshotting the request count")
				for i := range wrcs.Status.Environments {
					g.Expect(wrcs.Status.Environments[i].LastRequestTime).ToNot(BeNil(), "LastRequestTime should be set after first request")
					g.Expect(wrcs.Status.Environments[i].Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "validation fails (approved: false) so phase stays Pending")
				}
				scRequestMu.Lock()
				initialRequestCount = scRequestCount
				scRequestMu.Unlock()
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

			By("Success: HTTP request count stabilizes — controller must not hit the server on subsequent reconciles within interval")
			// Tolerate a few stragglers (e.g. transient cache lag) but
			// require the controller to converge to no further HTTP. The
			// regression we guard against is "controller never stops
			// firing", not "controller fires exactly initialRequestCount
			// times".
			expectHTTPRequestCountStabilizes(&scRequestMu, &scRequestCount,
				"WebRequestCommitStatus polling-interval short-circuit must skip HTTP within the polling interval.")
		})
	})

	Describe("Trigger Mode - SHA Change Detection", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var triggerRequestCount int
		var triggerMu sync.Mutex

		BeforeEach(func() {
			triggerRequestCount = 0

			By("Creating a test HTTP server that counts requests")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				triggerMu.Lock()
				triggerRequestCount++
				triggerMu.Unlock()
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: `find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["trackedSha"] ?? "")`,
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }`},
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
				triggerMu.Lock()
				c := triggerRequestCount
				triggerMu.Unlock()
				g.Expect(c).To(BeNumerically(">=", 1), "Should have made at least one HTTP request")

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

			By("Verifying the HTTP request count stabilizes once each env has tracked its SHA")
			expectHTTPRequestCountStabilizes(&triggerMu, &triggerRequestCount,
				"WebRequestCommitStatus 'Trigger Mode - SHA Change Detection' must stop firing HTTP for the same SHA.")
		})
	})

	Describe("Trigger mode — when.variables (Variables)", func() {
		var webRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
		var triggerMu sync.Mutex
		var triggerRequestCount int

		BeforeEach(func() {
			triggerRequestCount = 0
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				triggerMu.Lock()
				triggerRequestCount++
				triggerMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			webRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-trigger-when-variables",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true && Variables.approvedGate) : Phase == "success"`,
							Variables: &promoterv1alpha1.OutputSpec{
								Expression: `{ approvedGate: true }`,
							},
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Variables: &promoterv1alpha1.OutputSpec{
									Expression: `{ currentSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }`,
								},
								Expression: `Variables.currentSha != (TriggerOutput["trackedSha"] ?? "")`,
								Output: &promoterv1alpha1.OutputSpec{
									Expression: `{ trackedSha: Variables.currentSha, echoedLen: len(string(Variables.currentSha)) }`,
								},
							},
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

		It("should evaluate variables once and expose Variables to trigger when and output expressions", func() {
			Eventually(func(g Gomega) {
				triggerMu.Lock()
				c := triggerRequestCount
				triggerMu.Unlock()
				g.Expect(c).To(BeNumerically(">=", 1))

				var wrcs promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-trigger-when-variables",
					Namespace: "default",
				}, &wrcs)).To(Succeed())

				var dev *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
				for i := range wrcs.Status.Environments {
					if wrcs.Status.Environments[i].Branch == testBranchDevelopment {
						dev = &wrcs.Status.Environments[i]
						break
					}
				}
				g.Expect(dev).ToNot(BeNil())
				g.Expect(dev.TriggerOutput).ToNot(BeNil())
				var trig map[string]any
				g.Expect(json.Unmarshal(dev.TriggerOutput.Raw, &trig)).To(Succeed())
				g.Expect(trig).To(HaveKey("trackedSha"))
				g.Expect(trig).To(HaveKey("echoedLen"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should set Ready=False when when.variables expression does not return a map", func() {
			bad := webRequestCommitStatus.DeepCopy()
			bad.Name = name + "-trigger-when-variables-bad"
			bad.UID = ""
			bad.ResourceVersion = ""
			bad.Generation = 0
			bad.CreationTimestamp = metav1.Time{}
			bad.ManagedFields = nil
			bad.Status = promoterv1alpha1.WebRequestCommitStatusStatus{}
			bad.Spec.Mode.Trigger.When.Variables = &promoterv1alpha1.OutputSpec{
				Expression: `true`,
			}
			bad.Spec.Mode.Trigger.When.Expression = `true`
			bad.Spec.Mode.Trigger.When.Output = nil
			Expect(k8sClient.Create(ctx, bad)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, bad) }()

			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-trigger-when-variables-bad",
					Namespace: "default",
				}, &fetched)).To(Succeed())
				var ready *metav1.Condition
				for i := range fetched.Status.Conditions {
					if fetched.Status.Conditions[i].Type == "Ready" {
						ready = &fetched.Status.Conditions[i]
						break
					}
				}
				g.Expect(ready).ToNot(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(Or(
					ContainSubstring("when.variables expression must return"),
					ContainSubstring("failed to evaluate trigger.when.variables"),
				))
			}, constants.EventuallyTimeout).Should(Succeed())
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
					DescriptionTemplate: "Checking {{ .Branch }} ({{ .Phase }})",
					UrlTemplate:         `https://example.com/status/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + `/validate/{{ .Branch }}?sha={{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
						MethodTemplate: "POST",
						HeaderTemplates: map[string]string{
							"X-Branch": "{{ .Branch }}",
							"X-Sha":    `{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
						},
						BodyTemplate: `{"branch": "{{ .Branch }}", "sha": "{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}"}`,
						Timeout:      metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
				g.Expect(devReq.headers.Get("X-Branch")).To(Equal(testBranchDevelopment))
				g.Expect(devReq.headers.Get("X-Sha")).ToNot(BeEmpty(), "SHA header should be rendered from PromotionStrategy lookup")

				// Body should contain rendered values
				g.Expect(devReq.body).To(ContainSubstring(testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus has rendered description and URL")
			Eventually(func(g Gomega) {
				commitStatusName := utils.CommitStatusResourceName(ctx, webRequestCommitStatus, testBranchDevelopment)
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

		It("should render trigger.when.variables result into httpRequest URL, headers, and body templates", func() {
			By("Creating a WRCS in trigger mode with trigger.when.variables referenced in httpRequest templates")
			var triggerVarRequestMu sync.Mutex
			var triggerVarRequests []struct {
				url     string
				headers http.Header
				body    string
			}
			triggerVarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyBytes := make([]byte, 1024)
				n, _ := r.Body.Read(bodyBytes)
				triggerVarRequestMu.Lock()
				triggerVarRequests = append(triggerVarRequests, struct {
					url     string
					headers http.Header
					body    string
				}{url: r.URL.String(), headers: r.Header.Clone(), body: string(bodyBytes[:n])})
				triggerVarRequestMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))
			DeferCleanup(triggerVarServer.Close)

			wrcs := &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-trigger-vars-http-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate: triggerVarServer.URL + `/validate/{{ index .TriggerVariables "env" }}`,
						Method:      "POST",
						HeaderTemplates: map[string]string{
							"X-Env": `{{ index .TriggerVariables "env" }}`,
						},
						BodyTemplate: `{"env":"{{ index .TriggerVariables "env" }}"}`,
						Timeout:      metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 30 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Variables:  &promoterv1alpha1.OutputSpec{Expression: `{"env": "prod"}`},
								Expression: `true`,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, wrcs) })

			By("Waiting for the HTTP request with TriggerVariables rendered in URL, header, and body")
			Eventually(func(g Gomega) {
				triggerVarRequestMu.Lock()
				reqs := append([]struct {
					url     string
					headers http.Header
					body    string
				}(nil), triggerVarRequests...)
				triggerVarRequestMu.Unlock()

				g.Expect(reqs).ToNot(BeEmpty())
				req := reqs[0]
				g.Expect(req.url).To(ContainSubstring("/validate/prod"))
				g.Expect(req.headers.Get("X-Env")).To(Equal("prod"))
				g.Expect(req.body).To(ContainSubstring(`"env":"prod"`))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should expose success.when.variables result as .SuccessVariables in description template", func() {
			By("Creating a WRCS with success.when.variables and a description template referencing .SuccessVariables")
			wrcsVars := &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-success-vars-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					DescriptionTemplate:  `Build: {{ index .SuccessVariables "tag" }}`,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Variables:  &promoterv1alpha1.OutputSpec{Expression: `{"tag": "v1.2.3"}`},
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{
							Interval: metav1.Duration{Duration: 30 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcsVars)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, wrcsVars) })

			By("Waiting for CommitStatus description to contain the variable value")
			Eventually(func(g Gomega) {
				commitStatusName := utils.CommitStatusResourceName(ctx, wrcsVars, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: commitStatusName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Description).To(Equal("Build: v1.2.3"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should expose trigger.when.variables result as .TriggerVariables in description template", func() {
			By("Creating a WRCS in trigger mode with trigger.when.variables and a description template referencing .TriggerVariables")
			wrcsTriggerVars := &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-trigger-vars-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					DescriptionTemplate:  `Run: {{ index .TriggerVariables "runId" }}`,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 30 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Variables:  &promoterv1alpha1.OutputSpec{Expression: `{"runId": "42"}`},
								Expression: `true`,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, wrcsTriggerVars)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, wrcsTriggerVars) })

			By("Waiting for CommitStatus description to contain the trigger variable value")
			Eventually(func(g Gomega) {
				commitStatusName := utils.CommitStatusResourceName(ctx, wrcsTriggerVars, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: commitStatusName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Description).To(Equal("Run: 42"))
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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

				devCommitStatusName = utils.CommitStatusResourceName(ctx, &wrcs, testBranchDevelopment)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
				count := requestCount
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"count":    count,
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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

				mu.Lock()
				c := requestCount
				mu.Unlock()
				g.Expect(c).To(Equal(1), "Should only make one request")
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.status == "done") : Phase == "success"`,
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

				mu.Lock()
				c := requestCount
				mu.Unlock()
				g.Expect(c).To(BeNumerically(">=", 3))

				// Verify final response data shows "done"
				g.Expect(devEnv.ResponseOutput).NotTo(BeNil())
				var responseData map[string]any
				err = json.Unmarshal(devEnv.ResponseOutput.Raw, &responseData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(responseData["status"]).To(Equal("done"))
			}, constants.EventuallyTimeout).Should(Succeed())
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
			declarePreviousEnvironmentGate(scmAuthPromotionStrategy)
			Expect(k8sClient.Create(scmAuthCtx, scmAuthPromotionStrategy)).To(Succeed())
			createPreviousEnvironmentCommitStatus(scmAuthCtx, scmAuthPromotionStrategy)

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
						URLTemplate:    scmAuthTestServer.URL + `/check/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Active.Hydrated.Sha }}{{ end }}{{ end }}`,
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.ok == true) : Phase == "success"`,
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
						URLTemplate:    "http://example.com/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
			declarePreviousEnvironmentGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
			declarePreviousEnvironmentGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)

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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Scm: &promoterv1alpha1.Scm{},
						},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
					csName := utils.CommitStatusResourceName(ctx, wrcs, branch)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhasePending), "PhasePerBranch for %s should be pending", branch)
					csName := utils.CommitStatusResourceName(ctx, wrcs, branch)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
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
				g.Expect(fetched.Status.PromotionStrategyContext.PhasePerBranch).NotTo(BeEmpty(),
					"PhasePerBranch should be populated")
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchDevelopment)).To(
					Equal(promoterv1alpha1.CommitPhaseSuccess), "dev should be success")
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchStaging)).To(
					Equal(promoterv1alpha1.CommitPhasePending), "staging should be pending")
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchProduction)).To(
					Equal(promoterv1alpha1.CommitPhasePending), "production should be pending")

				devCSName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var devCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: devCSName, Namespace: "default"}, &devCS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(devCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

				stagingCSName := utils.CommitStatusResourceName(ctx, wrcs, testBranchStaging)
				var stagingCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: stagingCSName, Namespace: "default"}, &stagingCS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(stagingCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))

				prodCSName := utils.CommitStatusResourceName(ctx, wrcs, testBranchProduction)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
					csName := utils.CommitStatusResourceName(ctx, wrcs, branch)
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
				}
				g.Expect(fetched.Status.PromotionStrategyContext.LastSuccessfulShas).NotTo(BeEmpty(),
					"LastSuccessfulShas should be populated after success")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Sanity-checking the initial request count is small (no per-env fan-out)")
			requestMu.Lock()
			initialCount := requestCount
			requestMu.Unlock()
			Expect(initialCount).To(BeNumerically(">=", 1), "at least one HTTP request should have been made")
			Expect(initialCount).To(BeNumerically("<=", 4),
				"context=promotionstrategy should not fan out to per-environment HTTP calls; a few reconciles may occur before success")

			By("Verifying the HTTP request count stabilizes across polling intervals (skip optimization)")
			expectHTTPRequestCountStabilizes(&requestMu, &requestCount,
				"WebRequestCommitStatus context=promotionstrategy must skip HTTP after all environments succeed.")
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
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhasePending), "PhasePerBranch for %s should be pending", branch)
				}
				g.Expect(fetched.Status.PromotionStrategyContext.LastResponseStatusCode).NotTo(BeNil())
				g.Expect(*fetched.Status.PromotionStrategyContext.LastResponseStatusCode).To(Equal(500))

				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					csName := utils.CommitStatusResourceName(ctx, wrcs, branch)
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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)

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
					URLTemplate:    testServer.URL + "/check",
					MethodTemplate: "GET",
					Timeout:        metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
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
				"promotionstrategy-context reconcile clears promotionStrategyContext when applicableEnvs is empty")
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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)

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
					URLTemplate:    testServer.URL + "/validate",
					MethodTemplate: "GET",
					Timeout:        metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
			g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchDevelopment)).To(
				Equal(promoterv1alpha1.CommitPhaseSuccess))

			csName := utils.CommitStatusResourceName(ctx, webRequestCS, testBranchDevelopment)
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
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)

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
					URLTemplate:    testServer.URL + "/validate",
					MethodTemplate: "GET",
					Timeout:        metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
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
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
					Equal(promoterv1alpha1.CommitPhaseSuccess), "PhasePerBranch for %s should be success", branch)
				csName := utils.CommitStatusResourceName(ctx, wrcs, branch)
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

var _ = Describe("WebRequestCommitStatus Controller - Success.when Every Reconcile", Ordered, func() {
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
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-sw-test", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "external-approval"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
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

	Describe("Enriched context (environments context)", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var httpRequestCount int
		var httpCountMu sync.Mutex

		BeforeEach(func() {
			httpRequestCount = 0
			By("Creating a test HTTP server that counts requests (trigger never fires, so count must stay 0)")
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpCountMu.Lock()
				httpRequestCount++
				httpCountMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			By("Creating a WRCS that uses PromotionStrategy in success.when (no Response dependency)")
			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sw-enriched",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							// Uses PromotionStrategy and Environment instead of Response.
							// This should evaluate to success on every reconcile (even without a request)
							// because PromotionStrategy is always non-nil.
							Expression: `PromotionStrategy != nil && PromotionStrategy.Name != ""`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								// Never fire the HTTP request
								Expression: "false",
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

		It("should reach success phase using PromotionStrategy context even without an HTTP request", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(len(fetched.Status.Environments)).To(BeNumerically(">=", 1))

				for _, env := range fetched.Status.Environments {
					if env.Branch == testBranchDevelopment {
						g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
							"Phase should be success from PromotionStrategy context, no HTTP request needed")
						// LastRequestTime should be nil since no HTTP request was made
						g.Expect(env.LastRequestTime).To(BeNil(),
							"LastRequestTime should be nil since trigger never fired")
					}
				}

				// Verify CommitStatus was created with success phase
				csName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			httpCountMu.Lock()
			n := httpRequestCount
			httpCountMu.Unlock()
			Expect(n).To(Equal(0), "no HTTP request should occur when trigger is always false")
		})
	})

	Describe("Enriched context (promotionstrategy context)", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var httpRequestCount int
		var httpCountMu sync.Mutex

		BeforeEach(func() {
			httpRequestCount = 0
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpCountMu.Lock()
				httpRequestCount++
				httpCountMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sw-enriched-ps",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							// Uses PromotionStrategy in promotionstrategy context.
							// Response is nil (trigger never fires), but PromotionStrategy is always available.
							Expression: `PromotionStrategy != nil`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "false",
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

		It("should reach success phase using PromotionStrategy in promotionstrategy context without HTTP request", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil(),
					"PromotionStrategyContext should be populated")
				g.Expect(fetched.Status.PromotionStrategyContext.LastRequestTime).To(BeNil(),
					"LastRequestTime should be nil since trigger never fired")

				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, branch)).To(
						Equal(promoterv1alpha1.CommitPhaseSuccess),
						"PhasePerBranch for %s should be success from PromotionStrategy context", branch)
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			httpCountMu.Lock()
			n := httpRequestCount
			httpCountMu.Unlock()
			Expect(n).To(Equal(0), "no HTTP request should occur when trigger is always false (promotionstrategy context)")
		})
	})

	Describe("Response guard pattern", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var requestCount int
		var requestCountMu sync.Mutex

		BeforeEach(func() {
			requestCount = 0
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCountMu.Lock()
				requestCount++
				requestCountMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sw-guard",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							// Guard pattern: check Response when available, fall back to Phase when not.
							// After first request succeeds, Phase carries "success" and subsequent
							// reconciles (no request) use the fallback branch.
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: `find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["trackedSha"] ?? "")`,
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }`},
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

		It("should succeed via Response on first reconcile, then stay success via Phase fallback without extra HTTP", func() {
			By("Waiting for ALL environments to complete their first HTTP wave before snapshotting request count")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(Equal(3),
					"all three environments must be processed before snapshotting the HTTP request count")
				for _, env := range fetched.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
						"environment %s should be success before snapshotting", env.Branch)
				}

				requestCountMu.Lock()
				c := requestCount
				requestCountMu.Unlock()
				g.Expect(c).To(BeNumerically(">=", 3),
					"each environment fires once on first SHA track")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the HTTP request count stabilizes once trigger stays false and phase stays success")
			// Tolerate a few stragglers (e.g. transient cache lag between
			// the prior reconcile's status SSA and the controller's informer
			// observing it) but require the controller to converge to no
			// further HTTP. The CI failure that motivated this assertion
			// shows continuous growth — that's the regression we guard
			// against, not the literal initial count.
			expectHTTPRequestCountStabilizes(&requestCountMu, &requestCount,
				"WebRequestCommitStatus 'Response guard pattern' must stop firing HTTP once every environment has tracked its SHA.")

			By("Phase should stay success via Phase==success fallback after the count stabilizes")
			var fetched promoterv1alpha1.WebRequestCommitStatus
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)).To(Succeed())
			for _, env := range fetched.Status.Environments {
				if env.Branch == testBranchDevelopment {
					Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
						"Phase should stay success via Phase==success fallback")
				}
			}
		})
	})

	Describe("HTTP response expiry via ResponseOutput and now()", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var httpHits int
		var httpHitsMu sync.Mutex

		BeforeEach(func() {
			httpHits = 0
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpHitsMu.Lock()
				httpHits++
				httpHitsMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				validUntil := float64(time.Now().Add(15 * time.Second).Unix())
				_ = json.NewEncoder(w).Encode(map[string]any{"validUntilUnix": validUntil})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sw-expiry",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "external-approval",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && float(now().Unix()) < Response.Body.validUntilUnix) : (ResponseOutput != nil && float(now().Unix()) < ResponseOutput["validUntilUnix"])`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 2 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: `find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["trackedSha"] ?? "")`,
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }`},
							},
							Response: &promoterv1alpha1.ResponseOutputSpec{
								Output: promoterv1alpha1.OutputSpec{
									Expression: `{ validUntilUnix: Response.Body.validUntilUnix }`,
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

		It("should report success until expiry passes then revert to pending without new HTTP", func() {
			var hitsAfterFirstWave int

			By("Waiting for ALL environments to complete their first HTTP wave before snapshotting hit count")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(Equal(3),
					"all three environments must be processed before snapshotting the HTTP hit count")
				for _, env := range fetched.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
						"environment %s should be success before snapshotting", env.Branch)
					g.Expect(env.ResponseOutput).NotTo(BeNil(),
						"environment %s should have ResponseOutput before snapshotting", env.Branch)
				}

				httpHitsMu.Lock()
				h := httpHits
				httpHitsMu.Unlock()
				g.Expect(h).To(BeNumerically(">=", 3), "each environment fires once on first SHA track")

				csName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout, 500*time.Millisecond).Should(Succeed())

			By("Waiting for the HTTP hit count to stabilize once each env has tracked its SHA")
			// Tolerate a few stragglers (cache lag, write retries) but
			// require convergence to no further HTTP.
			finalHits := expectHTTPRequestCountStabilizes(&httpHitsMu, &httpHits,
				"WebRequestCommitStatus 'HTTP response expiry' must stop firing HTTP once each env has tracked its SHA.")
			Expect(finalHits).To(BeNumerically(">=", hitsAfterFirstWave),
				"sanity check: hits never decrease (initial=%d, final=%d)", hitsAfterFirstWave, finalHits)

			By("Waiting for success.when to flip to pending after now().Unix() passes validUntilUnix")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				for _, env := range fetched.Status.Environments {
					if env.Branch == testBranchDevelopment {
						g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
							"phase should return to pending after expiry")
					}
				}

				csName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout, 1*time.Second).Should(Succeed())
		})
	})
})

var _ = Describe("WebRequestCommitStatus Controller - SuccessOutput", Ordered, func() {
	var (
		name              string
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		gitRepo           *promoterv1alpha1.GitRepository
		scmProvider       *promoterv1alpha1.ScmProvider
		scmSecret         *corev1.Secret
		testServer        *httptest.Server
	)

	const (
		testBranchDevelopment = "environment/development"
		testBranchStaging     = "environment/staging"
		testBranchProduction  = "environment/production"
	)

	ctx := context.Background()

	BeforeAll(func() {
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "webrequest-successoutput", "default")

		promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
			{Branch: testBranchStaging},
			{Branch: testBranchProduction},
		}

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "success-output-test"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
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

	Describe("Environments Context - success.when.output stores data in status", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
					"approver": "jane",
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-env-success-out",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "success-output-test",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
							Output:     &promoterv1alpha1.OutputSpec{Expression: `{approver: Response != nil ? Response.Body.approver : SuccessOutput["approver"], evaluatedAt: "2025-01-01"}`},
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
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

		It("should populate successOutput in each environment status", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(BeNumerically(">=", 1))

				var foundEnvWithSuccessOutput bool
				for _, envStatus := range fetched.Status.Environments {
					if envStatus.Phase == promoterv1alpha1.CommitPhaseSuccess && envStatus.SuccessOutput != nil {
						var successData map[string]any
						err := json.Unmarshal(envStatus.SuccessOutput.Raw, &successData)
						g.Expect(err).NotTo(HaveOccurred())
						if approver, ok := successData["approver"].(string); ok && approver == "jane" {
							g.Expect(successData["evaluatedAt"]).To(Equal("2025-01-01"))
							foundEnvWithSuccessOutput = true
						}
					}
				}
				g.Expect(foundEnvWithSuccessOutput).To(BeTrue(),
					"At least one environment should have successOutput with approver=jane")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Environments Context - no success.when.output leaves successOutput nil", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-env-no-success-out",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "success-output-test",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
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

		It("should NOT populate successOutput when no output expression is configured", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(BeNumerically(">=", 1))

				for _, envStatus := range fetched.Status.Environments {
					if envStatus.Phase == promoterv1alpha1.CommitPhaseSuccess {
						g.Expect(envStatus.SuccessOutput).To(BeNil(),
							"successOutput should be nil when no output expression is configured for branch %s", envStatus.Branch)
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("PromotionStrategy Context - success.when.output stores data in promotionStrategyContext", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus

		BeforeEach(func() {
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved":   true,
					"approvedBy": "deploy-bot",
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctx-ps-success-out",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "success-output-test",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
							Output:     &promoterv1alpha1.OutputSpec{Expression: `{approvedBy: Response != nil ? Response.Body.approvedBy : SuccessOutput["approvedBy"]}`},
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: "true",
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

		It("should populate successOutput in promotionStrategyContext status", func() {
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				g.Expect(fetched.Status.PromotionStrategyContext.SuccessOutput).NotTo(BeNil(),
					"SuccessOutput should be populated in promotionstrategy context")

				var successData map[string]any
				err = json.Unmarshal(fetched.Status.PromotionStrategyContext.SuccessOutput.Raw, &successData)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(successData["approvedBy"]).To(Equal("deploy-bot"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("SuccessOutput available in trigger.when expression", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var requestCount int
		var mu sync.Mutex

		BeforeEach(func() {
			requestCount = 0
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requestCount++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": true,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-success-out-in-trigger",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "success-output-test",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
							Output:     &promoterv1alpha1.OutputSpec{Expression: `{checkedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha}`},
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 2 * time.Second},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: `SuccessOutput == nil || SuccessOutput["checkedSha"] != find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha`,
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

		It("should stop triggering once SuccessOutput records the SHA", func() {
			By("Waiting for first HTTP request and SuccessOutput to be stored")
			Eventually(func(g Gomega) {
				mu.Lock()
				count := requestCount
				mu.Unlock()
				g.Expect(count).To(BeNumerically(">=", 1), "Should have made at least one HTTP request")

				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(BeNumerically(">=", 1))

				var foundWithOutput bool
				for _, envStatus := range fetched.Status.Environments {
					if envStatus.SuccessOutput != nil {
						var data map[string]any
						if err := json.Unmarshal(envStatus.SuccessOutput.Raw, &data); err == nil {
							if sha, ok := data["checkedSha"].(string); ok && sha != "" {
								foundWithOutput = true
							}
						}
					}
				}
				g.Expect(foundWithOutput).To(BeTrue(), "Should have SuccessOutput with checkedSha")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the HTTP request count stabilizes once SuccessOutput has the SHA")
			expectHTTPRequestCountStabilizes(&mu, &requestCount,
				"WebRequestCommitStatus 'SuccessOutput' must stop firing HTTP once SuccessOutput has the SHA.")
		})
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Dry SHA Guard", Ordered, func() {
	var (
		name              string
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		gitRepo           *promoterv1alpha1.GitRepository
		scmProvider       *promoterv1alpha1.ScmProvider
		scmSecret         *corev1.Secret
		testServer        *httptest.Server
	)

	const (
		testBranchDevelopment = "environment/development"
	)

	ctx := context.Background()

	BeforeAll(func() {
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-dry-sha-guard", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "dry-sha-guard"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
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

	Describe("Trigger Mode - resets to pending when new commit arrives and is not yet approved", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var approved sync.Map

		BeforeEach(func() {
			approved.Store("value", true)

			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				v, _ := approved.Load("value")
				isApproved, ok := v.(bool)
				if !ok {
					isApproved = false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": isApproved,
				})
			}))

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-dry-sha-guard",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "dry-sha-guard",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil` +
								` ? (Response.StatusCode == 200 && Response.Body.approved == true)` +
								` : (Phase == "success" && (let wrcsEnv = find(WebRequestCommitStatus.Status.Environments ?? [], {.Branch == Branch}); wrcsEnv != nil && find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha == wrcsEnv.LastSuccessfulSha))`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Minute},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: `find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha != (TriggerOutput["lastCheckedSha"] ?? "") || Phase != "success"`,
								Output:     &promoterv1alpha1.OutputSpec{Expression: `{ lastCheckedSha: find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha }`},
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

		It("should go to pending for the new SHA when the external system has not approved it yet", func() {
			By("Pushing a hydrated commit and waiting for WRCS to reach success")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			firstDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "first commit for dry-sha-guard", "")

			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(fetched.Status.Environments)).To(BeNumerically(">=", 1))

				for _, env := range fetched.Status.Environments {
					if env.Branch == testBranchDevelopment {
						g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Switching the external system to not-approved (simulating new content not yet validated)")
			approved.Store("value", false)

			By("Pushing a second hydrated commit with a new dry SHA")
			secondDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "second commit for dry-sha-guard", "")
			Expect(secondDrySha).NotTo(Equal(firstDrySha))

			By("Verifying WRCS resets to pending for development")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())

				for _, env := range fetched.Status.Environments {
					if env.Branch == testBranchDevelopment {
						g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
							"development should be pending after new commit when external system returns not-approved")
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the CommitStatus resource is also pending")
			Eventually(func(g Gomega) {
				csName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("WebRequestCommitStatus Controller - Dry SHA Guard (PromotionStrategy Context)", Ordered, func() {
	var (
		name              string
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		gitRepo           *promoterv1alpha1.GitRepository
		scmProvider       *promoterv1alpha1.ScmProvider
		scmSecret         *corev1.Secret
		testServer        *httptest.Server
	)

	const (
		testBranchDevelopment = "environment/development"
		testBranchStaging     = "environment/staging"
		testBranchProduction  = "environment/production"
	)

	ctx := context.Background()

	BeforeAll(func() {
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-drysha-ps-ctx", "default")

		promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
			{Branch: testBranchStaging},
			{Branch: testBranchProduction},
		}

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "dry-sha-ps-guard"},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		declarePreviousEnvironmentGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
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

	Describe("Per-branch phases with dry SHA guard", func() {
		var wrcs *promoterv1alpha1.WebRequestCommitStatus
		var approved sync.Map

		BeforeEach(func() {
			approved.Store("value", true)

			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				v, _ := approved.Load("value")
				isApproved, ok := v.(bool)
				if !ok {
					isApproved = false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approved": isApproved,
				})
			}))

			successExpr := `Response != nil` +
				`  ? (Response.StatusCode == 200 && Response.Body.approved == true` +
				`      ? { defaultPhase: "success" }` +
				`      : { defaultPhase: "pending" })` +
				`  : {` +
				`      defaultPhase: "pending",` +
				`      environments: map(PromotionStrategy.Status.Environments, {` +
				`        let env = #;` +
				`        let lastSha = find(` +
				`          WebRequestCommitStatus.Status.PromotionStrategyContext.LastSuccessfulShas ?? [],` +
				`          {.Branch == env.Branch}` +
				`        );` +
				`        {` +
				`          branch: env.Branch,` +
				`          phase: lastSha != nil && lastSha.LastSuccessfulSha == env.Proposed.Hydrated.Sha` +
				`            ? "success" : "pending"` +
				`        }` +
				`      })` +
				`    }`

			triggerExpr := `Phase != "success" || ` +
				`any(PromotionStrategy.Status.Environments, {` +
				`  let env = #;` +
				`  let lastSha = find(` +
				`    WebRequestCommitStatus.Status.PromotionStrategyContext.LastSuccessfulShas ?? [],` +
				`    {.Branch == env.Branch}` +
				`  );` +
				`  lastSha == nil || lastSha.LastSuccessfulSha != env.Proposed.Hydrated.Sha` +
				`})`

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-drysha-ps-guard",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "dry-sha-ps-guard",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: successExpr,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Context: promoterv1alpha1.ContextPromotionStrategy,
						Trigger: &promoterv1alpha1.TriggerModeSpec{
							RequeueDuration: metav1.Duration{Duration: 5 * time.Minute},
							When: promoterv1alpha1.WhenWithOutputSpec{
								Expression: triggerExpr,
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

		It("should reset all branches to pending when new commit arrives and is not yet approved", func() {
			By("Pushing a hydrated commit and waiting for all branches to reach success")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			firstDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "first commit for ps-guard", "")

			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchDevelopment)).
					To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchStaging)).
					To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			_ = firstDrySha

			By("Switching the external system to not-approved")
			approved.Store("value", false)

			By("Pushing a second hydrated commit with new dry SHAs")
			secondDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "second commit for ps-guard", "")
			Expect(secondDrySha).NotTo(Equal(firstDrySha))

			By("Verifying all branches reset to pending")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: wrcs.Name, Namespace: "default"}, &fetched)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetched.Status.PromotionStrategyContext).NotTo(BeNil())

				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchDevelopment)).
					To(Equal(promoterv1alpha1.CommitPhasePending),
						"development should be pending after new commit when external system returns not-approved")
				g.Expect(wrcsPhaseForBranch(fetched.Status.PromotionStrategyContext.PhasePerBranch, testBranchStaging)).
					To(Equal(promoterv1alpha1.CommitPhasePending),
						"staging should be pending after new commit when external system returns not-approved")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus for development is pending")
			Eventually(func(g Gomega) {
				csName := utils.CommitStatusResourceName(ctx, wrcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

func wrcsPhaseForBranch(items []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem, branch string) promoterv1alpha1.CommitStatusPhase {
	for _, it := range items {
		if it.Branch == branch {
			return it.Phase
		}
	}
	return ""
}

// This suite exercises the stale-cache requeue path against a real envtest API
// server. The unit-level semantics of ResourceVersionTracker are covered by
// internal/utils/resourceversion_test.go; this suite proves the wiring inside
// WebRequestCommitStatusReconciler.Reconcile actually short-circuits the
// reconcile (without writing status) when the tracker reports the cached object
// is older than our last write. We seed the tracker with an artificially-future
// RV instead of trying to reproduce real informer-cache lag, which is
// non-deterministic in envtest.
var _ = Describe("WebRequestCommitStatus Controller - Stale Cache Guard", Ordered, func() {
	var (
		ctx  context.Context
		wrcs *promoterv1alpha1.WebRequestCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		// Minimal WRCS spec: the stale-cache guard fires immediately after the
		// initial Get(), before PromotionStrategy/namespace lookups, so a
		// fully-wired strategy isn't needed. We still satisfy the CRD's required
		// fields so Create() succeeds.
		wrcs = &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wrcs-stale-cache-guard",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
				Key:                  "stale-cache-guard",
				ReportOn:             constants.CommitRefProposed,
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate:    "http://127.0.0.1:1/never-called",
					MethodTemplate: "GET",
					Timeout:        metav1.Duration{Duration: 5 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{Duration: 1 * time.Hour},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
	})

	AfterAll(func() {
		if wrcs != nil {
			_ = k8sClient.Delete(ctx, wrcs)
		}
	})

	It("requeues with the short backoff when the tracker reports the cache is stale", func() {
		// Build a standalone reconciler so we can pre-seed its tracker. The
		// stale-cache requeue path exits BEFORE PromotionStrategy/namespace/
		// SettingsMgr/HTTP lookups, so we only need Client + Recorder + rvTracker.
		// (The Scheme/SettingsMgr fields are unused on this path; we set Scheme
		// to k8sClient.Scheme() for safety in case any helper reaches for it.)
		r := &WebRequestCommitStatusReconciler{
			Client:    k8sClient,
			Scheme:    k8sClient.Scheme(),
			Recorder:  events.NewFakeRecorder(10),
			rvTracker: utils.NewResourceVersionTracker(),
		}

		// Seed the tracker with an RV that the API server's current value
		// definitely won't exceed. CompareResourceVersion uses big-integer
		// semantics (longer-length string is greater), so 30 nines is reliably
		// larger than any real RV. This makes IsCacheStale return true for the
		// Get-from-cache the reconciler is about to do, simulating the race
		// where the informer hasn't observed the previous reconcile's write.
		key := types.NamespacedName{Name: wrcs.Name, Namespace: wrcs.Namespace}
		r.rvTracker.Record(key, "999999999999999999999999999999")

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})

		// Load-bearing assertion: a requeue of EXACTLY 100ms with nil error is
		// the unique signature of the stale-cache guard. Any other path returns
		// a longer RequeueAfter (the spec's polling interval / trigger requeue
		// duration) or an error. We can't reliably check "no status write
		// happened" via observed ResourceVersion because the running
		// manager-managed reconciler in this suite is concurrently reconciling
		// the same WRCS and bumps RV out from under us. The return-value check
		// is the deterministic signal that our isolated reconcile took the
		// short-circuit branch.
		Expect(err).NotTo(HaveOccurred(),
			"stale-cache requeue path must return nil error (it's a deferral, not a failure)")
		Expect(result.RequeueAfter).To(Equal(100*time.Millisecond),
			"stale-cache path must requeue with the short backoff so the cache can catch up")
	})

	It("proceeds past the guard and records the post-patch RV when the cache is not stale", func() {
		// Fresh reconciler with an empty tracker — no recorded RV for this key,
		// so IsCacheStale returns false on entry and Reconcile must proceed past
		// the guard. We deliberately reference a non-existent PromotionStrategy
		// so the reconcile fails after the guard, exercising the recording
		// defer on the error path (HandleReconciliationResult still patches the
		// Ready=False condition, which bumps RV, which the tracker records).
		r := &WebRequestCommitStatusReconciler{
			Client:      k8sClient,
			Scheme:      k8sClient.Scheme(),
			Recorder:    events.NewFakeRecorder(10),
			SettingsMgr: settings.NewManager(k8sClient, k8sClient, settings.ManagerConfig{ControllerNamespace: "default"}),
			rvTracker:   utils.NewResourceVersionTracker(),
		}
		key := types.NamespacedName{Name: wrcs.Name, Namespace: wrcs.Namespace}

		// Sanity: empty tracker means no RV is recorded for this key, so
		// IsCacheStale must return false regardless of the cached RV value.
		Expect(r.rvTracker.IsCacheStale(key, "1")).To(BeFalse())

		// Invoke Reconcile. We don't assert on the returned error — the
		// PromotionStrategy lookup fails by design here. What matters is that
		// (a) the call did NOT return our stale-cache requeue signature, proving
		// we got past the guard, and (b) the tracker now has an entry for the
		// key (set by the deferred Record after the status patch).
		result, _ := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(result.RequeueAfter).NotTo(Equal(100*time.Millisecond),
			"on the not-stale path we must NOT take the 100ms short-circuit branch")

		// The post-condition we actually care about for the tracker contract:
		// after this reconcile, the tracker has recorded SOME RV for this key.
		// Test by asking whether an obviously-old RV ("1") is now considered
		// stale. With an empty tracker the answer would be false (nothing
		// recorded); after a successful patch the recorded RV is the real
		// post-patch one which is much larger than "1", so the answer flips to
		// true. We can't pin the exact recorded RV because the manager-managed
		// reconciler in this suite is concurrently reconciling the same WRCS,
		// which can bump RV further between our patch and our assertion.
		Expect(r.rvTracker.IsCacheStale(key, "1")).To(BeTrue(),
			"after a non-stale-path reconcile, the tracker must have recorded the post-patch RV "+
				"so that a follow-up reconcile starting from an older cache snapshot is correctly "+
				"flagged as stale; an unrecorded tracker would still return false here")
	})

	It("forgets the tracker entry on the NotFound branch so deleted objects don't leak memory", func() {
		// Use a key that doesn't exist in envtest so Get returns NotFound. Seed
		// the tracker with an entry for this key first, then invoke Reconcile,
		// and verify the entry was dropped. This proves the cleanup wiring in
		// the IsNotFound branch is actually called.
		r := &WebRequestCommitStatusReconciler{
			Client:    k8sClient,
			Scheme:    k8sClient.Scheme(),
			Recorder:  events.NewFakeRecorder(10),
			rvTracker: utils.NewResourceVersionTracker(),
		}
		missingKey := types.NamespacedName{Name: "wrcs-stale-cache-guard-deleted", Namespace: "default"}
		r.rvTracker.Record(missingKey, "500")
		Expect(r.rvTracker.IsCacheStale(missingKey, "499")).To(BeTrue(),
			"sanity: tracker has an entry for the (about-to-be-NotFound) key")

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: missingKey})
		Expect(err).NotTo(HaveOccurred(), "NotFound is a benign terminal state, not an error")
		Expect(result).To(Equal(ctrl.Result{}), "NotFound must not request a requeue")

		Expect(r.rvTracker.IsCacheStale(missingKey, "499")).To(BeFalse(),
			"after Reconcile returns on NotFound, the tracker entry must be gone "+
				"(IsCacheStale on a forgotten key is treated as never-recorded → not stale); "+
				"a true return here means deleted-object entries are leaking and the tracker "+
				"will grow unbounded over the controller's lifetime")
	})
})
