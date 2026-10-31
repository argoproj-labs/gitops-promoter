package controller

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/KubernetesResourceCommitStatus.yaml
var kubernetesResourceCommitStatusTestdataYAML string

// Each gate uses its own commit-status key: two CommitStatuses with the same key on one SHA make the
// ChangeTransferPolicy controller fail with "too many matching SHAs", and envtest has no garbage collector
// to remove the CommitStatuses of deleted gates.
const (
	k8sResourceGateKey          = "smoke-test"
	k8sResourceHistoryGateKey   = "smoke-history"
	k8sResourceConfigMapGateKey = "smoke-config"
	k8sResourceFailureGateKey   = "smoke-failure"
	k8sResourcePruneGateKey     = "smoke-prune"
	k8sResourceRetryGateKey     = "smoke-retry"
)

// jobPhaseExpression keys off status.succeeded / status.failed, which the JobManagedBy status validation
// (enforced by the envtest API server) lets tests set without a fully consistent condition set.
const jobPhaseExpression = `(Resource.status?.succeeded ?? 0) > 0
	? {phase: 'success'}
	: ((Resource.status?.failed ?? 0) > 0 ? {phase: 'failure', description: 'Smoke test is failing'} : false)`

// templateJSON encodes an inline spec.template.
func templateJSON(object map[string]any) apiextensionsv1.JSON {
	GinkgoHelper()
	raw, err := json.Marshal(object)
	Expect(err).NotTo(HaveOccurred())
	return apiextensionsv1.JSON{Raw: raw}
}

// jobTemplate is a batch/v1 Job template exercising embedded expressions (args) and a standalone typed
// expression (parallelism is an integer in the Job schema, so a string would be rejected by the API server).
func jobTemplate() apiextensionsv1.JSON {
	return templateJSON(map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   map[string]any{"labels": map[string]any{"short-sha": "${ ShortSha }"}},
		"spec": map[string]any{
			"backoffLimit": 0,
			"parallelism":  "${ 1 + 1 }",
			"template": map[string]any{
				"spec": map[string]any{
					"restartPolicy": "Never",
					"containers": []any{map[string]any{
						"name":  "test",
						"image": "busybox",
						"args":  []any{"--sha", "${ Sha }", "--env", "${ Branch }", "--short", "${ ShortSha }"},
					}},
				},
			},
		},
	})
}

func configMapTemplate(extra string) apiextensionsv1.JSON {
	return templateJSON(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"data": map[string]any{
			"sha":    "${ Sha }",
			"branch": "${ Branch }",
			"extra":  extra,
		},
	})
}

func getK8sResourceGate(ctx context.Context, g Gomega, name string) *promoterv1alpha1.KubernetesResourceCommitStatus {
	var krcs promoterv1alpha1.KubernetesResourceCommitStatus
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &krcs)).To(Succeed())
	return &krcs
}

// devEnvironmentStatus returns the status entry for the development branch, failing the assertion when absent.
func devEnvironmentStatus(g Gomega, krcs *promoterv1alpha1.KubernetesResourceCommitStatus) promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus {
	for _, e := range krcs.Status.Environments {
		if e.Branch == testBranchDevelopment {
			return e
		}
	}
	g.Expect(krcs.Status.Environments).To(ContainElement(HaveField("Branch", testBranchDevelopment)), "expected a status entry for %s", testBranchDevelopment)
	return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}
}

var _ = Describe("KubernetesResourceCommitStatus Controller", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "k8s-resource-commit-status-test", "default")
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: k8sResourceGateKey},
			{Key: k8sResourceHistoryGateKey},
			{Key: k8sResourceConfigMapGateKey},
			{Key: k8sResourceFailureGateKey},
			{Key: k8sResourcePruneGateKey},
			{Key: k8sResourceRetryGateKey},
		}

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
		_ = k8sClient.Delete(ctx, promotionStrategy)
		_ = k8sClient.Delete(ctx, gitRepo)
		_ = k8sClient.Delete(ctx, scmProvider)
		_ = k8sClient.Delete(ctx, scmSecret)
	})

	It("strictly unmarshals the testdata example", func() {
		var krcs promoterv1alpha1.KubernetesResourceCommitStatus
		Expect(unmarshalYamlStrict(kubernetesResourceCommitStatusTestdataYAML, &krcs)).To(Succeed())
		_, gvk, err := resourcetemplate.ParseTemplate(krcs.Spec.Template)
		Expect(err).NotTo(HaveOccurred())
		Expect(gvk.Kind).To(Equal("Job"))
	})

	Describe("PerSha Job gate", Ordered, func() {
		var (
			gateName    string
			gate        *promoterv1alpha1.KubernetesResourceCommitStatus
			firstJob    string
			firstSha    string
			gitPath     string
			historyGate string
		)

		BeforeAll(func() {
			gateName = name + "-job"
			gate = &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: gateName, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceGateKey,
					ReportOn:             constants.CommitRefProposed,
					Template:             jobTemplate(),
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
					Description:          "Smoke test ${ ResourceName } is ${ Phase }",
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			var err error
			gitPath, err = os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, gate)
			if historyGate != "" {
				hg := &promoterv1alpha1.KubernetesResourceCommitStatus{}
				hg.Name, hg.Namespace = historyGate, "default"
				_ = k8sClient.Delete(ctx, hg)
			}
			_ = os.RemoveAll(gitPath)
		})

		It("creates a Job per environment from the rendered template and reports pending", func() {
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				g.Expect(krcs.Status.Environments).To(HaveLen(3))
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(dev.ReportedSha).NotTo(BeEmpty())
				g.Expect(dev.Resource).NotTo(BeNil())
				g.Expect(dev.Resource.APIVersion).To(Equal("batch/v1"))
				g.Expect(dev.Resource.Kind).To(Equal("Job"))
				g.Expect(dev.Resource.Name).To(Equal(resourcetemplate.PerShaResourceName(gateName, testBranchDevelopment, dev.ReportedSha)))
				g.Expect(dev.LastEvaluationTime).NotTo(BeNil())
				g.Expect(dev.Description).To(Equal("Smoke test " + dev.Resource.Name + " is pending"))
				firstJob, firstSha = dev.Resource.Name, dev.ReportedSha

				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: firstJob, Namespace: "default"}, &job)).To(Succeed())
				g.Expect(job.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--sha", firstSha, "--env", testBranchDevelopment, "--short", resourcetemplate.ShortSha(firstSha)}))
				g.Expect(job.Spec.Parallelism).To(HaveValue(Equal(int32(2))), "a standalone expression keeps its integer type")
				g.Expect(job.Labels).To(HaveKeyWithValue("short-sha", resourcetemplate.ShortSha(firstSha)))
				g.Expect(job.Labels).To(HaveKeyWithValue(promoterv1alpha1.ShaLabel, firstSha))
				g.Expect(job.Labels).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, utils.KubeSafeLabel(testBranchDevelopment)))
				g.Expect(job.Labels).To(HaveKeyWithValue(promoterv1alpha1.CommitStatusLabel, k8sResourceGateKey))
				g.Expect(job.Labels).To(HaveKeyWithValue("promoter.argoproj.io/kubernetes-resource-commit-status", utils.KubeSafeLabel(gateName)))
				g.Expect(metav1.IsControlledBy(&job, krcs)).To(BeTrue())

				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: utils.CommitStatusResourceName(ctx, krcs, testBranchDevelopment), Namespace: "default"}, &cs)).To(Succeed())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Sha).To(Equal(firstSha))
				g.Expect(cs.Spec.Name).To(Equal(k8sResourceGateKey + "/" + testBranchDevelopment))
				g.Expect(cs.Spec.Description).To(Equal("Smoke test " + firstJob + " is pending"))

				ready := meta.FindStatusCondition(krcs.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying a KubernetesResourceCreated event was emitted")
			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				expectEventEmitted(g, hasEventWithReasonAndMessage(eventList, gateName, constants.KubernetesResourceCreatedReason, firstJob))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reports success once the Job succeeds", func() {
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: firstJob, Namespace: "default"}, &job)).To(Succeed())
			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(dev.Description).To(Equal("Smoke test " + firstJob + " is success"))

				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: utils.CommitStatusResourceName(ctx, krcs, testBranchDevelopment), Namespace: "default"}, &cs)).To(Succeed())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				expectEventEmitted(g, hasEventWithReasonAndMessage(eventList, gateName, constants.CommitStatusPhaseChangedReason, "to success"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("keeps the terminal phase and does not recreate a Job that was deleted", func() {
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: firstJob, Namespace: "default"}, &job)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &job, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: firstJob, Namespace: "default"}, &batchv1.Job{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			// Force a reconcile by touching the gate spec, then make sure the Job stays gone and the phase sticks.
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				if krcs.Annotations == nil {
					krcs.Annotations = map[string]string{}
				}
				krcs.Spec.Url = "https://example.com/${ Branch }"
				g.Expect(k8sClient.Update(ctx, krcs)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(dev.ReportedSha).To(Equal(firstSha))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: firstJob, Namespace: "default"}, &batchv1.Job{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "the Job must not be recreated after a terminal phase")
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("creates a new Job for a new SHA and prunes older Jobs beyond historyLimit", func() {
			By("Creating a second gate with historyLimit 2 and the same template")
			historyGate = gateName + "-history"
			hg := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: historyGate, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceHistoryGateKey,
					Template:             jobTemplate(),
					Lifecycle:            promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecyclePerSha, HistoryLimit: new(int32(2))},
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
				},
			}
			Expect(k8sClient.Create(ctx, hg)).To(Succeed())
			var historyFirstJob string
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, historyGate)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Resource).NotTo(BeNil())
				g.Expect(dev.ReportedSha).To(Equal(firstSha))
				historyFirstJob = dev.Resource.Name
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing a new hydrated commit")
			newDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "second commit for kubernetes resource gate", "")
			Expect(newDrySha).NotTo(BeEmpty())

			var secondJob, secondSha string
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.ReportedSha).NotTo(Equal(firstSha))
				g.Expect(dev.Resource).NotTo(BeNil())
				g.Expect(dev.Resource.Name).NotTo(Equal(firstJob))
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "a new SHA starts over")
				secondJob, secondSha = dev.Resource.Name, dev.ReportedSha
				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secondJob, Namespace: "default"}, &job)).To(Succeed())
				g.Expect(job.Labels).To(HaveKeyWithValue(promoterv1alpha1.ShaLabel, secondSha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the historyLimit 2 gate keeps both Jobs while the default gate would keep one")
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, historyGate)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.ReportedSha).To(Equal(secondSha))
				g.Expect(dev.Resource.Name).NotTo(Equal(historyFirstJob))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: historyFirstJob, Namespace: "default"}, &batchv1.Job{})).To(Succeed(), "historyLimit 2 retains the previous Job")
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Pushing a third commit so the oldest Job of the historyLimit 2 gate is pruned")
			thirdDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "third commit for kubernetes resource gate", "")
			Expect(thirdDrySha).NotTo(Equal(newDrySha))
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: historyFirstJob, Namespace: "default"}, &batchv1.Job{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "oldest Job is pruned once a third SHA arrives")
				var jobs batchv1.JobList
				g.Expect(k8sClient.List(ctx, &jobs, ctrlclient.InNamespace("default"), ctrlclient.MatchingLabels{
					"promoter.argoproj.io/kubernetes-resource-commit-status": utils.KubeSafeLabel(historyGate),
					promoterv1alpha1.EnvironmentLabel:                        utils.KubeSafeLabel(testBranchDevelopment),
				})).To(Succeed())
				g.Expect(jobs.Items).To(HaveLen(2))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying a KubernetesResourcePruned event was emitted")
			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				expectEventEmitted(g, hasEventWithReasonAndMessage(eventList, historyGate, constants.KubernetesResourcePrunedReason, historyFirstJob))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("InPlace ConfigMap gate", Ordered, func() {
		var (
			gateName string
			gate     *promoterv1alpha1.KubernetesResourceCommitStatus
		)

		BeforeAll(func() {
			gateName = name + "-cm"
			gate = &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: gateName, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceConfigMapGateKey,
					Template:             configMapTemplate("v1"),
					Lifecycle:            promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecycleInPlace},
					// "external" is never set by the template, so another field manager can set it without
					// the controller's Server-Side Apply overwriting it.
					Phase: promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: `(Resource.data?.external ?? '') == 'ok' ? {phase: 'success', url: 'https://example.com/' + Branch} : false`},
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, gate)
		})

		It("maintains one ConfigMap per environment and re-evaluates when it changes", func() {
			cmName := resourcetemplate.InPlaceResourceName(gateName, testBranchDevelopment)
			var reportedSha string
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(dev.Resource).NotTo(BeNil())
				g.Expect(dev.Resource.Name).To(Equal(cmName))
				reportedSha = dev.ReportedSha

				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKeyWithValue("sha", reportedSha))
				g.Expect(cm.Data).To(HaveKeyWithValue("branch", testBranchDevelopment))
				g.Expect(cm.Data).To(HaveKeyWithValue("extra", "v1"))
				g.Expect(metav1.IsControlledBy(&cm, krcs)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Setting a field the template does not own")
			Eventually(func(g Gomega) {
				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
				cm.Data["external"] = "ok"
				g.Expect(k8sClient.Update(ctx, &cm)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				dev := devEnvironmentStatus(g, krcs)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(dev.Url).To(Equal("https://example.com/" + testBranchDevelopment))
				g.Expect(dev.Description).To(Equal("ConfigMap " + cmName + " reports success"))
				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: utils.CommitStatusResourceName(ctx, krcs, testBranchDevelopment), Namespace: "default"}, &cs)).To(Succeed())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Url).To(Equal("https://example.com/" + testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Propagating a template change to the in-place resource")
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				krcs.Spec.Template = configMapTemplate("v2")
				g.Expect(k8sClient.Update(ctx, krcs)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			var createdAt string
			Eventually(func(g Gomega) {
				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKeyWithValue("extra", "v2"))
				g.Expect(cm.Data).To(HaveKeyWithValue("external", "ok"), "fields owned by other managers survive the re-apply")
				createdAt = cm.Annotations[promoterv1alpha1.ResourceCreatedAtAnnotation]
				g.Expect(createdAt).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Keeping the created-at annotation stable across re-applies")
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				krcs.Spec.Template = configMapTemplate("v3")
				g.Expect(k8sClient.Update(ctx, krcs)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			Eventually(func(g Gomega) {
				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKeyWithValue("extra", "v3"))
				g.Expect(cm.Annotations).To(HaveKeyWithValue(promoterv1alpha1.ResourceCreatedAtAnnotation, createdAt))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Lifecycle options", func() {
		newGate := func(suffix, key string, lifecycle promoterv1alpha1.KubernetesResourceLifecycle) *promoterv1alpha1.KubernetesResourceCommitStatus {
			return &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-" + suffix, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  key,
					Template:             jobTemplate(),
					Lifecycle:            lifecycle,
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
				},
			}
		}
		devJob := func(g Gomega, gateName string) (string, promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus) {
			krcs := getK8sResourceGate(ctx, g, gateName)
			dev := devEnvironmentStatus(g, krcs)
			g.Expect(dev.Resource).NotTo(BeNil())
			return dev.Resource.Name, dev
		}
		setJobStatus := func(jobName string, set func(*batchv1.Job)) {
			Eventually(func(g Gomega) {
				var job batchv1.Job
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &job)).To(Succeed())
				set(&job)
				g.Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
		}

		It("deletes the resource on success with pruneOnSuccess and keeps reporting success", func() {
			gate := newGate("prune", k8sResourcePruneGateKey, promoterv1alpha1.KubernetesResourceLifecycle{PruneOnSuccess: true})
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })

			var jobName string
			Eventually(func(g Gomega) {
				jobName, _ = devJob(g, gate.Name)
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &batchv1.Job{})).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			setJobStatus(jobName, func(job *batchv1.Job) { job.Status.Succeeded = 1 })

			Eventually(func(g Gomega) {
				_, dev := devJob(g, gate.Name)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &batchv1.Job{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "the Job is pruned once success is reported")
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				_, dev := devJob(g, gate.Name)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &batchv1.Job{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "the Job is not recreated")
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			Eventually(func(g Gomega) {
				var eventList corev1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
				expectEventEmitted(g, hasEventWithReasonAndMessage(eventList, gate.Name, constants.KubernetesResourcePrunedReason, "pruneOnSuccess"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reruns a failed SHA when its resource is deleted with retryOnDelete", func() {
			gate := newGate("retry", k8sResourceRetryGateKey, promoterv1alpha1.KubernetesResourceLifecycle{RetryOnDelete: true})
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })

			var jobName string
			Eventually(func(g Gomega) {
				jobName, _ = devJob(g, gate.Name)
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &batchv1.Job{})).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			setJobStatus(jobName, func(job *batchv1.Job) { job.Status.Failed = 1 })
			Eventually(func(g Gomega) {
				_, dev := devJob(g, gate.Name)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("deleting the failed Job")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &job)).To(Succeed())
			failedUID := job.UID
			Expect(k8sClient.Delete(ctx, &job, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())

			Eventually(func(g Gomega) {
				var recreated batchv1.Job
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: "default"}, &recreated)).To(Succeed())
				g.Expect(recreated.UID).NotTo(Equal(failedUID), "a new Job is created for the same SHA")
				_, dev := devJob(g, gate.Name)
				g.Expect(dev.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Failure modes and validation", func() {
		It("reports Ready=False when the template has no kind", func() {
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-no-kind", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					Template:             templateJSON(map[string]any{"apiVersion": "batch/v1", "spec": map[string]any{}}),
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
				},
			}
			Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

			Eventually(func(g Gomega) {
				fetched := getK8sResourceGate(ctx, g, krcs.Name)
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("invalid spec.template: template is missing kind"))
				g.Expect(fetched.Status.Environments).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reports Ready=False with the API server's error when an expression produces the wrong type", func() {
			template := jobTemplate()
			var object map[string]any
			Expect(json.Unmarshal(template.Raw, &object)).To(Succeed())
			spec, ok := object["spec"].(map[string]any)
			Expect(ok).To(BeTrue())
			spec["parallelism"] = "${ 'two' }"
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-wrong-type", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					Template:             templateJSON(object),
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
				},
			}
			Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

			Eventually(func(g Gomega) {
				fetched := getK8sResourceGate(ctx, g, krcs.Name)
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("failed to create Job"))
				g.Expect(ready.Message).To(ContainSubstring("spec.parallelism"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reports Ready=False when the kind is not configured", func() {
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-unconfigured-kind", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					Template:             templateJSON(map[string]any{"apiVersion": "apps/v1", "kind": "Deployment"}),
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
				},
			}
			Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

			Eventually(func(g Gomega) {
				fetched := getK8sResourceGate(ctx, g, krcs.Name)
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("not listed in ControllerConfiguration"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reports Ready=False with the expression error when the expression is invalid", func() {
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-bad-expr", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					Template:             jobTemplate(),
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: `{phase: 'done'}`},
				},
			}
			Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

			Eventually(func(g Gomega) {
				fetched := getK8sResourceGate(ctx, g, krcs.Name)
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("invalid phase"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("rejects invalid specs through CRD validation", func() {
			base := func(suffix string) *promoterv1alpha1.KubernetesResourceCommitStatus {
				return &promoterv1alpha1.KubernetesResourceCommitStatus{
					Name: name + "-invalid-" + suffix, Namespace: "default",
					Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
						PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
						Key:                  k8sResourceFailureGateKey,
						Template:             jobTemplate(),
						Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
					},
				}
			}

			By("rejecting historyLimit with InPlace mode")
			inPlace := base("inplace")
			inPlace.Spec.Lifecycle = promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecycleInPlace, HistoryLimit: new(int32(2))}
			err := k8sClient.Create(ctx, inPlace)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("historyLimit is only valid with mode PerSha"))

			By("rejecting pruneOnSuccess and retryOnDelete with InPlace mode")
			prune := base("prune-inplace")
			prune.Spec.Lifecycle = promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecycleInPlace, PruneOnSuccess: true}
			err = k8sClient.Create(ctx, prune)
			Expect(err).To(MatchError(ContainSubstring("pruneOnSuccess is only valid with mode PerSha")))
			retry := base("retry-inplace")
			retry.Spec.Lifecycle = promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecycleInPlace, RetryOnDelete: true}
			err = k8sClient.Create(ctx, retry)
			Expect(err).To(MatchError(ContainSubstring("retryOnDelete is only valid with mode PerSha")))

			By("rejecting a template that is not an object")
			notObject := base("not-object")
			notObject.Spec.Template = apiextensionsv1.JSON{Raw: []byte(`"a string"`)}
			err = k8sClient.Create(ctx, notObject)
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())

			By("rejecting historyLimit 0")
			zero := base("zero")
			zero.Spec.Lifecycle = promoterv1alpha1.KubernetesResourceLifecycle{HistoryLimit: new(int32(0))}
			err = k8sClient.Create(ctx, zero)
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})
	})
})
