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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/DependentsSuccessfulCommitStatus.yaml
var testDependentsSuccessfulCommitStatusYAML string

// dagEnvs builds a DependentEnvironment slice from alternating (branch, dependsOn) pairs so tests can
// declare a graph compactly. dependsOn is a comma-joined string, empty for a graph root.
func dagEnvs(pairs ...string) []promoterv1alpha1.DependentEnvironment {
	out := make([]promoterv1alpha1.DependentEnvironment, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		var dependsOn []string
		if pairs[i+1] != "" {
			dependsOn = strings.Split(pairs[i+1], ",")
		}
		out = append(out, promoterv1alpha1.DependentEnvironment{Branch: pairs[i], DependsOn: dependsOn})
	}
	return out
}

// dagEnvStatus builds an EnvironmentStatus for the upstreamsPending tests. activeDry is the dry SHA
// the branch has merged and deployed; hydratedDry is the dry SHA its hydrator has processed
// (Proposed.Dry.Sha); healthy toggles the active argocd-health commit status.
func dagEnvStatus(branch, activeDry, hydratedDry string, healthy bool, commitTime time.Time) promoterv1alpha1.EnvironmentStatus {
	phase := "success"
	if !healthy {
		phase = "pending"
	}
	return promoterv1alpha1.EnvironmentStatus{
		Branch: branch,
		Active: promoterv1alpha1.CommitBranchState{
			Dry: promoterv1alpha1.CommitShaState{Sha: activeDry, CommitTime: metav1.NewTime(commitTime)},
			CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "argocd-health", Phase: phase},
			},
		},
		Proposed: promoterv1alpha1.CommitBranchState{
			Dry: promoterv1alpha1.CommitShaState{Sha: hydratedDry},
		},
	}
}

// dagEnvStatusWithNote is like dagEnvStatus but also sets the hydrator git note. The note dry SHA
// is what getEffectiveHydratedDrySha treats as the branch's effective hydrated dry, so when it
// differs from Proposed.Dry.Sha the branch is a no-op for that SHA (the note advanced without a new
// hydrated commit). This is required to exercise isUpstreamPending's no-op recursion, which the
// note-less dagEnvStatus cannot reach.
//
//nolint:unparam // branch is always "stg" in current tests but kept for consistency with dagEnvStatus
func dagEnvStatusWithNote(branch, activeDry, proposedDry, noteDry string, healthy bool, commitTime time.Time) promoterv1alpha1.EnvironmentStatus {
	envStatus := dagEnvStatus(branch, activeDry, proposedDry, healthy, commitTime)
	if noteDry != "" {
		envStatus.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: noteDry}
	}
	return envStatus
}

var _ = Describe("DependentsSuccessfulCommitStatus Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the DependentsSuccessfulCommitStatus resource", func() {
			err := unmarshalYamlStrict(testDependentsSuccessfulCommitStatusYAML, &promoterv1alpha1.DependentsSuccessfulCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When the PromotionStrategy is missing", func() {
		var (
			ctx                              context.Context
			dependentsSuccessfulCommitStatus *promoterv1alpha1.DependentsSuccessfulCommitStatus
		)

		BeforeEach(func() {
			ctx = context.Background()
			By("Creating a DependentsSuccessfulCommitStatus that references a non-existent PromotionStrategy")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dag-missing-ps",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "non-existent"},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
					Environments: []promoterv1alpha1.DependentEnvironment{
						{Branch: testBranchDevelopment},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, dependentsSuccessfulCommitStatus)
		})

		It("should set Ready=False when the PromotionStrategy is not found", func() {
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When reconciling against a PromotionStrategy", func() {
		var (
			ctx                              context.Context
			name                             string
			scmSecret                        *v1.Secret
			scmProvider                      *promoterv1alpha1.ScmProvider
			gitRepo                          *promoterv1alpha1.GitRepository
			promotionStrategy                *promoterv1alpha1.PromotionStrategy
			dependentsSuccessfulCommitStatus *promoterv1alpha1.DependentsSuccessfulCommitStatus
		)

		BeforeEach(func() {
			ctx = context.Background()
			dependentsSuccessfulCommitStatus = nil

			By("Setting up test git repository and PromotionStrategy")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "dependents-successful-commit-status-controller-test", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: promoterv1alpha1.DependentsSuccessfulCommitStatusKey},
			}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up test resources")
			if dependentsSuccessfulCommitStatus != nil {
				_ = k8sClient.Delete(ctx, dependentsSuccessfulCommitStatus)
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

		It("should render url.template onto per-environment CommitStatuses", func() {
			By("Creating a DependentsSuccessfulCommitStatus with a URL template that includes the environment")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-dag-url",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
					Environments: []promoterv1alpha1.DependentEnvironment{
						{Branch: testBranchDevelopment},
						{Branch: testBranchStaging, DependsOn: []string{testBranchDevelopment}},
						{Branch: testBranchProduction, DependsOn: []string{testBranchStaging}},
					},
					URL: promoterv1alpha1.URLConfig{
						Template: "https://example.com/ui?env={{ .Environment }}",
					},
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())

			By("Waiting for the DependentsSuccessfulCommitStatus to become Ready")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a proposed change so the DAG writes CommitStatuses")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
			makeChangeAndHydrateRepo(gitPath, gitRepo, "url template test change", "")

			By("Checking that each environment CommitStatus has the rendered URL")
			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				Eventually(func(g Gomega) {
					cs := &promoterv1alpha1.CommitStatus{}
					csName := utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, branch)
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
					g.Expect(cs.Spec.Url).To(Equal("https://example.com/ui?env=" + branch))
				}, constants.EventuallyTimeout).Should(Succeed())
			}
		})

		It("should infer a linear dependency chain when spec.environments is empty", func() {
			By("Creating a DependentsSuccessfulCommitStatus with no spec.environments")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-linear-default",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())

			By("Waiting for the DependentsSuccessfulCommitStatus to become Ready")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a proposed change so the inferred linear chain writes CommitStatuses")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
			makeChangeAndHydrateRepo(gitPath, gitRepo, "linear default test change", "")

			By("Checking CommitStatuses are created for all PromotionStrategy environments from the inferred linear chain")
			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				Eventually(func(g Gomega) {
					cs := &promoterv1alpha1.CommitStatus{}
					csName := utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, branch)
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
					g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(promoterv1alpha1.DependentsSuccessfulCommitStatusKey))
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			By("Checking the root environment gate succeeds with no upstream dependencies")
			Eventually(func(g Gomega) {
				devCS := &promoterv1alpha1.CommitStatus{}
				devName := utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, testBranchDevelopment)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: devName}, devCS)).To(Succeed())
				g.Expect(devCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should set Ready=False when declared branches do not match the PromotionStrategy", func() {
			By("Creating a DependentsSuccessfulCommitStatus that declares a branch not on the PromotionStrategy")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-dag-mismatch",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
					Environments: []promoterv1alpha1.DependentEnvironment{
						{Branch: testBranchDevelopment},
						{Branch: testBranchStaging, DependsOn: []string{testBranchDevelopment}},
						{Branch: "environment/ghost", DependsOn: []string{testBranchStaging}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())

			By("Waiting for Ready=False from environment validation")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Message).To(ContainSubstring(`declares branch "environment/ghost"`))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should set Ready=False when the dependency graph contains a cycle", func() {
			By("Creating a DependentsSuccessfulCommitStatus whose environments form a dependency cycle")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-dag-cycle",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
					// Branches still match the PromotionStrategy, but staging⇄production cycle.
					Environments: []promoterv1alpha1.DependentEnvironment{
						{Branch: testBranchDevelopment},
						{Branch: testBranchStaging, DependsOn: []string{testBranchProduction}},
						{Branch: testBranchProduction, DependsOn: []string{testBranchStaging}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())

			By("Waiting for Ready=False from graph validation")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should cleanup orphaned CommitStatus resources when environments are removed", func() {
			By("Creating a DependentsSuccessfulCommitStatus tracking all three environments")
			dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-dag-cleanup",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
					Environments: []promoterv1alpha1.DependentEnvironment{
						{Branch: testBranchDevelopment},
						{Branch: testBranchStaging, DependsOn: []string{testBranchDevelopment}},
						{Branch: testBranchProduction, DependsOn: []string{testBranchStaging}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())

			By("Waiting for the DependentsSuccessfulCommitStatus to become Ready")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a proposed change so the DAG writes CommitStatuses")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
			makeChangeAndHydrateRepo(gitPath, gitRepo, "cleanup test change", "")

			By("Waiting for all three CommitStatus resources to be created")
			var (
				oldCommitStatusDevName     string
				oldCommitStatusStagingName string
				oldCommitStatusProdName    string
			)
			Eventually(func(g Gomega) {
				oldCommitStatusDevName = utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, testBranchDevelopment)
				oldCommitStatusStagingName = utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, testBranchStaging)
				oldCommitStatusProdName = utils.CommitStatusResourceName(ctx, dependentsSuccessfulCommitStatus, testBranchProduction)

				for _, csName := range []string{oldCommitStatusDevName, oldCommitStatusStagingName, oldCommitStatusProdName} {
					cs := &promoterv1alpha1.CommitStatus{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Shrinking PromotionStrategy and DependentsSuccessfulCommitStatus to development + staging together")
			// DAG requires an exact environment match with the PromotionStrategy, so both must be
			// updated together; otherwise reconcile fails before orphan cleanup runs.
			Eventually(func(g Gomega) {
				ps := &promoterv1alpha1.PromotionStrategy{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(promotionStrategy), ps)).To(Succeed())
				ps.Spec.Environments = []promoterv1alpha1.Environment{
					{Branch: testBranchDevelopment},
					{Branch: testBranchStaging},
				}
				g.Expect(k8sClient.Update(ctx, ps)).To(Succeed())

				dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependentsSuccessfulCommitStatus), dcs)).To(Succeed())
				dcs.Spec.Environments = []promoterv1alpha1.DependentEnvironment{
					{Branch: testBranchDevelopment},
					{Branch: testBranchStaging, DependsOn: []string{testBranchDevelopment}},
				}
				g.Expect(k8sClient.Update(ctx, dcs)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying development and staging CommitStatuses still exist")
			Eventually(func(g Gomega) {
				for _, csName := range []string{oldCommitStatusDevName, oldCommitStatusStagingName} {
					cs := &promoterv1alpha1.CommitStatus{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the production CommitStatus is deleted as an orphan")
			Eventually(func(g Gomega) {
				cs := &promoterv1alpha1.CommitStatus{}
				err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: oldCommitStatusProdName}, cs)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "production CommitStatus should be deleted")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("DAG URL template helpers", func() {
	Describe("dependsOnForBranch", func() {
		It("returns the dependsOn list for a declared branch", func() {
			dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					Environments: dagEnvs("dev", "", "e2e", "dev", "prod", "e2e,perf"),
				},
			}
			Expect(dependsOnForBranch(dcs, "prod")).To(Equal([]string{"e2e", "perf"}))
			Expect(dependsOnForBranch(dcs, "dev")).To(BeEmpty())
		})

		It("returns nil for an unknown branch", func() {
			dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					Environments: dagEnvs("dev", ""),
				},
			}
			Expect(dependsOnForBranch(dcs, "missing")).To(BeNil())
		})
	})

	Describe("buildDependsOnQuery", func() {
		It("returns empty for a root with no dependsOn", func() {
			Expect(buildDependsOnQuery(nil)).To(Equal(""))
			Expect(buildDependsOnQuery([]string{})).To(Equal(""))
		})

		It("encodes a single upstream as env=", func() {
			Expect(buildDependsOnQuery([]string{"environment/dev"})).To(Equal("env=environment%2Fdev"))
		})

		It("encodes fan-in upstreams as repeated env=", func() {
			Expect(buildDependsOnQuery([]string{"environment/e2e", "environment/perf"})).
				To(Equal("env=environment%2Fe2e&env=environment%2Fperf"))
		})
	})
})

var _ = Describe("resolveDependentEnvironments", func() {
	It("returns spec.environments when set", func() {
		explicit := []promoterv1alpha1.DependentEnvironment{
			{Branch: "dev"},
			{Branch: "prd", DependsOn: []string{"dev"}},
		}
		dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
			Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{Environments: explicit},
		}
		ps := &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{
					{Branch: "dev"},
					{Branch: "stg"},
					{Branch: "prd"},
				},
			},
		}
		envs, err := resolveDependentEnvironments(dcs, ps)
		Expect(err).NotTo(HaveOccurred())
		Expect(envs).To(Equal(explicit))
	})

	It("infers a linear chain when spec.environments is empty", func() {
		dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Spec:       promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{},
		}
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "demo-ps"},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{
					{Branch: "dev"},
					{Branch: "stg"},
					{Branch: "prd"},
				},
			},
		}
		envs, err := resolveDependentEnvironments(dcs, ps)
		Expect(err).NotTo(HaveOccurred())
		Expect(envs).To(Equal([]promoterv1alpha1.DependentEnvironment{
			{Branch: "dev"},
			{Branch: "stg", DependsOn: []string{"dev"}},
			{Branch: "prd", DependsOn: []string{"stg"}},
		}))
	})

	It("errors when both spec.environments and PromotionStrategy environments are empty", func() {
		dcs := &promoterv1alpha1.DependentsSuccessfulCommitStatus{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}
		ps := &promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: "demo-ps"}}
		_, err := resolveDependentEnvironments(dcs, ps)
		Expect(err).To(MatchError(ContainSubstring("no environments to infer")))
	})
})

var _ = Describe("DAG graph logic", func() {
	Describe("buildDAG", func() {
		It("builds a graph preserving spec order", func() {
			g, err := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.branches).To(Equal([]string{"dev", "stg", "prd"}))
			Expect(g.dependsOn["stg"]).To(Equal([]string{"dev"}))
			Expect(g.dependsOn["dev"]).To(BeEmpty())
		})

		It("rejects a duplicate branch", func() {
			_, err := buildDAG(dagEnvs("dev", "", "dev", ""))
			Expect(err).To(MatchError(ContainSubstring("duplicate branch")))
		})
	})

	Describe("validateDAG", func() {
		It("accepts dependsOn that reference declared branches", func() {
			g, err := buildDAG(dagEnvs("dev", "", "prd", "dev"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateDAG()).To(Succeed())
		})

		It("rejects dependsOn referencing an unknown branch", func() {
			g, err := buildDAG(dagEnvs("dev", "", "prd", "stg"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateDAG()).To(MatchError(ContainSubstring("unknown branch")))
		})

		It("accepts a diamond where every upstream is declared", func() {
			g, err := buildDAG(dagEnvs("dev", "", "stg-us", "dev", "stg-eu", "dev", "prd", "stg-us,stg-eu"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateDAG()).To(Succeed())
		})

		It("detects a cycle between two branches", func() {
			g, _ := buildDAG(dagEnvs("a", "b", "b", "a"))
			Expect(g.validateDAG()).To(MatchError(ContainSubstring("cycle")))
		})

		It("detects a self-dependency cycle", func() {
			g, _ := buildDAG(dagEnvs("a", "a"))
			Expect(g.validateDAG()).To(MatchError(ContainSubstring("cycle")))
		})
	})

	Describe("checkCommitStatusesPassing", func() {
		It("returns not pending when all commit statuses are passing", func() {
			pending, reason := checkCommitStatusesPassing([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				{Key: "smoke", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
			}, "environment/dev")
			Expect(pending).To(BeFalse())
			Expect(reason).To(BeEmpty())
		})

		It("returns a single-key reason when one commit status is not passing", func() {
			pending, reason := checkCommitStatusesPassing([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "health", Phase: string(promoterv1alpha1.CommitPhasePending)},
			}, "environment/staging")
			Expect(pending).To(BeTrue())
			Expect(reason).To(Equal(`Waiting for "environment/staging" environment's "health" commit status to be successful`))
		})

		It("returns a plural reason when multiple commit statuses are not passing", func() {
			pending, reason := checkCommitStatusesPassing([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "health", Phase: string(promoterv1alpha1.CommitPhasePending)},
				{Key: "smoke", Phase: string(promoterv1alpha1.CommitPhasePending)},
			}, "environment/staging")
			Expect(pending).To(BeTrue())
			Expect(reason).To(Equal(`Waiting for "environment/staging" environment's commit statuses to be successful`))
		})

		It("uses previous environment wording when branch is empty", func() {
			pending, reason := checkCommitStatusesPassing([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "health", Phase: string(promoterv1alpha1.CommitPhasePending)},
			}, "")
			Expect(pending).To(BeTrue())
			Expect(reason).To(Equal(`Waiting for previous environment's "health" commit status to be successful`))
		})
	})

	Describe("validateEnvironmentsMatchPS", func() {
		psWithBranches := func(name string, branches ...string) *promoterv1alpha1.PromotionStrategy {
			ps := &promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: name}}
			for _, branch := range branches {
				ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: branch})
			}
			return ps
		}

		It("accepts when DAG branches exactly match the PromotionStrategy", func() {
			g, err := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateEnvironmentsMatchPS("demo-dag", psWithBranches("demo-ps", "dev", "stg", "prd"))).To(Succeed())
		})

		It("rejects a DAG branch that is not in the PromotionStrategy", func() {
			g, err := buildDAG(dagEnvs("dev", "", "ghost", "dev"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateEnvironmentsMatchPS("demo-dag", psWithBranches("demo-ps", "dev"))).To(MatchError(ContainSubstring(
				`declares branch "ghost", but PromotionStrategy "demo-ps" has no such environment`)))
		})

		It("rejects when the DAG is missing PromotionStrategy environment branches", func() {
			g, err := buildDAG(dagEnvs("dev", ""))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateEnvironmentsMatchPS("demo-dag", psWithBranches("demo-ps", "dev", "prd", "stg"))).To(MatchError(ContainSubstring(
				`missing PromotionStrategy "demo-ps" environment branches: prd, stg`)))
		})
	})

	// upstreamsPending runs the following truth table against every dependsOn upstream (a fan-in
	// passes only when all upstreams are satisfied; a linear chain is the single-upstream case). The
	// logic is a direct port of the DependentsSuccessfulCommitStatus controller's linear
	// upstreamsPending (legacy linear), generalized to a DAG.
	//
	// Truth table for isUpstreamPending (per upstream):
	// | Hydrated | NoOp | Pending | Merged | Healthy | Result |
	// |----------|------|---------|--------|---------|--------|
	// | N        | -    | -       | -      | -       | BLOCK (hydrator) |
	// | Y        | N    | -       | N      | -       | BLOCK (waiting for promotion) |
	// | Y        | N    | -       | Y      | N       | BLOCK (commit status) |
	// | Y        | N    | -       | Y      | Y       | ALLOW |
	// | Y        | Y    | Y       | -      | -       | BLOCK (pending changes from previous commit) |
	// | Y        | Y    | N       | -      | N       | BLOCK (commit status on no-op env) |
	// | Y        | Y    | N       | -      | Y       | RECURSE (or ALLOW if base case) |
	//
	// Each It below is annotated with the Case it covers.
	Describe("upstreamsPending", func() {
		const (
			oldDry = "old-dry-sha"
			midDry = "mid-dry-sha"
			newDry = "new-dry-sha"
		)
		old := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
		newer := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

		// linear chain: dev -> stg -> prd
		linear := func() *dag { g, _ := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg")); return g }
		// diamond: dev -> {e2e, perf} -> prd
		diamond := func() *dag {
			g, _ := buildDAG(dagEnvs("dev", "", "e2e", "dev", "perf", "dev", "prd", "e2e,perf"))
			return g
		}

		// Case 2 (hydrated, not no-op, not merged): prd is promoting newDry, but its upstream stg is
		// still on oldDry (healthy from a prior round) and has NOT taken newDry. prd must stay
		// pending — otherwise the new change merges ahead of its upstream, breaking DAG ordering.
		It("holds pending when an upstream is healthy on an OLD dry and has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 1 (not hydrated): stg's hydrator is still on oldDry (Proposed.Dry = oldDry), so it
		// has not even produced the target dry yet.
		It("holds pending when the upstream's hydrator has not yet processed the target dry", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, oldDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 4 (hydrated, not no-op, merged, healthy): the ALLOW row.
		It("is ready when the upstream merged the target dry and is healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Case 3 (hydrated, not no-op, merged, not healthy): BLOCK on commit status.
		It("holds pending when the upstream merged the target dry but is not healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, false, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 7 base case (RECURSE bottoms out at a graph root): a node with no upstreams is
		// always ready.
		It("is ready for a root that has no upstreams", func() {
			pending, _ := upstreamsPending(linear(), "dev", newDry, metav1.NewTime(newer), map[string]promoterv1alpha1.EnvironmentStatus{})
			Expect(pending).To(BeFalse())
		})

		// Fan-in (DAG generalization): with multiple upstreams, any one
		// unsatisfied upstream blocks — here perf has not promoted the target (Case 2 per-upstream).
		It("fan-in: holds pending when one upstream has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Fan-in (DAG generalization): all upstreams satisfied (Case 4 each) → the whole fan-in
		// passes.
		It("fan-in: is ready when both upstreams merged the target dry and are healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Fan-in pending reason comes from the first unsatisfied upstream. The
		// "not promoted" path still uses the legacy generic message (no branch name).
		It("fan-in: returns the blocking upstream's pending reason", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", oldDry, newDry, true, old),
			}
			pending, reason := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(Equal("Waiting for previous environment to be promoted"))
		})

		// Case 7 (hydrated, no-op, no pending changes, healthy → RECURSE): a clean, healthy no-op
		// upstream (its git note advanced to the target dry without a new hydrated commit) must be
		// transparently skipped by recursing into its own upstreams. Here stg is a healthy no-op for
		// newDry and its upstream dev has merged newDry and is healthy, so prd is ready.
		It("no-op recursion: ready when a healthy no-op upstream's own upstream is ready", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				// stg: note advanced to newDry, but active == proposed == oldDry (no new commit).
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, true, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Case 7 (RECURSE), negative side: recursion through a healthy no-op still blocks when the
		// deeper upstream is not satisfied — stg is a healthy no-op, but dev has not promoted newDry
		// yet. This proves the recursion actually happens (see the short-circuit reverse-test).
		It("no-op recursion: pending when a healthy no-op upstream's own upstream is not ready", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, true, old),
				"dev": dagEnvStatus("dev", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 6 (hydrated, no-op, no pending changes, not healthy → BLOCK): a no-op upstream is
		// only skippable if it is itself healthy. An unhealthy no-op blocks (no recursion) even
		// though it carries no real change of its own — this is the scenario that previously caused
		// premature promotions.
		It("no-op recursion: pending when the no-op upstream itself is unhealthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, false, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("argocd-health"))
		})

		// Case 5 (hydrated, no-op for the target, but has its own pending change → BLOCK): a previous
		// commit (midDry) changed stg and its PR is not yet merged (active still oldDry), while the
		// target (newDry) is a no-op for stg (its note advanced to newDry without a new commit). stg
		// is a no-op for the target yet still has an in-flight change of its own, so it must block
		// rather than be recursed past. Mirrors the old upstreamsPending (legacy linear) Case 5 test
		// (active=OLD, proposed=COMMIT1, note=COMMIT2).
		It("no-op recursion: pending when a no-op upstream has its own pending change", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				// stg: active = oldDry (PR from the previous change not merged), proposed = midDry
				// (that previous change), note = newDry = target (a no-op for stg). So it IS a no-op
				// (note != proposed) AND has a pending change (active != proposed).
				"stg": dagEnvStatusWithNote("stg", oldDry, midDry, newDry, true, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("Waiting for previous environment to be promoted"))
		})

		// Case 4, commit-time sub-check: an upstream that has merged the target dry but whose commit
		// is older than the current environment's active commit must block — otherwise the current
		// environment would promote ahead of an upstream that has not caught up in time ordering.
		It("commit-time ordering: pending when the upstream's merged commit is older than current", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, true, old),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("older"))
		})

		// Fan-in over unequal-length paths (DAG generalization, no linear-table equivalent):
		// dev -> fast -> prd (short path) and dev -> canary -> soak -> prd (long path). prd must
		// wait for the SLOW path: even when the short path (fast) is fully satisfied, an unsatisfied
		// node on the long path (soak) keeps prd pending.
		unevenDiamond := func() *dag {
			g, _ := buildDAG(dagEnvs(
				"dev", "",
				"fast", "dev",
				"canary", "dev",
				"soak", "canary",
				"prd", "fast,soak",
			))
			return g
		}
		It("uneven diamond: pending when the long path is not ready even though the short path is", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"fast": dagEnvStatus("fast", newDry, newDry, true, newer),
				"soak": dagEnvStatus("soak", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(unevenDiamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})
		It("uneven diamond: ready when both paths have promoted the target and are healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"fast": dagEnvStatus("fast", newDry, newDry, true, newer),
				"soak": dagEnvStatus("soak", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(unevenDiamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})
	})
})
