/*
Copyright 2026.

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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("ScheduledCommitStatus Controller", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *v1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "scheduled-test", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "promotion-window"},
		}
		// PS hard-fails without an ordering gate; declare and create PECS like other gate tests.
		declarePreviousEnvironmentGate(promotionStrategy)

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
		By("Cleaning up test resources")
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, &promoterv1alpha1.PreviousEnvironmentCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: promotionStrategy.Name, Namespace: promotionStrategy.Namespace},
			})
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

	Describe("Inside Allow Window", func() {
		var scs *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus with a wide-open window")
			scs = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-allow",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					Key: "promotion-window",
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledEnvironment{
						{
							Branch: testBranchDevelopment,
							Allow: []promoterv1alpha1.CronWindow{
								{
									Cron:     "* * * * *",
									Duration: metav1.Duration{Duration: 24 * time.Hour},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scs)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, scs)
		})

		It("should report success when inside an allow window", func() {
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-allow",
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(current.Status.Environments).To(HaveLen(1))
				g.Expect(current.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(current.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(current.Status.Environments[0].Sha).ToNot(BeEmpty())
				g.Expect(current.Status.Environments[0].Active).ToNot(BeNil())
				g.Expect(current.Status.Environments[0].Active.Allow).To(Equal("* * * * *"))

				commitStatusName := utils.CommitStatusResourceName(ctx, &current, testBranchDevelopment)
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

	Describe("Exclusion-Only Mode", func() {
		var scs *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus with only exclusions (none active)")
			scs = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-excl-only",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					Key: "promotion-window",
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledEnvironment{
						{
							Branch: testBranchDevelopment,
							Exclude: []promoterv1alpha1.CronWindow{
								{
									// Schedule exclusion far in the future (Feb 29 only fires on leap years)
									Cron:     "0 0 29 2 *",
									Duration: metav1.Duration{Duration: 1 * time.Hour},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scs)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, scs)
		})

		It("should report success when no exclusion is active", func() {
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-excl-only",
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(current.Status.Environments).To(HaveLen(1))
				g.Expect(current.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(current.Status.Environments[0].Active).To(BeNil())

				commitStatusName := utils.CommitStatusResourceName(ctx, &current, testBranchDevelopment)
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

	Describe("Exclusion Overrides Allow Window", func() {
		var scs *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus with both window and active exclusion")
			scs = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-excl-override",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					Key: "promotion-window",
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledEnvironment{
						{
							Branch: testBranchDevelopment,
							Allow: []promoterv1alpha1.CronWindow{
								{
									Cron:     "* * * * *",
									Duration: metav1.Duration{Duration: 24 * time.Hour},
								},
							},
							Exclude: []promoterv1alpha1.CronWindow{
								{
									Cron:     "* * * * *",
									Duration: metav1.Duration{Duration: 24 * time.Hour},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scs)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, scs)
		})

		It("should report pending when exclusion overrides allow window", func() {
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-excl-override",
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(current.Status.Environments).To(HaveLen(1))
				g.Expect(current.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
				g.Expect(current.Status.Environments[0].Active).ToNot(BeNil())
				g.Expect(current.Status.Environments[0].Active.Exclude).To(Equal("* * * * *"))

				commitStatusName := utils.CommitStatusResourceName(ctx, &current, testBranchDevelopment)
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

	Describe("Environment Cleanup", func() {
		var scs *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus tracking all three environments")
			scs = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-cleanup",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					Key: "promotion-window",
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledEnvironment{
						{
							Branch: testBranchDevelopment,
							Allow: []promoterv1alpha1.CronWindow{
								{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
							},
						},
						{
							Branch: testBranchStaging,
							Allow: []promoterv1alpha1.CronWindow{
								{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
							},
						},
						{
							Branch: testBranchProduction,
							Allow: []promoterv1alpha1.CronWindow{
								{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scs)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, scs)
		})

		It("should cleanup orphaned CommitStatus resources when environments are removed", func() {
			var oldCommitStatusDevName string
			var oldCommitStatusStagingName string
			var oldCommitStatusProdName string

			By("Waiting for all three CommitStatus resources to be created")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(current.Status.Environments).To(HaveLen(3))

				oldCommitStatusDevName = utils.CommitStatusResourceName(ctx, &current, testBranchDevelopment)
				oldCommitStatusStagingName = utils.CommitStatusResourceName(ctx, &current, testBranchStaging)
				oldCommitStatusProdName = utils.CommitStatusResourceName(ctx, &current, testBranchProduction)

				for _, csName := range []string{oldCommitStatusDevName, oldCommitStatusStagingName, oldCommitStatusProdName} {
					cs := &promoterv1alpha1.CommitStatus{}
					err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, cs)
					g.Expect(err).NotTo(HaveOccurred())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating to only track development")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())

				current.Spec.Environments = []promoterv1alpha1.ScheduledEnvironment{
					{
						Branch: testBranchDevelopment,
						Allow: []promoterv1alpha1.CronWindow{
							{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
						},
					},
				}
				err = k8sClient.Update(ctx, &current)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging and production CommitStatus resources are deleted")
			Eventually(func(g Gomega) {
				cs := &promoterv1alpha1.CommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: oldCommitStatusStagingName, Namespace: "default"}, cs)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "Staging CommitStatus should be deleted")

				err = k8sClient.Get(ctx, types.NamespacedName{Name: oldCommitStatusProdName, Namespace: "default"}, cs)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "Production CommitStatus should be deleted")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying development CommitStatus still exists")
			Eventually(func(g Gomega) {
				cs := &promoterv1alpha1.CommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: oldCommitStatusDevName, Namespace: "default"}, cs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("commit status key", func() {
		const customKey = "deploy-window"

		var (
			keyCtx     context.Context
			keyName    string
			keySecret  *v1.Secret
			keyScmProv *promoterv1alpha1.ScmProvider
			keyGitRepo *promoterv1alpha1.GitRepository
			keyPS      *promoterv1alpha1.PromotionStrategy
			keyPwcs    *promoterv1alpha1.ScheduledCommitStatus
		)

		BeforeEach(func() {
			keyCtx = context.Background()
			keyName, keySecret, keyScmProv, keyGitRepo, _, _, keyPS = promotionStrategyResource(keyCtx, "scheduled-key-test", "default")
			keyPS.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: customKey},
			}
			declarePreviousEnvironmentGate(keyPS)

			setupInitialTestGitRepoOnServer(keyCtx, keyGitRepo)

			Expect(k8sClient.Create(keyCtx, keySecret)).To(Succeed())
			Expect(k8sClient.Create(keyCtx, keyScmProv)).To(Succeed())
			Expect(k8sClient.Create(keyCtx, keyGitRepo)).To(Succeed())
			Expect(k8sClient.Create(keyCtx, keyPS)).To(Succeed())
			createPreviousEnvironmentCommitStatus(keyCtx, keyPS)

			keyPwcs = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyName + "-custom-key",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: keyName,
					},
					Key: customKey,
					Environments: []promoterv1alpha1.ScheduledEnvironment{
						{
							Branch: testBranchDevelopment,
							Allow: []promoterv1alpha1.CronWindow{
								{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(keyCtx, keyPwcs)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(keyCtx, keyPwcs)
			_ = k8sClient.Delete(keyCtx, &promoterv1alpha1.PreviousEnvironmentCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: keyPS.Name, Namespace: keyPS.Namespace},
			})
			_ = k8sClient.Delete(keyCtx, keyPS)
			_ = k8sClient.Delete(keyCtx, keyGitRepo)
			_ = k8sClient.Delete(keyCtx, keyScmProv)
			_ = k8sClient.Delete(keyCtx, keySecret)
		})

		It("should use custom spec.key on CommitStatus label and name", func() {
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(keyCtx, types.NamespacedName{
					Name:      keyPwcs.Name,
					Namespace: "default",
				}, &current)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(current.Status.Environments).To(HaveLen(1))

				commitStatusName := utils.CommitStatusResourceName(keyCtx, &current, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(keyCtx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(customKey))
				g.Expect(cs.Spec.Name).To(HavePrefix(customKey + "/"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("ScheduledCommitStatus Controller - Missing PromotionStrategy", Ordered, func() {
	var (
		ctx context.Context
		scs *promoterv1alpha1.ScheduledCommitStatus
	)

	BeforeEach(func() {
		ctx = context.Background()
		scs = &promoterv1alpha1.ScheduledCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scs-missing-ps",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
				Key: "promotion-window",
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{
					Name: "nonexistent-ps",
				},
				Environments: []promoterv1alpha1.ScheduledEnvironment{
					{
						Branch: testBranchDevelopment,
						Allow: []promoterv1alpha1.CronWindow{
							{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scs)).To(Succeed())
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, scs)
	})

	It("should handle missing PromotionStrategy gracefully", func() {
		Eventually(func(g Gomega) {
			var current promoterv1alpha1.ScheduledCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "scs-missing-ps",
				Namespace: "default",
			}, &current)
			g.Expect(err).NotTo(HaveOccurred())

			readyCondition := meta.FindStatusCondition(current.Status.Conditions, "Ready")
			g.Expect(readyCondition).ToNot(BeNil())
			g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})

var _ = Describe("ScheduledCommitStatus Controller - Branch Mismatch", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *v1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		scs               *promoterv1alpha1.ScheduledCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "scheduled-branch-mismatch", "default")

		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "promotion-window"},
		}
		declarePreviousEnvironmentGate(promotionStrategy)

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createPreviousEnvironmentCommitStatus(ctx, promotionStrategy)
	})

	AfterAll(func() {
		if scs != nil {
			_ = k8sClient.Delete(ctx, scs)
		}
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, &promoterv1alpha1.PreviousEnvironmentCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: promotionStrategy.Name, Namespace: promotionStrategy.Namespace},
			})
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

	It("should set Ready=False when a branch does not exist in PromotionStrategy", func() {
		scs = &promoterv1alpha1.ScheduledCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-branch-mismatch",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
				Key: "promotion-window",
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{
					Name: name,
				},
				Environments: []promoterv1alpha1.ScheduledEnvironment{
					{
						Branch: "environment/nonexistent",
						Allow: []promoterv1alpha1.CronWindow{
							{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scs)).To(Succeed())

		Eventually(func(g Gomega) {
			var current promoterv1alpha1.ScheduledCommitStatus
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      name + "-branch-mismatch",
				Namespace: "default",
			}, &current)
			g.Expect(err).NotTo(HaveOccurred())

			readyCondition := meta.FindStatusCondition(current.Status.Conditions, "Ready")
			g.Expect(readyCondition).ToNot(BeNil())
			g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(readyCondition.Message).To(ContainSubstring("environment/nonexistent"))
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})

var _ = Describe("ScheduledCommitStatus - YAML testdata", func() {
	It("should deserialize the example manifest", func() {
		data, err := os.ReadFile("testdata/ScheduledCommitStatus.yaml")
		Expect(err).NotTo(HaveOccurred())

		var scs promoterv1alpha1.ScheduledCommitStatus
		Expect(unmarshalYamlStrict(string(data), &scs)).To(Succeed())

		Expect(scs.Spec.Key).To(Equal("promotion-window"))
		Expect(scs.Spec.PromotionStrategyRef.Name).To(Equal("webservice-tier-1"))
		Expect(scs.Spec.Timezone).To(Equal("UTC"))
		Expect(scs.Spec.Exclude).To(HaveLen(1))
		Expect(scs.Spec.Environments).To(HaveLen(3))
		Expect(scs.Spec.Environments[0].Branch).To(Equal("environment/development"))
		Expect(scs.Spec.Environments[0].Allow).To(HaveLen(1))
		Expect(scs.Spec.Environments[0].Allow[0].Description).To(Equal("US East business hours"))
		Expect(scs.Spec.Environments[0].Allow[0].Timezone).To(Equal("America/New_York"))
	})
})

var _ = Describe("mergeWindows", func() {
	globalExclude := []promoterv1alpha1.CronWindow{
		{Cron: "0 0 25 12 *", Duration: metav1.Duration{Duration: 48 * time.Hour}},
	}
	localAllow := []promoterv1alpha1.CronWindow{
		{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
	}
	localExclude := []promoterv1alpha1.CronWindow{
		{Cron: "0 0 * * 0", Duration: metav1.Duration{Duration: 24 * time.Hour}},
	}

	It("should return local when global is empty", func() {
		result := mergeWindows(nil, localAllow)
		Expect(result).To(Equal(localAllow))
	})

	It("should return global when local is empty", func() {
		result := mergeWindows(globalExclude, nil)
		Expect(result).To(Equal(globalExclude))
	})

	It("should return nil when both are empty", func() {
		result := mergeWindows(nil, nil)
		Expect(result).To(BeNil())
	})

	It("should concatenate global exclude + local allow", func() {
		result := mergeWindows(globalExclude, localAllow)
		Expect(result).To(HaveLen(2))
		Expect(result[0]).To(Equal(globalExclude[0]))
		Expect(result[1]).To(Equal(localAllow[0]))
	})

	It("should concatenate global exclude + local exclude", func() {
		result := mergeWindows(globalExclude, localExclude)
		Expect(result).To(HaveLen(2))
		Expect(result[0]).To(Equal(globalExclude[0]))
		Expect(result[1]).To(Equal(localExclude[0]))
	})

	It("should evaluate OR across global + local allow windows", func() {
		globalAllow := []promoterv1alpha1.CronWindow{
			{Cron: "0 6 * * 1-5", Duration: metav1.Duration{Duration: 2 * time.Hour}},
		}
		merged := mergeWindows(globalAllow, localAllow)
		Expect(merged).To(HaveLen(2))

		// Monday 07:00 — inside global allow (06:00+2h) but outside local (09:00+8h)
		now := time.Date(2026, 7, 6, 7, 0, 0, 0, time.UTC)
		result, err := evaluateWindows(now, merged, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Allow).To(Equal("0 6 * * 1-5"))
	})

	It("should allow promotion when only global allow windows are set and env has none", func() {
		globalAllow := []promoterv1alpha1.CronWindow{
			{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
		}
		merged := mergeWindows(globalAllow, nil)

		// Monday 10:00 — inside global allow window
		now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
		result, err := evaluateWindows(now, merged, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Allow).To(Equal("0 9 * * 1-5"))
	})

	It("should block promotion when only global allow windows are set and time is outside", func() {
		globalAllow := []promoterv1alpha1.CronWindow{
			{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
		}
		merged := mergeWindows(globalAllow, nil)

		// Monday 06:00 — outside global allow window
		now := time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC)
		result, err := evaluateWindows(now, merged, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
	})

	It("should block promotion when only global exclude is set and exclusion is active", func() {
		globalExcl := []promoterv1alpha1.CronWindow{
			{Cron: "0 0 25 12 *", Duration: metav1.Duration{Duration: 48 * time.Hour}},
		}
		merged := mergeWindows(globalExcl, nil)

		// Christmas day — inside global exclusion
		now := time.Date(2026, 12, 25, 12, 0, 0, 0, time.UTC)
		result, err := evaluateWindows(now, nil, merged, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Exclude).To(Equal("0 0 25 12 *"))
	})

	It("should block when global exclude overrides local allow", func() {
		allow := mergeWindows(nil, localAllow)   // local only: Mon-Fri 09:00+8h
		excl := mergeWindows(globalExclude, nil) // global: Christmas 48h

		// Christmas on a weekday, 10:00 — inside local allow AND global exclude
		now := time.Date(2026, 12, 25, 10, 0, 0, 0, time.UTC) // Friday
		result, err := evaluateWindows(now, allow, excl, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Exclude).To(Equal("0 0 25 12 *"))
	})

	It("should block when local exclude is active even with global allow", func() {
		globalAllow := []promoterv1alpha1.CronWindow{
			{Cron: "0 0 * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}}, // always open
		}
		allow := mergeWindows(globalAllow, nil)
		excl := mergeWindows(nil, localExclude) // local: Sundays 24h

		// Sunday 12:00 — inside global allow AND local exclude
		now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) // Sunday
		result, err := evaluateWindows(now, allow, excl, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Exclude).To(Equal("0 0 * * 0"))
	})
})

var _ = Describe("evaluateWindows", func() {
	DescribeTable("window evaluation logic",
		func(now time.Time, allow, exclude []promoterv1alpha1.CronWindow, expectedPhase promoterv1alpha1.CommitStatusPhase, expectedActiveAllow, expectedActiveExclude string) {
			result, err := evaluateWindows(now, allow, exclude, "UTC")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Phase).To(Equal(expectedPhase), "phase mismatch")
			activeAllow := ""
			activeExclude := ""
			if result.Active != nil {
				activeAllow = result.Active.Allow
				activeExclude = result.Active.Exclude
			}
			Expect(activeAllow).To(Equal(expectedActiveAllow), "activeAllow mismatch")
			Expect(activeExclude).To(Equal(expectedActiveExclude), "activeExclude mismatch")
		},

		// Inside a window
		Entry("inside allow window (Mon 10:00, window Mon-Fri 09:00 for 8h)",
			time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC), // Monday
			[]promoterv1alpha1.CronWindow{{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}}},
			nil,
			promoterv1alpha1.CommitPhaseSuccess, "0 9 * * 1-5", "",
		),

		// Outside all windows
		Entry("outside allow window (Mon 06:00, window Mon-Fri 09:00 for 8h)",
			time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC), // Monday 06:00
			[]promoterv1alpha1.CronWindow{{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}}},
			nil,
			promoterv1alpha1.CommitPhasePending, "", "",
		),

		// Exclusion-only, no exclusion active
		Entry("exclusion-only mode, no exclusion active",
			time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
			nil,
			[]promoterv1alpha1.CronWindow{{Cron: "0 0 25 12 *", Duration: metav1.Duration{Duration: 48 * time.Hour}}},
			promoterv1alpha1.CommitPhaseSuccess, "", "",
		),

		// Exclusion-only, exclusion active
		Entry("exclusion-only mode, exclusion active",
			time.Date(2026, 12, 25, 12, 0, 0, 0, time.UTC),
			nil,
			[]promoterv1alpha1.CronWindow{{Cron: "0 0 25 12 *", Duration: metav1.Duration{Duration: 48 * time.Hour}}},
			promoterv1alpha1.CommitPhasePending, "", "0 0 25 12 *",
		),

		// Exclusion overrides allow window
		Entry("exclusion overrides allow window",
			time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
			[]promoterv1alpha1.CronWindow{{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}}},
			[]promoterv1alpha1.CronWindow{{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}}},
			promoterv1alpha1.CommitPhasePending, "", "* * * * *",
		),

		// Multiple windows, one active
		Entry("multiple windows, second one active",
			time.Date(2026, 7, 11, 11, 0, 0, 0, time.UTC), // Saturday 11:00
			[]promoterv1alpha1.CronWindow{
				{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
				{Cron: "0 10 * * 6", Duration: metav1.Duration{Duration: 4 * time.Hour}},
			},
			nil,
			promoterv1alpha1.CommitPhaseSuccess, "0 10 * * 6", "",
		),

		// Window boundary: exactly at end should be outside
		Entry("at exact window end (window 09:00 for 1h, now 10:00)",
			time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
			[]promoterv1alpha1.CronWindow{{Cron: "0 9 * * 1", Duration: metav1.Duration{Duration: 1 * time.Hour}}},
			nil,
			promoterv1alpha1.CommitPhasePending, "", "",
		),

		// Window boundary: exactly at start should be inside
		Entry("at exact window start (window 09:00 for 1h, now 09:00)",
			time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC),
			[]promoterv1alpha1.CronWindow{{Cron: "0 9 * * 1", Duration: metav1.Duration{Duration: 1 * time.Hour}}},
			nil,
			promoterv1alpha1.CommitPhaseSuccess, "0 9 * * 1", "",
		),
	)

	It("should return error for invalid cron expression", func() {
		now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
		_, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "invalid", Duration: metav1.Duration{Duration: 1 * time.Hour}},
		}, nil, "UTC")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid allow cron"))
	})

	It("should return error for invalid exclusion cron expression", func() {
		now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
		_, err := evaluateWindows(now, nil, []promoterv1alpha1.CronWindow{
			{Cron: "not-a-cron", Duration: metav1.Duration{Duration: 1 * time.Hour}},
		}, "UTC")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid exclude cron"))
	})

	It("should compute nextTransition for active window", func() {
		now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC) // Monday 10:00
		result, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
		}, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Transition).ToNot(BeNil())
		// Window started at 09:00, duration 8h, so it ends at 17:00
		Expect(result.Active.Transition.Equal(time.Date(2026, 7, 6, 17, 0, 0, 0, time.UTC))).To(BeTrue())
		Expect(result.Next).ToNot(BeNil())
		Expect(result.Next.Transition).ToNot(BeNil())
		Expect(result.Next.Transition.Equal(time.Date(2026, 7, 6, 17, 0, 0, 0, time.UTC))).To(BeTrue())
		Expect(result.Next.Allow).To(Equal("0 9 * * 1-5"))
	})

	It("should use latest covering occurrence for frequent crons to avoid reconciliation churn", func() {
		now := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC) // Monday 10:30
		result, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "* * * * *", Duration: metav1.Duration{Duration: 2 * time.Hour}},
		}, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		Expect(result.Next).ToNot(BeNil())
		Expect(result.Next.Transition).ToNot(BeNil())
		// Latest covering occurrence is 10:30, so window ends at 12:30, not 08:31+2h=10:31
		Expect(result.Next.Transition.Equal(time.Date(2026, 7, 6, 12, 30, 0, 0, time.UTC))).To(BeTrue())
	})

	It("should compute nextTransition for pending state (next window start)", func() {
		now := time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC) // Monday 06:00
		result, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}},
		}, nil, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		Expect(result.Next).ToNot(BeNil())
		Expect(result.Next.Transition).ToNot(BeNil())
		// Next window opens at 09:00
		Expect(result.Next.Transition.Equal(time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC))).To(BeTrue())
		Expect(result.Next.Allow).To(Equal("0 9 * * 1-5"))
	})

	It("should use per-window timezone override instead of default", func() {
		// 14:00 UTC = 10:00 America/New_York (EDT, UTC-4)
		now := time.Date(2026, 7, 6, 14, 0, 0, 0, time.UTC) // Monday 14:00 UTC
		result, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "0 9 * * 1-5", Duration: metav1.Duration{Duration: 8 * time.Hour}, Timezone: "America/New_York"},
		}, nil, "Europe/London")
		Expect(err).NotTo(HaveOccurred())
		// 14:00 UTC is 10:00 ET — inside window (09:00-17:00 ET)
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		Expect(result.Active).ToNot(BeNil())
		Expect(result.Active.Allow).To(Equal("0 9 * * 1-5"))
	})

	It("should use default timezone when window has no override", func() {
		// 10:00 UTC = 12:00 Europe/Paris (CEST, UTC+2)
		now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC) // Monday 10:00 UTC
		result, err := evaluateWindows(now, []promoterv1alpha1.CronWindow{
			{Cron: "0 11 * * 1-5", Duration: metav1.Duration{Duration: 6 * time.Hour}},
		}, nil, "Europe/Paris")
		Expect(err).NotTo(HaveOccurred())
		// 10:00 UTC = 12:00 Paris — inside window (11:00-17:00 Paris)
		Expect(result.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
	})
})
