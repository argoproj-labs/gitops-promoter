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
	"errors"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	bitbucket_datacenter "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

// // createCheckCommitStatus builds an in-memory CommitStatus for seeding SCM checks via
// CommitStatusProvider.Set (scmserver). It is not created in the cluster.
func createCheckCommitStatus(gitRepo *promoterv1alpha1.GitRepository, name, sha string, phase promoterv1alpha1.CommitStatusPhase) *promoterv1alpha1.CommitStatus {
	return &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gitRepo.Namespace},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: gitRepo.Name},
			Sha:                 sha,
			Name:                name,
			Phase:               phase,
			Url:                 "https://ci.example.com/" + name,
		},
	}
}

// toFakeChecks converts a list of commit statuses to fake checks
func toFakeChecks(commitStatuses []*promoterv1alpha1.CommitStatus) []fake.Check {
	fakeChecks := make([]fake.Check, 0, len(commitStatuses))
	for _, cs := range commitStatuses {
		fakeChecks = append(fakeChecks, fake.Check{
			Name:   cs.Name,
			Status: string(cs.Spec.Phase),
			URL:    cs.Spec.Url,
		})
	}
	return fakeChecks
}

// seedSCMChecks creates the checks on the SCM that is set
func seedSCMChecks(ctx context.Context, ps promoterv1alpha1.PromotionStrategy, scmProvider promoterv1alpha1.GenericScmProvider, secret *v1.Secret, gitRepo promoterv1alpha1.GitRepository,
	branch string, checks []*promoterv1alpha1.CommitStatus,
) {
	if scmTarget.isFake() {
		err := fake.SetChecks(ctx, k8sClient, ps, branch, toFakeChecks(checks))
		Expect(err).NotTo(HaveOccurred())
		return
	}

	Eventually(func(g Gomega) {
		var provider scms.CommitStatusProvider
		var err error
		switch {
		case scmProvider.GetSpec().GitHub != nil:
			provider, err = github.NewGithubCommitStatusProvider(ctx, k8sClient, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
		case scmProvider.GetSpec().GitLab != nil:
			provider, err = gitlab.NewGitlabCommitStatusProvider(k8sClient, *secret, scmProvider.GetSpec().GitLab.Domain)
		case scmProvider.GetSpec().BitbucketCloud != nil:
			provider, err = bitbucket_cloud.NewBitbucketCloudCommitStatusProvider(k8sClient, *secret, scmProvider.GetSpec().BitbucketCloud.Domain)
		case scmProvider.GetSpec().BitbucketDataCenter != nil:
			provider, err = bitbucket_datacenter.NewBitbucketDataCenterCommitStatusProvider(k8sClient, scmProvider, *secret)
		case scmProvider.GetSpec().Forgejo != nil:
			provider, err = forgejo.NewForgejoCommitStatusProvider(k8sClient, scmProvider, *secret)
		case scmProvider.GetSpec().Gitea != nil:
			provider, err = gitea.NewGiteaCommitStatusProvider(k8sClient, scmProvider, *secret)
		case scmProvider.GetSpec().AzureDevOps != nil:
			provider, err = azuredevops.NewAzureDevopsCommitStatusProvider(ctx, k8sClient, settingsMgr, scmProvider, *secret, scmProvider.GetSpec().AzureDevOps.Organization)
		default:
			err = errors.New("unknown scm type")
		}
		g.Expect(err).NotTo(HaveOccurred())

		for _, check := range checks {
			_, err := provider.Set(ctx, check)
			g.Expect(err).NotTo(HaveOccurred())
		}
	}, constants.EventuallyTimeout).To(Succeed())
}

// skipIfBitbucketDataCenter skips the test if the SCM target is BitBucketDataCenter
func skipIfBitbucketDataCenter() {
	if scmTarget.provider == scmserver.BitbucketDataCenter {
		Skip("BitBucket Data Center returns aggregated build stats, not named checks")
	}
}

// skipIfNotBitBucketDataCenter skips the test if the SCM target is not BitBucketDataCenter
func skipIfNotBitBucketDataCenter() {
	if scmTarget.provider != scmserver.BitbucketDataCenter {
		Skip("SCM Server does not return an aggregated check so it is not valid for these tests")
	}
}

// getEnvironmentHydratedShaFromPS returns the hydrated SHAs for each environment from the Promotion Strategy
func getEnvironmentHydratedShaFromPS(ctx context.Context, psName string) (dev, staging, prod string) {
	Eventually(func(g Gomega) {
		var ps promoterv1alpha1.PromotionStrategy
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      psName,
			Namespace: "default",
		}, &ps)
		g.Expect(err).NotTo(HaveOccurred())

		for _, env := range ps.Status.Environments {
			var err error
			switch env.Branch {
			case testBranchDevelopment:
				dev = env.Proposed.Hydrated.Sha
			case testBranchStaging:
				staging = env.Proposed.Hydrated.Sha
			case testBranchProduction:
				prod = env.Proposed.Hydrated.Sha
			default:
				err = fmt.Errorf("unknown environment %s", env.Branch)
			}
			g.Expect(err).NotTo(HaveOccurred())
		}

		g.Expect(dev).ToNot(BeEmpty())
		g.Expect(staging).ToNot(BeEmpty())
		g.Expect(prod).ToNot(BeEmpty())
	}, constants.EventuallyTimeout).To(Succeed())
	return dev, staging, prod
}

var _ = Describe("SCMCheckCommitStatus Controller", Ordered, func() {
	var (
		ctx                  context.Context
		name                 string
		scmSecret            *v1.Secret
		scmProvider          *promoterv1alpha1.ScmProvider
		gitRepo              *promoterv1alpha1.GitRepository
		promotionStrategy    *promoterv1alpha1.PromotionStrategy
		scmCheckCommitStatus *promoterv1alpha1.SCMCheckCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "scm-check-commit-status-test", "default")

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		declareDependentsSuccessfulGate(promotionStrategy)
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		createDependentsSuccessfulCommitStatus(ctx, promotionStrategy)
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

	Describe("Phase of environment is set correct along with results of the SCM Checks", func() {
		key := "scm-commit-status-phase-result-test"

		BeforeEach(func() {
			skipIfBitbucketDataCenter()

			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Creating SCMCheckCommitStatus")
			scmCheckCommitStatus = &promoterv1alpha1.SCMCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-phase-result-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.SCMCheckCommitStatusSpec{
					Key: key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, scmCheckCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmCheckCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scmCheckCommitStatus)
			}
		})

		DescribeTable("All checks set to the same state",
			func(phase promoterv1alpha1.CommitStatusPhase, expectedStatus promoterv1alpha1.SCMCheckStatus) {
				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for status test", "")

				By("Get full hydrated sha from the promotion strategy to provide to the SCM seed checks")
				devSha, stagingSha, prodSha := getEnvironmentHydratedShaFromPS(ctx, promotionStrategy.Name)
				branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
				shas := []string{devSha, stagingSha, prodSha}

				By("Seeding SCM checks")
				for i := range branches {
					checks := []*promoterv1alpha1.CommitStatus{
						createCheckCommitStatus(gitRepo, "ci/build", shas[i], phase),
						createCheckCommitStatus(gitRepo, "ci/e2e-tests", shas[i], phase),
						createCheckCommitStatus(gitRepo, "ci/lint", shas[i], phase),
					}
					seedSCMChecks(ctx, *promotionStrategy, scmProvider, scmSecret, *gitRepo, branches[i], checks)
				}

				By("Check SCMCheckCommitStatus has the correct status for each environment")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.SCMCheckCommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      scmCheckCommitStatus.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(string(phase)))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/build"),
							"Status":    Equal(expectedStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/e2e-tests"),
							"Status":    Equal(expectedStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/lint"),
							"Status":    Equal(expectedStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("all checks are passing", promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.SCMCheckStatusSucceeded),
			Entry("all checks are pending", promoterv1alpha1.CommitPhasePending, promoterv1alpha1.SCMCheckStatusPending),
			Entry("all checks have failed", promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.SCMCheckStatusFailed),
		)
	})

	Describe("Status of checks are mixed", func() {
		key := "scm-commit-status-mixed-status"

		BeforeEach(func() {
			skipIfBitbucketDataCenter()

			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Creating SCMCheckCommitStatus")
			scmCheckCommitStatus = &promoterv1alpha1.SCMCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-mixed-status",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.SCMCheckCommitStatusSpec{
					Key: key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, scmCheckCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmCheckCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scmCheckCommitStatus)
			}
		})

		DescribeTable("Mixed checks have correct priority",
			func(buildPhase, testPhase, lintPhase, envPhase promoterv1alpha1.CommitStatusPhase,
				buildStatus, testStatus, lintStatus promoterv1alpha1.SCMCheckStatus,
			) {
				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for mixed status test", "")

				By("Get full hydrated sha from the promotion strategy to provide to the SCM seed checks")
				devSha, stagingSha, prodSha := getEnvironmentHydratedShaFromPS(ctx, promotionStrategy.Name)
				branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
				shas := []string{devSha, stagingSha, prodSha}

				By("Seeding SCM checks")
				for i := range branches {
					checks := []*promoterv1alpha1.CommitStatus{
						createCheckCommitStatus(gitRepo, "ci/build", shas[i], buildPhase),
						createCheckCommitStatus(gitRepo, "ci/e2e-tests", shas[i], testPhase),
						createCheckCommitStatus(gitRepo, "ci/lint", shas[i], lintPhase),
					}
					seedSCMChecks(ctx, *promotionStrategy, scmProvider, scmSecret, *gitRepo, branches[i], checks)
				}

				By("Check SCMCheckCommitStatus has the correct status for each environment")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.SCMCheckCommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      scmCheckCommitStatus.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(string(envPhase)))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/build"),
							"Status":    Equal(buildStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/e2e-tests"),
							"Status":    Equal(testStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/lint"),
							"Status":    Equal(lintStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("Pending is set when one is pending and rest is success", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhasePending,
				promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusPending, promoterv1alpha1.SCMCheckStatusSucceeded),
			Entry("Failure is set when one has failed and rest is success", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusFailed, promoterv1alpha1.SCMCheckStatusSucceeded),
			Entry("Failure is set over success and pending", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusFailed, promoterv1alpha1.SCMCheckStatusPending),
		)
	})

	Describe("Filtering Checks", func() {
		key := "scm-commit-status-filtering-checks"

		BeforeEach(func() {
			skipIfBitbucketDataCenter()

			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Creating SCMCheckCommitStatus")
			scmCheckCommitStatus = &promoterv1alpha1.SCMCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-filtering-checks",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.SCMCheckCommitStatusSpec{
					Key: key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Filters: []string{`CheckName == "ci/e2e-tests"`},
				},
			}

			Expect(k8sClient.Create(ctx, scmCheckCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmCheckCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scmCheckCommitStatus)
			}
		})

		DescribeTable("Check that matches determines state",
			func(buildPhase, testPhase, lintPhase, envPhase promoterv1alpha1.CommitStatusPhase,
				buildStatus, testStatus, lintStatus promoterv1alpha1.SCMCheckStatus,
			) {
				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for filter checks", "")

				By("Get full hydrated sha from the promotion strategy to provide to the SCM seed checks")
				devSha, stagingSha, prodSha := getEnvironmentHydratedShaFromPS(ctx, promotionStrategy.Name)
				branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
				shas := []string{devSha, stagingSha, prodSha}

				By("Seeding SCM checks")
				for i := range branches {
					checks := []*promoterv1alpha1.CommitStatus{
						createCheckCommitStatus(gitRepo, "ci/build", shas[i], buildPhase),
						createCheckCommitStatus(gitRepo, "ci/e2e-tests", shas[i], testPhase),
						createCheckCommitStatus(gitRepo, "ci/lint", shas[i], lintPhase),
					}
					seedSCMChecks(ctx, *promotionStrategy, scmProvider, scmSecret, *gitRepo, branches[i], checks)
				}

				By("Check SCMCheckCommitStatus has the correct status for each environment")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.SCMCheckCommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      scmCheckCommitStatus.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(string(envPhase)))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/build"),
							"Status":    Equal(buildStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeFalse(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/e2e-tests"),
							"Status":    Equal(testStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
						g.Expect(env.SCMChecks).To(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("ci/lint"),
							"Status":    Equal(lintStatus),
							"SCMStatus": Not(BeEmpty()),
							"URL":       Not(BeEmpty()),
							"Matched":   BeFalse(),
						})))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("Shows success when matched one is success and others have failed", promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.SCMCheckStatusFailed, promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusFailed),
			Entry("Shows failure if the others have succeeded", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusFailed, promoterv1alpha1.SCMCheckStatusSucceeded),
			Entry("Shows pending over the other statuses", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhasePending,
				promoterv1alpha1.SCMCheckStatusSucceeded, promoterv1alpha1.SCMCheckStatusPending, promoterv1alpha1.SCMCheckStatusFailed),
		)
	})

	Describe("A Child CommitStatus gets created for each environment", func() {
		key := "child-commit-statuses"

		BeforeEach(func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Creating SCMCheckCommitStatus")
			scmCheckCommitStatus = &promoterv1alpha1.SCMCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-child-commit-statuses",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.SCMCheckCommitStatusSpec{
					Key: key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, scmCheckCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmCheckCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scmCheckCommitStatus)
			}
		})

		It("Check child commit statuses", func() {
			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for filter checks", "")

			Eventually(func(g Gomega) {
				var commitStatus promoterv1alpha1.CommitStatus
				devCSName := utils.CommitStatusResourceName(ctx, scmCheckCommitStatus, testBranchDevelopment)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: devCSName}, &commitStatus)).To(Succeed())
				g.Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				g.Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchDevelopment)))

				stageCSName := utils.CommitStatusResourceName(ctx, scmCheckCommitStatus, testBranchStaging)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: stageCSName}, &commitStatus)).To(Succeed())
				g.Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				g.Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchStaging)))

				prodCSName := utils.CommitStatusResourceName(ctx, scmCheckCommitStatus, testBranchProduction)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: prodCSName}, &commitStatus)).To(Succeed())
				g.Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				g.Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchProduction)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	// The following tests use an aggregated check instead of multiple named ones as that is a quirk of BitBucket DataCenter
	Describe("BitBucket DataCenter Tests", func() {
		key := "bitbucket-datacenter-tests"

		BeforeEach(func() {
			skipIfNotBitBucketDataCenter()

			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Creating SCMCheckCommitStatus")
			scmCheckCommitStatus = &promoterv1alpha1.SCMCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-bitbucket-datacenter-tests",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.SCMCheckCommitStatusSpec{
					Key: key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, scmCheckCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if scmCheckCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scmCheckCommitStatus)
			}
		})

		DescribeTable("All checks set to the same state",
			func(phase promoterv1alpha1.CommitStatusPhase, expectedStatus promoterv1alpha1.SCMCheckStatus) {
				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for status test", "")

				By("Get full hydrated sha from the promotion strategy to provide to the SCM seed checks")
				devSha, stagingSha, prodSha := getEnvironmentHydratedShaFromPS(ctx, promotionStrategy.Name)
				branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
				shas := []string{devSha, stagingSha, prodSha}

				By("Seeding SCM checks")
				for i := range branches {
					checks := []*promoterv1alpha1.CommitStatus{
						createCheckCommitStatus(gitRepo, "ci/build", shas[i], phase),
						createCheckCommitStatus(gitRepo, "ci/e2e-tests", shas[i], phase),
						createCheckCommitStatus(gitRepo, "ci/lint", shas[i], phase),
					}
					seedSCMChecks(ctx, *promotionStrategy, scmProvider, scmSecret, *gitRepo, branches[i], checks)
				}

				By("Check SCMCheckCommitStatus has the correct status for each environment")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.SCMCheckCommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      scmCheckCommitStatus.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(string(phase)))
						g.Expect(env.SCMChecks).To(ConsistOf(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("Bitbucket DataCenter Builds Combined Status"),
							"Status":    Equal(expectedStatus),
							"SCMStatus": Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("all checks are passing", promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.SCMCheckStatusSucceeded),
			Entry("all checks are pending", promoterv1alpha1.CommitPhasePending, promoterv1alpha1.SCMCheckStatusPending),
			Entry("all checks have failed", promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.SCMCheckStatusFailed),
		)

		DescribeTable("Mixed checks have correct priority",
			func(buildPhase, testPhase, lintPhase, envPhase promoterv1alpha1.CommitStatusPhase,
				expectedStatus promoterv1alpha1.SCMCheckStatus,
			) {
				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for mixed status test", "")

				By("Get full hydrated sha from the promotion strategy to provide to the SCM seed checks")
				devSha, stagingSha, prodSha := getEnvironmentHydratedShaFromPS(ctx, promotionStrategy.Name)
				branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
				shas := []string{devSha, stagingSha, prodSha}

				By("Seeding SCM checks")
				for i := range branches {
					checks := []*promoterv1alpha1.CommitStatus{
						createCheckCommitStatus(gitRepo, "ci/build", shas[i], buildPhase),
						createCheckCommitStatus(gitRepo, "ci/e2e-tests", shas[i], testPhase),
						createCheckCommitStatus(gitRepo, "ci/lint", shas[i], lintPhase),
					}
					seedSCMChecks(ctx, *promotionStrategy, scmProvider, scmSecret, *gitRepo, branches[i], checks)
				}

				By("Check SCMCheckCommitStatus has the correct status for each environment")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.SCMCheckCommitStatus
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      scmCheckCommitStatus.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(string(envPhase)))
						g.Expect(env.SCMChecks).To(ConsistOf(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Name":      Equal("Bitbucket DataCenter Builds Combined Status"),
							"Status":    Equal(expectedStatus),
							"SCMStatus": Not(BeEmpty()),
							"Matched":   BeTrue(),
						})))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			// BBDC aggregates via build stats: any successful count wins over in-progress; any failed wins overall.
			Entry("Success when one is pending and rest is success", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhasePending,
				promoterv1alpha1.SCMCheckStatusPending),
			Entry("Failure is set when one has failed and rest is success", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.SCMCheckStatusFailed),
			Entry("Failure is set over success and pending", promoterv1alpha1.CommitPhaseSuccess,
				promoterv1alpha1.CommitPhaseFailure, promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseFailure,
				promoterv1alpha1.SCMCheckStatusFailed),
		)
	})
})
