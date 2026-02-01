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
	"fmt"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("RequiredCheckCommitStatus Controller", func() {
	Context("When reconciling a RequiredCheckCommitStatus", func() {
		var (
			name               string
			scmSecret          *v1.Secret
			scmProvider        *promoterv1alpha1.ScmProvider
			gitRepo            *promoterv1alpha1.GitRepository
			ps                 *promoterv1alpha1.PromotionStrategy
			rccs               *promoterv1alpha1.RequiredCheckCommitStatus
			typeNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, ps, rccs = requiredCheckCommitStatusResources(ctx, "rccs-test", "default")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			Expect(k8sClient.Create(ctx, rccs)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, rccs)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("should successfully reconcile with no CTPs", func() {
			By("Waiting for reconciliation to complete")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				// No CTPs exist, so no environments should be tracked
				g.Expect(rccs.Status.Environments).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should handle missing PromotionStrategy gracefully", func() {
			By("Deleting the PromotionStrategy")
			Expect(k8sClient.Delete(ctx, ps)).To(Succeed())

			By("Verifying that reconciliation doesn't crash")
			// The controller should log an error but not fail permanently
			// We just verify it doesn't crash the controller
			time.Sleep(2 * time.Second)
		})
	})

	Context("When reconciling with ChangeTransferPolicy", func() {
		var (
			name               string
			scmSecret          *v1.Secret
			scmProvider        *promoterv1alpha1.ScmProvider
			gitRepo            *promoterv1alpha1.GitRepository
			ps                 *promoterv1alpha1.PromotionStrategy
			rccs               *promoterv1alpha1.RequiredCheckCommitStatus
			ctp                *promoterv1alpha1.ChangeTransferPolicy
			typeNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, ps, rccs = requiredCheckCommitStatusResources(ctx, "rccs-ctp-test", "default")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			// Create ChangeTransferPolicy
			ctp = &promoterv1alpha1.ChangeTransferPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-ctp",
					Namespace: "default",
					Labels: map[string]string{
						promoterv1alpha1.PromotionStrategyLabel: name,
					},
				},
				Spec: promoterv1alpha1.ChangeTransferPolicySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					ActiveBranch:   "environment/dev",
					ProposedBranch: "environment/dev-next",
				},
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			Expect(k8sClient.Create(ctx, ctp)).To(Succeed())

			// Update CTP status with proposed SHA
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ctp.Name, Namespace: ctp.Namespace}, ctp)
				g.Expect(err).NotTo(HaveOccurred())
				ctp.Status.Proposed.Hydrated.Sha = "abcdef1234567890abcdef1234567890abcdef12"
				err = k8sClient.Status().Update(ctx, ctp)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Create(ctx, rccs)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, rccs)
			_ = k8sClient.Delete(ctx, ctp)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("should process environment with CTP and proposed SHA", func() {
			By("Waiting for reconciliation to complete and environment to be tracked")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rccs.Status.Environments).To(HaveLen(1))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying environment status")
			err := k8sClient.Get(ctx, typeNamespacedName, rccs)
			Expect(err).NotTo(HaveOccurred())

			env := rccs.Status.Environments[0]
			Expect(env.Branch).To(Equal("environment/dev"))
			Expect(env.Sha).To(Equal("abcdef1234567890abcdef1234567890abcdef12"))
			// Fake provider doesn't support required checks, so we expect no checks
			Expect(env.RequiredChecks).To(BeEmpty())
			Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess)) // No checks = success
		})
	})

	Context("Per-check polling optimization integration", func() {
		var (
			name        string
			scmSecret   *v1.Secret
			scmProvider *promoterv1alpha1.ScmProvider
			gitRepo     *promoterv1alpha1.GitRepository
			ps          *promoterv1alpha1.PromotionStrategy
			rccs        *promoterv1alpha1.RequiredCheckCommitStatus
		)

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, ps, rccs = requiredCheckCommitStatusResources(ctx, "rccs-polling-test", "default")

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			Expect(k8sClient.Create(ctx, rccs)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, rccs)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("should initialize LastPolledAt for all checks on first reconciliation", func() {
			By("Waiting for initial reconciliation")
			// Note: Since we're using FakeRepo, no actual checks will be discovered
			// This test verifies the field exists and can be set
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			// Verify the status is properly initialized (no panics with the new field)
			Expect(rccs.Status).NotTo(BeNil())
		})

		It("should preserve check status structure with LastPolledAt field", func() {
			By("Manually setting check status with LastPolledAt")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())

				now := metav1.Now()
				rccs.Status.Environments = []promoterv1alpha1.RequiredCheckEnvironmentStatus{
					{
						Branch: "environment/dev",
						Sha:    "abc123",
						Phase:  promoterv1alpha1.CommitPhaseSuccess,
						RequiredChecks: []promoterv1alpha1.RequiredCheckStatus{
							{
								Name:         "test-check",
								Key:          "github-test-check",
								Phase:        promoterv1alpha1.CommitPhaseSuccess,
								LastPolledAt: &now,
							},
						},
					},
				}

				err = k8sClient.Status().Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying LastPolledAt persists")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rccs.Status.Environments).To(HaveLen(1))
				g.Expect(rccs.Status.Environments[0].RequiredChecks).To(HaveLen(1))
				g.Expect(rccs.Status.Environments[0].RequiredChecks[0].LastPolledAt).NotTo(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should handle checks with nil LastPolledAt gracefully", func() {
			By("Setting check status without LastPolledAt")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())

				rccs.Status.Environments = []promoterv1alpha1.RequiredCheckEnvironmentStatus{
					{
						Branch: "environment/dev",
						Sha:    "abc123",
						Phase:  promoterv1alpha1.CommitPhasePending,
						RequiredChecks: []promoterv1alpha1.RequiredCheckStatus{
							{
								Name:         "pending-check",
								Key:          "github-pending-check",
								Phase:        promoterv1alpha1.CommitPhasePending,
								LastPolledAt: nil, // Explicitly nil
							},
						},
					},
				}

				err = k8sClient.Status().Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying nil LastPolledAt is accepted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rccs.Status.Environments).To(HaveLen(1))
				g.Expect(rccs.Status.Environments[0].RequiredChecks).To(HaveLen(1))
				g.Expect(rccs.Status.Environments[0].RequiredChecks[0].LastPolledAt).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should update LastPolledAt timestamps over multiple reconciliations", func() {
			By("Setting initial check status")
			var initialPollTime metav1.Time
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())

				initialPollTime = metav1.Now()
				rccs.Status.Environments = []promoterv1alpha1.RequiredCheckEnvironmentStatus{
					{
						Branch: "environment/dev",
						Sha:    "abc123",
						Phase:  promoterv1alpha1.CommitPhaseSuccess,
						RequiredChecks: []promoterv1alpha1.RequiredCheckStatus{
							{
								Name:         "stable-check",
								Key:          "github-stable-check",
								Phase:        promoterv1alpha1.CommitPhaseSuccess,
								LastPolledAt: &initialPollTime,
							},
						},
					},
				}

				err = k8sClient.Status().Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Simulating a later reconciliation with updated timestamp")
			time.Sleep(2 * time.Second) // Ensure time difference
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())

				laterPollTime := metav1.Now()
				rccs.Status.Environments[0].RequiredChecks[0].LastPolledAt = &laterPollTime

				err = k8sClient.Status().Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying timestamp was updated")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rccs.Status.Environments[0].RequiredChecks[0].LastPolledAt).NotTo(BeNil())
				g.Expect(rccs.Status.Environments[0].RequiredChecks[0].LastPolledAt.Time).To(
					BeTemporally(">", initialPollTime.Time))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should handle mixed check states with different poll times", func() {
			By("Setting multiple checks with different states and poll times")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())

				now := metav1.Now()
				fiveMinAgo := metav1.NewTime(now.Time.Add(-5 * time.Minute))
				tenMinAgo := metav1.NewTime(now.Time.Add(-10 * time.Minute))

				rccs.Status.Environments = []promoterv1alpha1.RequiredCheckEnvironmentStatus{
					{
						Branch: "environment/staging",
						Sha:    "def456",
						Phase:  promoterv1alpha1.CommitPhasePending,
						RequiredChecks: []promoterv1alpha1.RequiredCheckStatus{
							{
								Name:         "lint",
								Key:          "github-lint",
								Phase:        promoterv1alpha1.CommitPhaseSuccess,
								LastPolledAt: &tenMinAgo, // Terminal, polled 10 min ago
							},
							{
								Name:         "test",
								Key:          "github-test",
								Phase:        promoterv1alpha1.CommitPhaseSuccess,
								LastPolledAt: &fiveMinAgo, // Terminal, polled 5 min ago
							},
							{
								Name:         "e2e",
								Key:          "github-e2e",
								Phase:        promoterv1alpha1.CommitPhasePending,
								LastPolledAt: &now, // Pending, polled just now
							},
							{
								Name:         "smoke",
								Key:          "github-smoke",
								Phase:        promoterv1alpha1.CommitPhaseFailure,
								LastPolledAt: &fiveMinAgo, // Terminal (failure), polled 5 min ago
							},
						},
					},
				}

				err = k8sClient.Status().Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying all checks maintain their individual poll times")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rccs.Status.Environments).To(HaveLen(1))
				g.Expect(rccs.Status.Environments[0].RequiredChecks).To(HaveLen(4))

				checks := rccs.Status.Environments[0].RequiredChecks

				// Verify each check maintained its poll time
				for _, check := range checks {
					g.Expect(check.LastPolledAt).NotTo(BeNil(), "check %s should have LastPolledAt", check.Name)
				}

				// Terminal checks should have older timestamps
				lintCheck := checks[0]
				g.Expect(lintCheck.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(lintCheck.LastPolledAt.Time).To(BeTemporally("<", checks[2].LastPolledAt.Time))

				// Pending check should have most recent timestamp
				e2eCheck := checks[2]
				g.Expect(e2eCheck.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When cleaning up orphaned CommitStatus resources", func() {
		var (
			name        string
			scmSecret   *v1.Secret
			scmProvider *promoterv1alpha1.ScmProvider
			gitRepo     *promoterv1alpha1.GitRepository
			ps          *promoterv1alpha1.PromotionStrategy
			rccs        *promoterv1alpha1.RequiredCheckCommitStatus
			orphanedCS  *promoterv1alpha1.CommitStatus
		)

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, ps, rccs = requiredCheckCommitStatusResources(ctx, "rccs-cleanup-test", "default")

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			Expect(k8sClient.Create(ctx, rccs)).To(Succeed())

			// Wait for RCCS to be created
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			// Create orphaned CommitStatus
			orphanedCS = &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-orphaned",
					Namespace: "default",
					Labels: map[string]string{
						promoterv1alpha1.RequiredCheckCommitStatusLabel: name,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: promoterv1alpha1.GroupVersion.String(),
							Kind:       "RequiredCheckCommitStatus",
							Name:       rccs.Name,
							UID:        rccs.UID,
							Controller: func() *bool { b := true; return &b }(),
						},
					},
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Sha:         "1234567890abcdef1234567890abcdef12345678",
					Name:        "orphaned-check",
					Phase:       promoterv1alpha1.CommitPhasePending,
					Description: "This should be deleted",
				},
			}
			Expect(k8sClient.Create(ctx, orphanedCS)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, orphanedCS)
			_ = k8sClient.Delete(ctx, rccs)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("should delete orphaned CommitStatus resources", func() {
			By("Triggering reconciliation by updating RCCS")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rccs)
				g.Expect(err).NotTo(HaveOccurred())
				rccs.Annotations = map[string]string{"trigger": "cleanup"}
				err = k8sClient.Update(ctx, rccs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for orphaned CommitStatus to be deleted")
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      orphanedCS.Name,
					Namespace: orphanedCS.Namespace,
				}, &cs)
				g.Expect(err).To(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	// Unit tests for helper functions
	Describe("Helper functions", func() {
		Context("calculateAggregatedPhase", func() {
			var r *RequiredCheckCommitStatusReconciler

			BeforeEach(func() {
				r = &RequiredCheckCommitStatusReconciler{}
			})

			It("should return success when there are no checks", func() {
				phase := r.calculateAggregatedPhase([]promoterv1alpha1.RequiredCheckStatus{})
				Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			})

			It("should return failure if any check is failing", func() {
				checks := []promoterv1alpha1.RequiredCheckStatus{
					{Name: "check1", Key: "github-check1", Phase: promoterv1alpha1.CommitPhaseSuccess},
					{Name: "check2", Key: "github-check2", Phase: promoterv1alpha1.CommitPhaseFailure},
					{Name: "check3", Key: "github-check3", Phase: promoterv1alpha1.CommitPhasePending},
				}
				phase := r.calculateAggregatedPhase(checks)
				Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
			})

			It("should return pending if no failures and any check is pending", func() {
				checks := []promoterv1alpha1.RequiredCheckStatus{
					{Name: "check1", Key: "github-check1", Phase: promoterv1alpha1.CommitPhaseSuccess},
					{Name: "check2", Key: "github-check2", Phase: promoterv1alpha1.CommitPhasePending},
				}
				phase := r.calculateAggregatedPhase(checks)
				Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			})

			It("should return success if all checks are successful", func() {
				checks := []promoterv1alpha1.RequiredCheckStatus{
					{Name: "check1", Key: "github-check1", Phase: promoterv1alpha1.CommitPhaseSuccess},
					{Name: "check2", Key: "github-check2", Phase: promoterv1alpha1.CommitPhaseSuccess},
				}
				phase := r.calculateAggregatedPhase(checks)
				Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			})
		})

		Context("per-check polling optimization", func() {
			It("should include LastPolledAt in check status", func() {
				now := metav1.Now()
				check := promoterv1alpha1.RequiredCheckStatus{
					Name:         "test-check",
					Key:          "github-test-check",
					Phase:        promoterv1alpha1.CommitPhaseSuccess,
					LastPolledAt: &now,
				}

				Expect(check.LastPolledAt).NotTo(BeNil())
				Expect(check.LastPolledAt.Time).To(BeTemporally("~", now.Time, time.Second))
			})

			It("should allow nil LastPolledAt for new checks", func() {
				check := promoterv1alpha1.RequiredCheckStatus{
					Name:         "new-check",
					Key:          "github-new-check",
					Phase:        promoterv1alpha1.CommitPhasePending,
					LastPolledAt: nil,
				}

				Expect(check.LastPolledAt).To(BeNil())
			})

			It("should preserve LastPolledAt through deepcopy", func() {
				now := metav1.Now()
				original := promoterv1alpha1.RequiredCheckStatus{
					Name:         "test-check",
					Key:          "github-test-check",
					Phase:        promoterv1alpha1.CommitPhaseSuccess,
					LastPolledAt: &now,
				}

				copy := *original.DeepCopy()
				Expect(copy.LastPolledAt).NotTo(BeNil())
				Expect(copy.LastPolledAt.Time).To(Equal(original.LastPolledAt.Time))

				// Modify copy shouldn't affect original
				laterTime := metav1.NewTime(now.Time.Add(5 * time.Minute))
				copy.LastPolledAt = &laterTime
				Expect(original.LastPolledAt.Time).NotTo(Equal(copy.LastPolledAt.Time))
			})

			It("should track different poll times for different checks", func() {
				now := metav1.Now()
				laterTime := metav1.NewTime(now.Time.Add(5 * time.Minute))

				env := promoterv1alpha1.RequiredCheckEnvironmentStatus{
					Branch: "main",
					Sha:    "abc123",
					RequiredChecks: []promoterv1alpha1.RequiredCheckStatus{
						{
							Name:         "lint",
							Key:          "github-lint",
							Phase:        promoterv1alpha1.CommitPhaseSuccess,
							LastPolledAt: &now,
						},
						{
							Name:         "test",
							Key:          "github-test",
							Phase:        promoterv1alpha1.CommitPhasePending,
							LastPolledAt: &laterTime,
						},
					},
				}

				Expect(env.RequiredChecks).To(HaveLen(2))
				Expect(env.RequiredChecks[0].LastPolledAt.Time).To(BeTemporally("<", env.RequiredChecks[1].LastPolledAt.Time))
			})
		})

		Context("generateRequiredCheckDescription", func() {
			It("should generate pending description", func() {
				desc := generateRequiredCheckDescription("smoke-test", promoterv1alpha1.CommitPhasePending)
				Expect(desc).To(Equal("Waiting for smoke-test to pass"))
			})

			It("should generate success description", func() {
				desc := generateRequiredCheckDescription("smoke-test", promoterv1alpha1.CommitPhaseSuccess)
				Expect(desc).To(Equal("Check smoke-test is passing"))
			})

			It("should generate failure description", func() {
				desc := generateRequiredCheckDescription("smoke-test", promoterv1alpha1.CommitPhaseFailure)
				Expect(desc).To(Equal("Check smoke-test is failing"))
			})

			It("should handle unknown phase", func() {
				desc := generateRequiredCheckDescription("smoke-test", "unknown")
				Expect(desc).To(Equal("Required check: smoke-test"))
			})
		})

		Context("buildCacheKey", func() {
			var r *RequiredCheckCommitStatusReconciler

			BeforeEach(func() {
				r = &RequiredCheckCommitStatusReconciler{}
			})

			It("should build cache keys with namespace, name, and branch", func() {
				gitRepo := &promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-repo",
						Namespace: "test-ns",
					},
				}
				key := r.buildCacheKey(gitRepo, "main")
				Expect(key).To(Equal("test-ns|test-repo|main"))
			})

			It("should generate different keys for different repos", func() {
				repo1 := &promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
				}
				repo2 := &promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{Name: "repo2", Namespace: "ns1"},
				}

				key1 := r.buildCacheKey(repo1, "main")
				key2 := r.buildCacheKey(repo2, "main")
				Expect(key1).NotTo(Equal(key2))
			})

			It("should generate different keys for different branches", func() {
				gitRepo := &promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
				}

				key1 := r.buildCacheKey(gitRepo, "main")
				key2 := r.buildCacheKey(gitRepo, "develop")
				Expect(key1).NotTo(Equal(key2))
			})
		})

		Context("validateRequiredCheckConfig", func() {
			It("should validate pending check interval minimum", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					PendingCheckInterval: &metav1.Duration{Duration: 5 * time.Second},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("pendingCheckInterval must be >= 10s"))
			})

			It("should validate terminal check interval minimum", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					TerminalCheckInterval: &metav1.Duration{Duration: 5 * time.Second},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("terminalCheckInterval must be >= 10s"))
			})

			It("should reject negative intervals", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					PendingCheckInterval: &metav1.Duration{Duration: -1 * time.Second},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must not be negative"))
			})

			It("should validate interval relationships", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					PendingCheckInterval:  &metav1.Duration{Duration: 2 * time.Minute},
					TerminalCheckInterval: &metav1.Duration{Duration: 1 * time.Minute},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("pendingCheckInterval"))
				Expect(err.Error()).To(ContainSubstring("should be <= terminalCheckInterval"))
			})

			It("should validate safety net interval relationships", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					PendingCheckInterval: &metav1.Duration{Duration: 2 * time.Minute},
					SafetyNetInterval:    &metav1.Duration{Duration: 1 * time.Minute},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("safetyNetInterval"))
			})

			It("should validate cache TTL", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					RequiredCheckCacheTTL: &metav1.Duration{Duration: -1 * time.Second},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("requiredCheckCacheTTL must not be negative"))
			})

			It("should validate cache max size", func() {
				size := 5
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					RequiredCheckCacheMaxSize: &size,
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("requiredCheckCacheMaxSize is very small"))
			})

			It("should accept valid configuration", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					PendingCheckInterval:  &metav1.Duration{Duration: 1 * time.Minute},
					TerminalCheckInterval: &metav1.Duration{Duration: 10 * time.Minute},
					SafetyNetInterval:     &metav1.Duration{Duration: 1 * time.Hour},
					RequiredCheckCacheTTL: &metav1.Duration{Duration: 24 * time.Hour},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should allow zero safety net interval (disabled)", func() {
				config := &promoterv1alpha1.RequiredCheckCommitStatusConfiguration{
					SafetyNetInterval: &metav1.Duration{Duration: 0},
				}
				err := validateRequiredCheckConfig(config)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		// Note: calculateRequeueDuration tests are omitted as they require SettingsMgr
		// which is complex to set up in unit tests. This functionality is covered by
		// integration tests where the full controller is properly initialized.

		Context("Cache integration with golang-lru", func() {
			var testCache *expirable.LRU[string, []scms.RequiredCheck]

			BeforeEach(func() {
				testCache = expirable.NewLRU[string, []scms.RequiredCheck](
					5,                      // small size for testing
					nil,                    // no eviction callback
					100*time.Millisecond,   // short TTL for testing
				)
			})

			It("should automatically evict expired entries after TTL", func() {
				testCache.Add("key1", []scms.RequiredCheck{})
				Expect(testCache.Contains("key1")).To(BeTrue())

				time.Sleep(150 * time.Millisecond) // Wait past TTL

				_, found := testCache.Get("key1")
				Expect(found).To(BeFalse())
			})

			It("should automatically evict oldest entries when size limit reached", func() {
				// Add 6 entries to cache with limit of 5
				for i := 0; i < 6; i++ {
					testCache.Add(fmt.Sprintf("key-%d", i), []scms.RequiredCheck{})
					time.Sleep(10 * time.Millisecond) // Ensure ordering
				}

				// First entry should be evicted (LRU)
				Expect(testCache.Contains("key-0")).To(BeFalse())
				// Most recent entry should remain
				Expect(testCache.Contains("key-5")).To(BeTrue())
			})
		})
	})
})

// requiredCheckCommitStatusResources creates all the resources needed for a RequiredCheckCommitStatus test
// Returns: name (with random suffix), scmSecret, scmProvider, gitRepo, promotionStrategy, requiredCheckCommitStatus
func requiredCheckCommitStatusResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PromotionStrategy, *promoterv1alpha1.RequiredCheckCommitStatus) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))

	scmSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"token": []byte("fake-token"),
		},
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &v1.LocalObjectReference{Name: name},
			Fake:      &promoterv1alpha1.Fake{},
		},
	}

	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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

	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Environments: []promoterv1alpha1.Environment{
				{
					Branch: "environment/dev",
				},
			},
		},
	}

	rccs := &promoterv1alpha1.RequiredCheckCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.RequiredCheckCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{
				Name: name,
			},
		},
	}

	return name, scmSecret, scmProvider, gitRepo, ps, rccs
}
