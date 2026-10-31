package controller

import (
	"context"
	_ "embed"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	jobTemplateAPIVersion       = "batch.templates.promoter.argoproj.io/v1"
	configMapTemplateAPIVersion = "core.templates.promoter.argoproj.io/v1"
	k8sResourceGateKey          = "smoke-test"
	k8sResourceHistoryGateKey   = "smoke-history"
	k8sResourceConfigMapGateKey = "smoke-config"
	k8sResourceFailureGateKey   = "smoke-failure"
	k8sResourceValidateGateKey  = "smoke-validate"
)

// jobPhaseExpression keys off status.succeeded / status.failed, which the JobManagedBy status validation
// (enforced by the envtest API server) lets tests set without a fully consistent condition set.
const jobPhaseExpression = `(Resource.status?.succeeded ?? 0) > 0
	? {phase: 'success'}
	: ((Resource.status?.failed ?? 0) > 0 ? {phase: 'failure', description: 'Smoke test is failing'} : false)`

func newJobTemplate(name string) *unstructured.Unstructured {
	tpl := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"backoffLimit": int64(0),
			"template": map[string]any{
				"spec": map[string]any{
					"restartPolicy": "Never",
					"containers": []any{map[string]any{
						"name":  "test",
						"image": "busybox",
						"args":  []any{"--sha", "{{ .Sha }}", "--env", "{{ .Branch }}", "--short", "{{ .ShortSha }}"},
					}},
				},
			},
		},
	}}
	tpl.SetAPIVersion(jobTemplateAPIVersion)
	tpl.SetKind("Job")
	tpl.SetName(name)
	tpl.SetNamespace("default")
	return tpl
}

func newConfigMapTemplate(name string) *unstructured.Unstructured {
	tpl := &unstructured.Unstructured{Object: map[string]any{
		"data": map[string]any{
			"sha":    "{{ .Sha }}",
			"branch": "{{ .Branch }}",
			"extra":  "v1",
		},
	}}
	tpl.SetAPIVersion(configMapTemplateAPIVersion)
	tpl.SetKind("ConfigMap")
	tpl.SetName(name)
	tpl.SetNamespace("default")
	return tpl
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
			{Key: k8sResourceGateKey}, {Key: k8sResourceHistoryGateKey}, {Key: k8sResourceConfigMapGateKey}, {Key: k8sResourceFailureGateKey},
			{Key: k8sResourceValidateGateKey},
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
		Expect(krcs.Spec.TemplateRef.APIVersion).To(Equal(jobTemplateAPIVersion))
	})

	Describe("Wrapped template CRDs", func() {
		It("generates a Job template CRD that validates like batch/v1 Job", func() {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "jobs.batch.templates.promoter.argoproj.io"}, &crd)).To(Succeed())
			Expect(crd.Spec.Group).To(Equal("batch.templates.promoter.argoproj.io"))
			Expect(crd.Spec.Scope).To(Equal(apiextensionsv1.NamespaceScoped))
			Expect(crd.Spec.Names).To(Equal(apiextensionsv1.CustomResourceDefinitionNames{Plural: "jobs", Singular: "job", Kind: "Job", ListKind: "JobList"}))
			Expect(crd.Labels).To(HaveKeyWithValue(promoterv1alpha1.TemplateCRDLabel, "true"))
			Expect(crd.Annotations).To(HaveKeyWithValue(promoterv1alpha1.TemplateCRDSourceAnnotation, "batch/Job"))
			Expect(crd.Spec.Versions).To(HaveLen(1))
			Expect(crd.Spec.Versions[0].Name).To(Equal("v1"))
			Expect(crd.Spec.Versions[0].Storage).To(BeTrue())
			Expect(crd.Spec.Versions[0].Subresources).To(BeNil())

			schemaProps := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			Expect(schemaProps.Properties).NotTo(HaveKey("status"))
			Expect(schemaProps.Properties["spec"].Properties).To(HaveKey("template"), "the batch/v1 Job schema was copied, not the permissive fallback")
			Expect(schemaProps.Properties["spec"].Properties["parallelism"].Type).To(Equal("integer"))

			By("accepting a valid template")
			valid := newJobTemplate(name + "-crd-valid")
			Expect(k8sClient.Create(ctx, valid, ctrlclient.FieldValidation("Strict"))).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, valid) })

			By("rejecting a template that violates the Job schema")
			invalid := newJobTemplate(name + "-crd-invalid")
			Expect(unstructured.SetNestedField(invalid.Object, "abc", "spec", "parallelism")).To(Succeed())
			err := k8sClient.Create(ctx, invalid)
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue(), err.Error())
		})

		It("generates a core-group template CRD under core.templates", func() {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "configmaps.core.templates.promoter.argoproj.io"}, &crd)).To(Succeed())
			Expect(crd.Spec.Names.Kind).To(Equal("ConfigMap"))
			Expect(crd.Annotations).To(HaveKeyWithValue(promoterv1alpha1.TemplateCRDSourceAnnotation, "core/ConfigMap"))
		})

		It("is idempotent and converts CRD-backed kinds from the source CRD", func() {
			generator, err := resourcetemplate.NewCRDGenerator(cfg, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred())
			Expect(generator.Ensure(ctx, []promoterv1alpha1.ResourceGroupVersionKind{
				{Group: "batch", Version: "v1", Kind: "Job"},
				{Group: "promoter.argoproj.io", Version: "v1alpha1", Kind: "CommitStatus"},
			})).To(Succeed())
			DeferCleanup(func() {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				crd.Name = "commitstatuses.promoter.argoproj.io.templates.promoter.argoproj.io"
				_ = k8sClient.Delete(ctx, crd)
			})

			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "commitstatuses.promoter.argoproj.io.templates.promoter.argoproj.io"}, &crd)).To(Succeed())
			props := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			Expect(props.Properties).NotTo(HaveKey("status"))
			Expect(props.Properties["spec"].Properties).To(HaveKey("sha"), "schema copied from the source CRD")

			By("rejecting cluster-scoped kinds")
			err = generator.Ensure(ctx, []promoterv1alpha1.ResourceGroupVersionKind{{Group: "", Version: "v1", Kind: "Namespace"}})
			Expect(err).To(MatchError(ContainSubstring("cluster-scoped")))

			By("reporting kinds the API server does not serve")
			err = generator.Ensure(ctx, []promoterv1alpha1.ResourceGroupVersionKind{{Group: "batch", Version: "v1", Kind: "Nope"}})
			Expect(err).To(MatchError(ContainSubstring("not served")))
		})
	})

	Describe("PerSha Job gate", Ordered, func() {
		var (
			gateName    string
			template    *unstructured.Unstructured
			gate        *promoterv1alpha1.KubernetesResourceCommitStatus
			firstJob    string
			firstSha    string
			gitPath     string
			historyGate string
		)

		BeforeAll(func() {
			gateName = name + "-job"
			template = newJobTemplate(gateName + "-template")
			Expect(k8sClient.Create(ctx, template)).To(Succeed())

			gate = &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: gateName, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceGateKey,
					ReportOn:             constants.CommitRefProposed,
					TemplateRef: promoterv1alpha1.KubernetesResourceTemplateReference{
						APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: template.GetName(),
					},
					Phase:               promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
					DescriptionTemplate: "Smoke test {{ .ResourceName }} is {{ .Phase }}",
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			var err error
			gitPath, err = os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, gate)
			_ = k8sClient.Delete(ctx, template)
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
				krcs.Spec.UrlTemplate = "https://example.com/{{ .Branch }}"
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
			By("Creating a second gate with historyLimit 2 on the same template")
			historyGate = gateName + "-history"
			hg := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: historyGate, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceHistoryGateKey,
					TemplateRef: promoterv1alpha1.KubernetesResourceTemplateReference{
						APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: template.GetName(),
					},
					Lifecycle: promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecyclePerSha, HistoryLimit: new(int32(2))},
					Phase:     promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
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
			template *unstructured.Unstructured
			gate     *promoterv1alpha1.KubernetesResourceCommitStatus
		)

		BeforeAll(func() {
			gateName = name + "-cm"
			template = newConfigMapTemplate(gateName + "-template")
			Expect(k8sClient.Create(ctx, template)).To(Succeed())
			gate = &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: gateName, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceConfigMapGateKey,
					TemplateRef: promoterv1alpha1.KubernetesResourceTemplateReference{
						APIVersion: configMapTemplateAPIVersion, Kind: "ConfigMap", Name: template.GetName(),
					},
					Lifecycle: promoterv1alpha1.KubernetesResourceLifecycle{Mode: promoterv1alpha1.KubernetesResourceLifecycleInPlace},
					// "external" is never set by the template, so another field manager can set it without
					// the controller's Server-Side Apply overwriting it.
					Phase: promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: `(Resource.data?.external ?? '') == 'ok' ? {phase: 'success', url: 'https://example.com/' + Branch} : false`},
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, gate)
			_ = k8sClient.Delete(ctx, template)
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
			var cm corev1.ConfigMap
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
			cm.Data["external"] = "ok"
			Expect(k8sClient.Update(ctx, &cm)).To(Succeed())

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
				var tpl unstructured.Unstructured
				tpl.SetGroupVersionKind(schema.GroupVersionKind{Group: "core.templates.promoter.argoproj.io", Version: "v1", Kind: "ConfigMap"})
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: template.GetName(), Namespace: "default"}, &tpl)).To(Succeed())
				g.Expect(unstructured.SetNestedField(tpl.Object, "v2", "data", "extra")).To(Succeed())
				g.Expect(k8sClient.Update(ctx, &tpl)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			Eventually(func(g Gomega) {
				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "default"}, &cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKeyWithValue("extra", "v2"))
				g.Expect(cm.Data).To(HaveKeyWithValue("external", "ok"), "fields owned by other managers survive the re-apply")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Template validation by dry-run", Ordered, func() {
		var (
			gateName string
			template *unstructured.Unstructured
			gate     *promoterv1alpha1.KubernetesResourceCommitStatus
		)

		BeforeAll(func() {
			gateName = name + "-validate"
			template = newJobTemplate(gateName + "-template")
			Expect(k8sClient.Create(ctx, template)).To(Succeed())
			gate = &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: gateName, Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceValidateGateKey,
					TemplateRef:          promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: template.GetName()},
					Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: jobPhaseExpression},
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, gate)
			_ = k8sClient.Delete(ctx, template)
		})

		It("validates the template once per resourceVersion and reports native validation errors", func() {
			var validatedAt metav1.Time
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				g.Expect(krcs.Status.TemplateValidation).NotTo(BeNil())
				g.Expect(krcs.Status.TemplateValidation.Valid).To(BeTrue())
				g.Expect(krcs.Status.TemplateValidation.ObservedResourceVersion).To(Equal(template.GetResourceVersion()))
				g.Expect(krcs.Status.TemplateValidation.LastValidationTime).NotTo(BeNil())
				validatedAt = *krcs.Status.TemplateValidation.LastValidationTime
				g.Expect(krcs.Status.Environments).To(HaveLen(3))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Forcing a reconcile and confirming the dry-run is not repeated for the same template")
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				krcs.Spec.UrlTemplate = "https://example.com/{{ .Branch }}"
				g.Expect(k8sClient.Update(ctx, krcs)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				g.Expect(krcs.Status.TemplateValidation.LastValidationTime.Time).To(BeTemporally("==", validatedAt.Time))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Breaking the template in a way the schema allows but native Job validation rejects")
			var invalidRV string
			Eventually(func(g Gomega) {
				var tpl unstructured.Unstructured
				tpl.SetGroupVersionKind(schema.GroupVersionKind{Group: "batch.templates.promoter.argoproj.io", Version: "v1", Kind: "Job"})
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: template.GetName(), Namespace: "default"}, &tpl)).To(Succeed())
				g.Expect(unstructured.SetNestedField(tpl.Object, int64(-1), "spec", "backoffLimit")).To(Succeed())
				g.Expect(k8sClient.Update(ctx, &tpl)).To(Succeed(), "the wrapped CRD has no minimum on backoffLimit")
				invalidRV = tpl.GetResourceVersion()
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				g.Expect(krcs.Status.TemplateValidation).NotTo(BeNil())
				g.Expect(krcs.Status.TemplateValidation.Valid).To(BeFalse())
				g.Expect(krcs.Status.TemplateValidation.ObservedResourceVersion).To(Equal(invalidRV))
				g.Expect(krcs.Status.TemplateValidation.Message).To(ContainSubstring("backoffLimit"))
				ready := meta.FindStatusCondition(krcs.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("is invalid"))
				g.Expect(krcs.Status.Environments).To(HaveLen(3), "previous environment reports are kept")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Fixing the template restores Ready=True")
			Eventually(func(g Gomega) {
				var tpl unstructured.Unstructured
				tpl.SetGroupVersionKind(schema.GroupVersionKind{Group: "batch.templates.promoter.argoproj.io", Version: "v1", Kind: "Job"})
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: template.GetName(), Namespace: "default"}, &tpl)).To(Succeed())
				g.Expect(unstructured.SetNestedField(tpl.Object, int64(0), "spec", "backoffLimit")).To(Succeed())
				g.Expect(k8sClient.Update(ctx, &tpl)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			Eventually(func(g Gomega) {
				krcs := getK8sResourceGate(ctx, g, gateName)
				g.Expect(krcs.Status.TemplateValidation.Valid).To(BeTrue())
				g.Expect(krcs.Status.TemplateValidation.ObservedResourceVersion).NotTo(Equal(invalidRV))
				ready := meta.FindStatusCondition(krcs.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Failure modes and validation", func() {
		It("reports Ready=False when the template does not exist", func() {
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-missing-template", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					TemplateRef:          promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: "does-not-exist"},
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
				g.Expect(ready.Message).To(ContainSubstring(`template Job "does-not-exist" not found`))
				g.Expect(fetched.Status.Environments).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("reports Ready=False when the kind is not configured", func() {
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-unconfigured-kind", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					TemplateRef:          promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: "apps.templates.promoter.argoproj.io/v1", Kind: "Deployment", Name: "x"},
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
			template := newJobTemplate(name + "-bad-expr-template")
			Expect(k8sClient.Create(ctx, template)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, template) })
			krcs := &promoterv1alpha1.KubernetesResourceCommitStatus{
				Name: name + "-bad-expr", Namespace: "default",
				Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  k8sResourceFailureGateKey,
					TemplateRef:          promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: template.GetName()},
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
						TemplateRef:          promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: jobTemplateAPIVersion, Kind: "Job", Name: "x"},
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

			By("rejecting a templateRef outside the wrapped template groups")
			wrongGroup := base("group")
			wrongGroup.Spec.TemplateRef.APIVersion = "batch/v1"
			err = k8sClient.Create(ctx, wrongGroup)
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
