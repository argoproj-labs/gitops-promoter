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
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/types/tekton"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// waitForTektonRunCreation is a helper function that waits for a TaskRun or PipelineRun to be created
// by checking the TektonCommitStatus's environment status for the name of the run
// Returns the name of the run to be used later
func waitForTektonRunCreation(ctx context.Context, tcsName, branch string) string {
	var runName string
	Eventually(func(g Gomega) {
		var tcs promoterv1alpha1.TektonCommitStatus
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: tcsName}, &tcs, &client.GetOptions{})).To(Succeed())

		var envStatus promoterv1alpha1.TektonCommitStatusEnvironmentStatus
		for i := range tcs.Status.Environments {
			if tcs.Status.Environments[i].Branch == branch {
				envStatus = tcs.Status.Environments[i]
				break
			}
		}
		g.Expect(envStatus.TektonRunName).ToNot(BeEmpty())

		runName = envStatus.TektonRunName
	}, constants.EventuallyTimeout).Should(Succeed())

	return runName
}

// setTektonRunConditionStatus is a helper function that sets the condition status on the TektonRun to which ever is specified
// This ConditionStatus indicates whether the run is completed or not
func setTektonRunConditionStatus(ctx context.Context, runName, kind string, conditionStatus metav1.ConditionStatus) {
	condition := metav1.Condition{
		Type:               tekton.TektonConditionType,
		Status:             conditionStatus,
		Reason:             "TektonCommitStatusTests",
		Message:            "Updated during test",
		LastTransitionTime: metav1.Now(),
	}

	Eventually(func(g Gomega) {
		var run client.Object
		switch kind {
		case tekton.TektonKindTask:
			var taskRun tekton.TaskRun
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &taskRun)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get TaskRun %s: %v", runName, err)
			taskRun.Status.Conditions = []metav1.Condition{condition}
			run = &taskRun
		case tekton.TektonKindPipeline:
			var pipelineRun tekton.PipelineRun
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &pipelineRun)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get PipelineRun %s: %v", runName, err)
			pipelineRun.Status.Conditions = []metav1.Condition{condition}
			run = &pipelineRun
		}
		err := k8sClient.Status().Update(ctx, run)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to update Tekton Run %s of kind %s: %v", runName, kind, err)
	}, constants.EventuallyTimeout).Should(Succeed())
}

// setTektonRunResults is a helper function that sets the results on a TektonRun. When using this function to alter the results of a run
// make sure to use it before altering the condition as altering the condition will cause the rest of the reconciliation for the environment
// to happen. A types map that matches what is inside of results must be provided. If there is a missing result in types then it will
// default to a string result type
func setTektonRunResults(ctx context.Context, runName, kind string, results map[string]any, types map[string]string) {
	Eventually(func(g Gomega) {
		data := []tekton.Data{}
		for key, result := range results {
			bytes, err := json.Marshal(result)
			g.Expect(err).ToNot(HaveOccurred())

			dataType := "string"
			if t, ok := types[key]; ok {
				dataType = t
			}

			data = append(data, tekton.Data{
				Name:  key,
				Value: &apiextensionsv1.JSON{Raw: bytes},
				Type:  dataType,
			})
		}

		var run client.Object
		switch kind {
		case tekton.TektonKindTask:
			var taskRun tekton.TaskRun
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &taskRun, &client.GetOptions{})).To(Succeed())
			taskRun.Status.Results = data
			run = &taskRun
		case tekton.TektonKindPipeline:
			var pipelineRun tekton.PipelineRun
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &pipelineRun, &client.GetOptions{})).To(Succeed())
			pipelineRun.Status.Results = data
			run = &pipelineRun
		}
		g.Expect(k8sClient.Status().Update(ctx, run)).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
}

func setAllEnvironmentsTektonRunCondition(ctx context.Context, tcsName, runType string, condition metav1.ConditionStatus) {
	devRun := waitForTektonRunCreation(ctx, tcsName, testBranchDevelopment)
	setTektonRunConditionStatus(ctx, devRun, runType, condition)
	stagingRun := waitForTektonRunCreation(ctx, tcsName, testBranchStaging)
	setTektonRunConditionStatus(ctx, stagingRun, runType, condition)
	productionRun := waitForTektonRunCreation(ctx, tcsName, testBranchProduction)
	setTektonRunConditionStatus(ctx, productionRun, runType, condition)
}

var _ = Describe("TektonCommitStatus Controller", Ordered, func() {
	var (
		name                             string
		scmSecret                        *v1.Secret
		scmProvider                      *promoterv1alpha1.ScmProvider
		gitRepo                          *promoterv1alpha1.GitRepository
		promotionStrategy                *promoterv1alpha1.PromotionStrategy
		taskTCS                          *promoterv1alpha1.TektonCommitStatus
		pipelineTCS                      *promoterv1alpha1.TektonCommitStatus
		dependentsSuccessfulCommitStatus *promoterv1alpha1.DependentsSuccessfulCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "tekton-commit-status-test", "default")

		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

		dependentsSuccessfulCommitStatus = &promoterv1alpha1.DependentsSuccessfulCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{
					Name: name,
				},
				Key: promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
			},
		}
		Expect(k8sClient.Create(ctx, dependentsSuccessfulCommitStatus)).To(Succeed())
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

	Describe("Basic reconciliation for TaskRun/PipelineRuns", func() {
		taskKey := "tcs-taskrun-basic"
		pipelineKey := "tcs-pipelinerun-basic"

		BeforeEach(func() {
			By("create the TektonCommitStatus for the TaskRun")
			taskTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-taskrun-basic",
				},
				Spec: promoterv1alpha1.TektonCommitStatusSpec{
					Key: taskKey,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					TektonResource: promoterv1alpha1.TektonResourceRef{
						Kind:           tekton.TektonKindTask,
						Name:           "test-task",
						ServiceAccount: "default",
					},
				},
			}
			Expect(k8sClient.Create(ctx, taskTCS)).To(Succeed())

			By("create the TektonCommitStatus for the PipelineRun")
			pipelineTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-pipelinerun-basic",
				},
				Spec: promoterv1alpha1.TektonCommitStatusSpec{
					Key: pipelineKey,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					TektonResource: promoterv1alpha1.TektonResourceRef{
						Kind:           tekton.TektonKindPipeline,
						Name:           "test-pipeline",
						ServiceAccount: "default",
					},
				},
			}
			Expect(k8sClient.Create(ctx, pipelineTCS)).To(Succeed())
		})

		AfterEach(func() {
			if taskTCS != nil {
				_ = k8sClient.Delete(ctx, taskTCS)
			}
			if pipelineTCS != nil {
				_ = k8sClient.Delete(ctx, pipelineTCS)
			}
			Expect(k8sClient.DeleteAllOf(ctx, &tekton.TaskRun{}, client.InNamespace("default"))).To(Succeed())
			Expect(k8sClient.DeleteAllOf(ctx, &tekton.PipelineRun{}, client.InNamespace("default"))).To(Succeed())
		})

		It("Should create a TaskRun and report success on all environments once the TaskRun is finished", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, taskKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs taskrun basic test for success", "")

			By("Set conditions for TaskRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, taskTCS.Name, tekton.TektonKindTask, metav1.ConditionTrue)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      taskTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "Environment "+env.Branch+" should report success")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseSucceeded), "TektonRuns phase should be Succeeded")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("Should create a TaskRun and report fail on all environments once the TaskRun has failed", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, taskKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs taskrun basic test for failure", "")

			By("Set conditions for TaskRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, taskTCS.Name, tekton.TektonKindTask, metav1.ConditionFalse)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      taskTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure), "Environment "+env.Branch+" should report failure")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseFailed), "TektonRuns phase should be Failed")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("Should create a TaskRun and report pending on all environments when the TaskRun is running", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, taskKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs taskrun basic test for pending", "")

			By("Set conditions for TaskRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, taskTCS.Name, tekton.TektonKindTask, metav1.ConditionUnknown)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      taskTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "Environment "+env.Branch+" should report pending")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseRunning), "TektonRuns phase should be Running")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("Should create a PipelineRun and report success on all environments once their PipelineRun is finished", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, pipelineKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs pipelinerun basic test for success", "")

			By("Set conditions for PipelineRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, pipelineTCS.Name, tekton.TektonKindPipeline, metav1.ConditionTrue)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pipelineTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "Environment "+env.Branch+" should report success")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseSucceeded), "TektonRuns phase should be Succeeded")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("Should create a PipelineRun and report failed on all environments once their PipelineRun has failed", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, pipelineKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs pipelinerun basic test for failure", "")

			By("Set conditions for PipelineRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, pipelineTCS.Name, tekton.TektonKindPipeline, metav1.ConditionFalse)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pipelineTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure), "Environment "+env.Branch+" should report failure")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseFailed), "TektonRuns phase should be Failed")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("Should create a PipelineRun and report pending on all environments while the PipelineRun is running", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, pipelineKey)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs pipelinerun basic test for pending", "")

			By("Set conditions for PipelineRuns created for all environments")
			setAllEnvironmentsTektonRunCondition(ctx, pipelineTCS.Name, tekton.TektonKindPipeline, metav1.ConditionUnknown)

			By("Waiting for TektonCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pipelineTCS.Name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have a status for all 3 environments
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				// Check that each environment is in the phase of succeeded
				for _, env := range tcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "Environment "+env.Branch+" should report pending")
					g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseRunning), "TektonRuns phase should be Running")
					g.Expect(env.TargetedSha).ToNot(BeEmpty(), "TargetedSha should be populated")
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
