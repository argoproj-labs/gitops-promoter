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
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
		default:
			GinkgoWriter.Printf("Unknown kind of %s for Tekton resource", kind)
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
		data := make([]tekton.Data, 0, len(results))
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

		condition := metav1.Condition{
			Type:               tekton.TektonConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             "TektonCommitStatusTests",
			Message:            "Updated during test",
			LastTransitionTime: metav1.Now(),
		}

		var run client.Object
		switch kind {
		case tekton.TektonKindTask:
			var taskRun tekton.TaskRun
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &taskRun, &client.GetOptions{})).To(Succeed())
			taskRun.Status.Results = data
			taskRun.Status.Conditions = []metav1.Condition{condition}
			run = &taskRun
		case tekton.TektonKindPipeline:
			var pipelineRun tekton.PipelineRun
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: runName}, &pipelineRun, &client.GetOptions{})).To(Succeed())
			pipelineRun.Status.Results = data
			pipelineRun.Status.Conditions = []metav1.Condition{condition}
			run = &pipelineRun
		default:
			GinkgoWriter.Printf("Unknown kind of %s for Tekton resource", kind)
		}
		g.Expect(k8sClient.Status().Update(ctx, run)).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
}

// setAllEnvironmentsTektonRunCondition is a helper function that waits for the creation of the Tekton runs in all environments
// and updates their status to what is passed.
func setAllEnvironmentsTektonRunCondition(ctx context.Context, tcsName, runType string, condition metav1.ConditionStatus) {
	devRun := waitForTektonRunCreation(ctx, tcsName, testBranchDevelopment)
	setTektonRunConditionStatus(ctx, devRun, runType, condition)
	stagingRun := waitForTektonRunCreation(ctx, tcsName, testBranchStaging)
	setTektonRunConditionStatus(ctx, stagingRun, runType, condition)
	productionRun := waitForTektonRunCreation(ctx, tcsName, testBranchProduction)
	setTektonRunConditionStatus(ctx, productionRun, runType, condition)
}

// setAllEnvironmentsTektonRunResults is a helper function that waits for creation of the Tekton runs in all environments and sets
// the results for each run.
func setAllEnvironmentsTektonRunResults(ctx context.Context, tcsName, runType string, results map[string]any, resultTypes map[string]string) {
	devRun := waitForTektonRunCreation(ctx, tcsName, testBranchDevelopment)
	setTektonRunResults(ctx, devRun, runType, results, resultTypes)

	stagingRun := waitForTektonRunCreation(ctx, tcsName, testBranchStaging)
	setTektonRunResults(ctx, stagingRun, runType, results, resultTypes)

	productionRun := waitForTektonRunCreation(ctx, tcsName, testBranchProduction)
	setTektonRunResults(ctx, productionRun, runType, results, resultTypes)
}

// setExpectedResultsOnTCS is a helper function that recreates the supplied TCS with the expected results set
func setExpectedResultsOnTCS(ctx context.Context, tcsName string, expectedResults []promoterv1alpha1.ExpectedResult, clearExpression bool) {
	Eventually(ctx, func(g Gomega) {
		var tcs promoterv1alpha1.TektonCommitStatus
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      tcsName,
			Namespace: "default",
		}, &tcs)
		Expect(err).NotTo(HaveOccurred())
		tcs.Spec.ExpectedResults = expectedResults
		if clearExpression {
			tcs.Spec.Expression = ""
		}
		// Delete the TCS as it was causing an error where object was modified when trying to update
		// so recreate it
		Expect(k8sClient.Delete(ctx, &tcs)).To(Succeed())
		newTCS := promoterv1alpha1.TektonCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      tcs.Name,
			},
			Spec: promoterv1alpha1.TektonCommitStatusSpec{
				Key:                  tcs.Spec.Key,
				PromotionStrategyRef: tcs.Spec.PromotionStrategyRef,
				TektonResource:       tcs.Spec.TektonResource,
				Expression:           tcs.Spec.Expression,
				ExpectedResults:      tcs.Spec.ExpectedResults,
			},
		}
		Expect(k8sClient.Create(ctx, &newTCS)).To(Succeed())
	}, constants.EventuallyTimeout).To(Succeed())
}

// UnmarshalJSONDataToString converts JSON bytes to a string
func UnmarshalJSONDataToString(data *apiextensionsv1.JSON) string {
	var s string
	err := json.Unmarshal(data.Raw, &s)
	Expect(err).ToNot(HaveOccurred())
	return s
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

	// Helper functions to be used in table tests
	getTaskTCS := func() *promoterv1alpha1.TektonCommitStatus { return taskTCS }
	getPipelineTCS := func() *promoterv1alpha1.TektonCommitStatus { return pipelineTCS }

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

	Describe("Basic reconciliation with TaskRun/PipelineRuns", func() {
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

		DescribeTable("TCS should report the correct phase",
			func(kind, key string, condition metav1.ConditionStatus, expectedPhase promoterv1alpha1.CommitStatusPhase,
				expectedTektonPhase string, getTCS func() *promoterv1alpha1.TektonCommitStatus,
			) {
				tcs := getTCS()
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs basic test", "")

				By("Set conditions for TaskRuns created for all environments")
				setAllEnvironmentsTektonRunCondition(ctx, tcs.Name, kind, condition)

				By("Waiting for TektonCommitStatus to process all environments")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.Phase).To(Equal(expectedPhase))
						g.Expect(env.TektonRunPhase).To(Equal(expectedTektonPhase))
						g.Expect(env.TargetedSha).ToNot(BeEmpty())
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TCS should report success when TaskRun completes",
				tekton.TektonKindTask,
				taskKey,
				metav1.ConditionTrue,
				promoterv1alpha1.CommitPhaseSuccess,
				tekton.TektonRunPhaseSucceeded,
				getTaskTCS),
			Entry("TCS should report failure when TaskRun fails",
				tekton.TektonKindTask,
				taskKey,
				metav1.ConditionFalse,
				promoterv1alpha1.CommitPhaseFailure,
				tekton.TektonRunPhaseFailed,
				getTaskTCS),
			Entry("TCS should report pending when TaskRun is running",
				tekton.TektonKindTask,
				taskKey,
				metav1.ConditionUnknown,
				promoterv1alpha1.CommitPhasePending,
				tekton.TektonRunPhaseRunning,
				getTaskTCS),
			Entry("TCS should report success when PipelineRun completes",
				tekton.TektonKindPipeline,
				pipelineKey,
				metav1.ConditionTrue,
				promoterv1alpha1.CommitPhaseSuccess,
				tekton.TektonRunPhaseSucceeded,
				getPipelineTCS),
			Entry("TCS should report failure when PipelineRun fails",
				tekton.TektonKindPipeline,
				pipelineKey,
				metav1.ConditionFalse,
				promoterv1alpha1.CommitPhaseFailure,
				tekton.TektonRunPhaseFailed,
				getPipelineTCS),
			Entry("TCS should report pending when PipelineRun is running",
				tekton.TektonKindPipeline,
				pipelineKey,
				metav1.ConditionUnknown,
				promoterv1alpha1.CommitPhasePending,
				tekton.TektonRunPhaseRunning,
				getPipelineTCS))
	})

	Describe("Child CommitStatuses get created by a TektonCommitStatus", func() {
		key := "tcs-child-commitstatus-test"

		BeforeEach(func() {
			By("create the TektonCommitStatus")
			taskTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-child-commitstatus-test",
				},
				Spec: promoterv1alpha1.TektonCommitStatusSpec{
					Key: key,
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
		})

		It("Child CommitStatuses get created by the TektonCommitStatus", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs test for child commit statuses", "")

			Eventually(func(g Gomega) {
				var commitStatus promoterv1alpha1.CommitStatus
				devCSName := utils.CommitStatusResourceName(ctx, taskTCS, testBranchDevelopment)
				Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: devCSName}, &commitStatus)).To(Succeed())
				Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchDevelopment)))

				stageCSName := utils.CommitStatusResourceName(ctx, taskTCS, testBranchStaging)
				Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: stageCSName}, &commitStatus)).To(Succeed())
				Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchStaging)))

				prodCSName := utils.CommitStatusResourceName(ctx, taskTCS, testBranchProduction)
				Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: prodCSName}, &commitStatus)).To(Succeed())
				Expect(commitStatus.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(key))
				Expect(commitStatus.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(testBranchProduction)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Passing parameters to a TaskRun/PipelineRun", func() {
		taskKey := "tcs-taskrun-parameters"
		pipelineKey := "tcs-pipelinerun-parameters"

		arrayStr := `["linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"]`
		templatedArrayStr := `["{{ .TargetedSha }}", "{{ .Branch }}"]`
		objectStr := `{"timeout": 300, "retries": 3, "verbose": true}`
		templatedObjectStr := `{"branch": "{{ .Branch }}", "sha": "{{ .TargetedSha }}"}`

		parameters := []promoterv1alpha1.TektonParameter{
			{Name: "branch", Value: "{{ .Branch }}", Type: tekton.TektonDataTypeString},
			{Name: "sha", Value: "{{ .TargetedSha }}", Type: tekton.TektonDataTypeString},
			{Name: "someParam", Value: "value", Type: tekton.TektonDataTypeString},
			{Name: "simpleArray", Value: arrayStr, Type: tekton.TektonDataTypeArray},
			{Name: "templatedArray", Value: templatedArrayStr, Type: tekton.TektonDataTypeArray},
			{Name: "simpleObj", Value: objectStr, Type: tekton.TektonDataTypeObject},
			{Name: "templatedObj", Value: templatedObjectStr, Type: tekton.TektonDataTypeObject},
		}

		type SimpleObj struct {
			Timeout int  `json:"timeout"`
			Retries int  `json:"retries"`
			Verbose bool `json:"verbose"`
		}

		type TemplatedObj struct {
			Branch string `json:"branch"`
			Sha    string `json:"sha"`
		}

		BeforeEach(func() {
			By("create the TektonCommitStatus for the TaskRun")
			taskTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-taskrun-parameters",
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
						Params:         parameters,
					},
				},
			}
			Expect(k8sClient.Create(ctx, taskTCS)).To(Succeed())

			By("create the TektonCommitStatus for the PipelineRun")
			pipelineTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-pipelinerun-parameters",
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
						Params:         parameters,
					},
				},
			}
			Expect(k8sClient.Create(ctx, pipelineTCS)).To(Succeed())
		})

		DescribeTable("TCS should populate TektonRun with parameters",
			func(key, kind string, getTCS func() *promoterv1alpha1.TektonCommitStatus) {
				tcs := getTCS()
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs parameter test", "")

				By("Check parameters that are included on the TektonRun")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						params := []tekton.Data{}
						switch kind {
						case tekton.TektonKindTask:
							var taskRun tekton.TaskRun
							err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &taskRun)
							g.Expect(err).NotTo(HaveOccurred())
							params = taskRun.Spec.Params
						case tekton.TektonKindPipeline:
							var pipelineRun tekton.PipelineRun
							err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &pipelineRun)
							g.Expect(err).NotTo(HaveOccurred())
							params = pipelineRun.Spec.Params
						default:
							GinkgoWriter.Printf("Unknown kind of %s for Tekton resource", kind)
						}

						branchFound := false
						shaFound := false
						paramFound := false
						simpleArrayFound := false
						templatedArrayFound := false
						simpleObjFound := false
						templatedObjFound := false
						for _, param := range params {
							switch param.Name {
							case "branch":
								branchFound = true
								g.Expect(UnmarshalJSONDataToString(param.Value)).To(Equal(env.Branch))
							case "sha":
								shaFound = true
								g.Expect(UnmarshalJSONDataToString(param.Value)).To(Equal(env.TargetedSha))
							case "someParam":
								paramFound = true
								g.Expect(UnmarshalJSONDataToString(param.Value)).To(Equal("value"))
							case "simpleArray":
								simpleArrayFound = true
								var array []any
								g.Expect(json.Unmarshal(param.Value.Raw, &array)).To(Succeed())
								g.Expect(array).To(HaveLen(4))
							case "templatedArray":
								templatedArrayFound = true
								var array []any
								g.Expect(json.Unmarshal(param.Value.Raw, &array)).To(Succeed())
								g.Expect(array).To(HaveLen(2))
								g.Expect(array).To(ContainElements(env.Branch, env.TargetedSha))
							case "simpleObj":
								simpleObjFound = true
								expectedObj := SimpleObj{Timeout: 300, Retries: 3, Verbose: true}
								var obj SimpleObj
								g.Expect(json.Unmarshal(param.Value.Raw, &obj)).To(Succeed())
								g.Expect(obj).To(Equal(expectedObj))
							case "templatedObj":
								templatedObjFound = true
								expectedObj := TemplatedObj{Branch: env.Branch, Sha: env.TargetedSha}
								var obj TemplatedObj
								g.Expect(json.Unmarshal(param.Value.Raw, &obj)).To(Succeed())
								g.Expect(obj).To(Equal(expectedObj))
							default:
								GinkgoWriter.Printf("Unknown parameter %s", param.Name)
							}
						}
						g.Expect(branchFound).To(BeTrue())
						g.Expect(shaFound).To(BeTrue())
						g.Expect(paramFound).To(BeTrue())
						g.Expect(simpleArrayFound).To(BeTrue())
						g.Expect(templatedArrayFound).To(BeTrue())
						g.Expect(simpleObjFound).To(BeTrue())
						g.Expect(templatedObjFound).To(BeTrue())
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TaskRun should get parameters from TCS",
				taskKey,
				tekton.TektonKindTask,
				getTaskTCS),
			Entry("PipelineRun should get parameters from TCS",
				pipelineKey,
				tekton.TektonKindPipeline,
				getPipelineTCS))
	})

	Describe("Tekton Runs get pruned when run is successful and PruneRun is enabled", func() {
		taskKey := "tcs-taskrun-prune"
		pipelineKey := "tcs-pipelinerun-prune"

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
					PruneRun: true,
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
					PruneRun: true,
				},
			}
			Expect(k8sClient.Create(ctx, pipelineTCS)).To(Succeed())
		})

		DescribeTable("Should prune TektonRuns on a successful completion",
			func(key, kind string, pruned bool, condition metav1.ConditionStatus,
				getTCS func() *promoterv1alpha1.TektonCommitStatus,
			) {
				tcs := getTCS()
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs should prune test", "")

				By("Make sure Tekton was created")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						switch kind {
						case tekton.TektonKindTask:
							var taskRun tekton.TaskRun
							g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &taskRun)).To(Succeed())
						case tekton.TektonKindPipeline:
							var pipelineRun tekton.PipelineRun
							g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &pipelineRun)).To(Succeed())
						default:
							GinkgoWriter.Printf("Unknown kind of %s for Tekton resource", kind)
						}
					}
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Set all environments to success so the runs get pruned")
				setAllEnvironmentsTektonRunCondition(ctx, tcs.Name, kind, condition)

				By("Make sure TektonRuns were deleted")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						switch kind {
						case tekton.TektonKindTask:
							var taskRun tekton.TaskRun
							if pruned {
								err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &taskRun)
								g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
							} else {
								g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &taskRun)).To(Succeed())
							}

						case tekton.TektonKindPipeline:
							var pipelineRun tekton.PipelineRun
							if pruned {
								err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &pipelineRun)
								g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
							} else {
								g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &pipelineRun)).To(Succeed())
							}
						default:
							GinkgoWriter.Printf("Unknown kind of %s for Tekton resource", kind)
						}
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TaskRun created by TCS should get pruned on success",
				taskKey,
				tekton.TektonKindTask,
				true,
				metav1.ConditionTrue,
				getTaskTCS),
			Entry("PipelineRun created by TCS should get pruned on success",
				pipelineKey,
				tekton.TektonKindPipeline,
				true,
				metav1.ConditionTrue,
				getPipelineTCS),
			Entry("TaskRun created by TCS should get pruned on failure",
				taskKey,
				tekton.TektonKindTask,
				false,
				metav1.ConditionFalse,
				getTaskTCS),
			Entry("PipelineRun created by TCS should not get pruned on failure",
				pipelineKey,
				tekton.TektonKindPipeline,
				false,
				metav1.ConditionFalse,
				getPipelineTCS))
	})

	Describe("TaskRun/PipelineRun Results Evaluation", func() {
		taskKey := "tcs-taskrun-results-eval"
		pipelineKey := "tcs-pipelinerun-results-eval"

		type Object struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		}

		results := map[string]any{
			"str": "data",
			"arr": []string{"one", "two", "three"},
			"obj": Object{Id: "abcdef12345", Name: "test"},
		}

		resultTypes := map[string]string{
			"str": "string",
			"arr": "array",
			"obj": "object",
		}

		defaultExpectedResults := []promoterv1alpha1.ExpectedResult{
			{
				Key:        "str",
				Expression: `Result == "data"`,
			},
			{
				Key:        "arr",
				Expression: `Result[0] == "one"`,
			},
			{
				Key:        "obj",
				Expression: `Result.id == "abcdef12345"`,
			},
		}

		BeforeEach(func() {
			By("create the TektonCommitStatus for the TaskRun")
			taskTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-taskrun-results-eval",
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
					Expression: `Results.str == "data" && Results.arr[0] == "one" && Results.obj.id == "abcdef12345"`,
				},
			}
			Expect(k8sClient.Create(ctx, taskTCS)).To(Succeed())
			By("create the TektonCommitStatus for the PipelineRun")
			pipelineTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-pipelinerun-results-eval",
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
					Expression: `Results.str == "data" && Results.arr[0] == "one" && Results.obj.id == "abcdef12345"`,
				},
			}
			Expect(k8sClient.Create(ctx, pipelineTCS)).To(Succeed())
		})

		DescribeTable("General expression should allow for access of all results",
			func(key, kind string, results map[string]any, resultTypes map[string]string,
				expressionResult bool, csPhase promoterv1alpha1.CommitStatusPhase, getTCS func() *promoterv1alpha1.TektonCommitStatus,
			) {
				tcs := getTCS()
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs parameter test", "")

				By("Put results on all of the runs for each environment and progress them to completed")
				setAllEnvironmentsTektonRunResults(ctx, tcs.Name, kind, results, resultTypes)

				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.ExpressionResult).To(Equal(expressionResult))
						g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseSucceeded))
						g.Expect(env.Phase).To(Equal(csPhase))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TaskRun results match expression",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				true,
				promoterv1alpha1.CommitPhaseSuccess,
				getTaskTCS),
			Entry("PipelineRun results match expression",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				true,
				promoterv1alpha1.CommitPhaseSuccess,
				getPipelineTCS),
			Entry("TaskRun results do not match expression",
				taskKey,
				tekton.TektonKindTask,
				map[string]any{"str": "notright", "arr": []string{"zero"}, "obj": Object{Id: "zyx321"}},
				resultTypes,
				false,
				promoterv1alpha1.CommitPhaseFailure,
				getTaskTCS),
			Entry("PipelineRun results do not match expression",
				pipelineKey,
				tekton.TektonKindPipeline,
				map[string]any{"str": "notright", "arr": []string{"zero"}, "obj": Object{Id: "zyx321"}},
				resultTypes,
				false,
				promoterv1alpha1.CommitPhaseFailure,
				getPipelineTCS))

		DescribeTable("Individual results should reflect their values",
			func(key, kind string, results map[string]any, resultTypes map[string]string, expectedResults []promoterv1alpha1.ExpectedResult,
				individualResults map[string]bool, csPhase promoterv1alpha1.CommitStatusPhase, getTCS func() *promoterv1alpha1.TektonCommitStatus,
			) {
				tcs := getTCS()
				setExpectedResultsOnTCS(ctx, tcs.Name, expectedResults, true)
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs parameter test", "")

				By("Put results on all of the runs for each environment and progress them to completed")
				setAllEnvironmentsTektonRunResults(ctx, tcs.Name, kind, results, resultTypes)

				By("Waiting for controller to process the completed runs")
				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseSucceeded),
							"Branch %s: Expected TektonRunPhase=Succeeded, got=%s", env.Branch, env.TektonRunPhase)
						g.Expect(env.Phase).To(Equal(csPhase),
							"Branch %s: Expected Phase=%s, got=%s", env.Branch, csPhase, env.Phase)

						for _, result := range env.IndividualExpressionResults {
							expected, ok := individualResults[result.Key]
							g.Expect(ok).To(BeTrue(), "Unexpected result key: %s", result.Key)
							g.Expect(result.ExpressionResult).To(Equal(expected),
								"Branch=%s Key=%s: Expected=%v, Got=%v", env.Branch, result.Key, expected, result.ExpressionResult)
						}
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TaskRun all values are correct",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				defaultExpectedResults,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseSuccess,
				getTaskTCS),
			Entry("PipelineRun all values are correct",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				defaultExpectedResults,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseSuccess,
				getPipelineTCS),
			Entry("TaskRun all values are incorrect",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				map[string]bool{"str": false, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getTaskTCS),
			Entry("PipelineRun all values are incorrect",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				map[string]bool{"str": false, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getPipelineTCS),
			Entry("TaskRun one is correct",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "data"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				map[string]bool{"str": true, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getTaskTCS),
			Entry("PipelineRun one is correct",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "data"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				map[string]bool{"str": true, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getPipelineTCS))

		DescribeTable("Phase set is determined by both the general expression and individual expressions",
			func(key, kind string, results map[string]any, resultTypes map[string]string, expectedResults []promoterv1alpha1.ExpectedResult, generalResult bool,
				individualResults map[string]bool, csPhase promoterv1alpha1.CommitStatusPhase, getTCS func() *promoterv1alpha1.TektonCommitStatus,
			) {
				tcs := getTCS()
				setExpectedResultsOnTCS(ctx, tcs.Name, expectedResults, false)
				setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

				By("Pushing a hydrated commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs parameter test", "")

				By("Put results on all of the runs for each environment and progress them to completed")
				setAllEnvironmentsTektonRunResults(ctx, tcs.Name, kind, results, resultTypes)

				Eventually(func(g Gomega) {
					var updated promoterv1alpha1.TektonCommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      tcs.Name,
						Namespace: "default",
					}, &updated)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(updated.Status.Environments).To(HaveLen(3))

					for _, env := range updated.Status.Environments {
						g.Expect(env.TektonRunPhase).To(Equal(tekton.TektonRunPhaseSucceeded))
						g.Expect(env.ExpressionResult).To(Equal(generalResult))
						g.Expect(env.Phase).To(Equal(csPhase))
						for _, result := range env.IndividualExpressionResults {
							expected, ok := individualResults[result.Key]
							g.Expect(ok).To(BeTrue())
							g.Expect(result.ExpressionResult).To(Equal(expected))
						}
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			},
			Entry("TaskRun both combined and individual match provided expressions",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				defaultExpectedResults,
				true,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseSuccess,
				getTaskTCS),
			Entry("PipelineRun both combined and individual match provided expressions",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				defaultExpectedResults,
				true,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseSuccess,
				getPipelineTCS),
			Entry("TaskRun combined matches but individual does not",
				taskKey,
				tekton.TektonKindTask,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				true,
				map[string]bool{"str": false, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getTaskTCS),
			Entry("PipelineRun combined matches but individual does not",
				pipelineKey,
				tekton.TektonKindPipeline,
				results,
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				true,
				map[string]bool{"str": false, "arr": false, "obj": false},
				promoterv1alpha1.CommitPhaseFailure,
				getPipelineTCS),
			Entry("TaskRun combined does not match but indiviual does",
				taskKey,
				tekton.TektonKindTask,
				map[string]any{"str": "notright", "arr": []string{"zero"}, "obj": Object{Id: "notright"}},
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				false,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseFailure,
				getTaskTCS),
			Entry("PipelineRun combined does not match but individual does",
				pipelineKey,
				tekton.TektonKindPipeline,
				map[string]any{"str": "notright", "arr": []string{"zero"}, "obj": Object{Id: "notright"}},
				resultTypes,
				[]promoterv1alpha1.ExpectedResult{{Key: "str", Expression: `Result == "notright"`}, {Key: "arr", Expression: `Result[0] == "zero"`}, {Key: "obj", Expression: `Result.id == "notright"`}},
				false,
				map[string]bool{"str": true, "arr": true, "obj": true},
				promoterv1alpha1.CommitPhaseFailure,
				getPipelineTCS))
	})

	Describe("Expression Evaluation With Pruning", func() {
		key := "tcs-expression-eval-with-prune"

		BeforeEach(func() {
			By("create the TektonCommitStatus for the TaskRun")
			taskTCS = &promoterv1alpha1.TektonCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name + "-expression-eval-with-prune",
				},
				Spec: promoterv1alpha1.TektonCommitStatusSpec{
					Key:                  key,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					TektonResource: promoterv1alpha1.TektonResourceRef{
						Kind:           tekton.TektonKindTask,
						Name:           "test-task",
						ServiceAccount: "default",
					},
					ExpectedResults: []promoterv1alpha1.ExpectedResult{
						{
							Key:        "str",
							Expression: "thiswontbetheresult",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, taskTCS)).To(Succeed())
		})

		It("Run is not pruned when expression evaluation fails", func() {
			setPromotionStrategyGlobalProposedCommitStatusKey(ctx, name, key)

			By("Pushing a hydrated commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "commit for tcs basic test", "")

			By("Put results on all of the runs for each environment and progress them to completed")
			setAllEnvironmentsTektonRunResults(ctx, taskTCS.Name, tekton.TektonKindTask, map[string]any{"str": "fail"}, map[string]string{"str": "string"})
			Eventually(func(g Gomega) {
				var updated promoterv1alpha1.TektonCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      taskTCS.Name,
					Namespace: "default",
				}, &updated)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(updated.Status.Environments).To(HaveLen(3))

				for _, env := range updated.Status.Environments {
					var taskRun tekton.TaskRun
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: env.TektonRunName}, &taskRun)).To(Succeed())
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
