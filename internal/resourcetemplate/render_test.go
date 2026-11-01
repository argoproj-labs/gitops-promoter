package resourcetemplate

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

var _ = Describe("ParseTemplate", func() {
	It("returns the object and its GVK", func() {
		obj, gvk, err := ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"batch/v1","kind":"Job","spec":{"backoffLimit":0}}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(gvk).To(Equal(schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}))
		Expect(obj["spec"]).To(Equal(map[string]any{"backoffLimit": int64(0)}))

		_, gvk, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(gvk).To(Equal(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}))
	})

	It("rejects templates without a literal apiVersion and kind", func() {
		_, _, err := ParseTemplate(apiextensionsv1.JSON{})
		Expect(err).To(MatchError(ContainSubstring("template is empty")))
		_, _, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`[]`)})
		Expect(err).To(MatchError(ContainSubstring("not an object")))
		_, _, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"kind":"Job"}`)})
		Expect(err).To(MatchError(ContainSubstring("missing apiVersion")))
		_, _, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"batch/v1","kind":""}`)})
		Expect(err).To(MatchError(ContainSubstring("kind must be a non-empty string")))
		_, _, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"batch/v1","kind":"${ Kind }"}`)})
		Expect(err).To(MatchError(ContainSubstring("kind must be a literal")))
		_, _, err = ParseTemplate(apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"a/b/c","kind":"Job"}`)})
		Expect(err).To(MatchError(ContainSubstring("apiVersion \"a/b/c\" is invalid")))
	})
})

var _ = Describe("BuildObject", func() {
	jobGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

	newTemplate := func() map[string]any {
		return map[string]any{
			"apiVersion": "batch/v1",
			"kind":       "Job",
			"metadata": map[string]any{
				"name":        "ignored",
				"namespace":   "ignored",
				"labels":      map[string]any{"team": "a", "sha-label": "${ ShortSha }"},
				"annotations": map[string]any{"note": "for ${ Branch }"},
			},
			"spec": map[string]any{
				"backoffLimit": int64(0),
				"parallelism":  "${ 2 }",
				"suspend":      "${ Branch == 'environment/production' }",
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{map[string]any{
							"name":  "test",
							"image": "busybox",
							"args":  []any{"--sha", "${ Sha }", "--env", "${ Branch | replace('/', '-') }"},
							"env": []any{
								map[string]any{"name": "LITERAL", "value": "no $${ templating } here"},
								"${ ReportOn == 'active' ? {name: 'ACTIVE', value: 'true'} : nil }",
							},
						}},
						"nodeSelector": "${ {'kubernetes.io/os': 'linux'} }",
					},
				},
				"activeDeadlineSeconds": "${ nil }",
			},
			"status": map[string]any{"active": int64(1)},
		}
	}

	params := func(t map[string]any) BuildParams {
		return BuildParams{
			Template:    t,
			Cache:       exprcache.New(16),
			SourceGVK:   jobGVK,
			Name:        "smoke-env-abc",
			Namespace:   "team-a",
			Labels:      map[string]string{promoterv1alpha1.EnvironmentLabel: "environment-development", "team": "override"},
			Annotations: map[string]string{promoterv1alpha1.ResourceCreatedAtAnnotation: "2026-10-31T01:02:03.000000004Z"},
			Owner:       metav1.OwnerReference{APIVersion: "promoter.argoproj.io/v1alpha1", Kind: "KubernetesResourceCommitStatus", Name: "smoke", UID: types.UID("uid")},
			Data: TemplateData{
				Branch:   "environment/development",
				Sha:      "0123456789abcdef0123456789abcdef01234567",
				ShortSha: "0123456",
				ReportOn: "proposed",
			},
		}
	}

	It("renders expressions with their types, rebuilds metadata, and drops status", func() {
		tpl := newTemplate()
		obj, err := BuildObject(params(tpl))
		Expect(err).NotTo(HaveOccurred())

		Expect(obj.GetAPIVersion()).To(Equal("batch/v1"))
		Expect(obj.GetKind()).To(Equal("Job"))
		Expect(obj.GetName()).To(Equal("smoke-env-abc"))
		Expect(obj.GetNamespace()).To(Equal("team-a"))
		Expect(obj.Object).NotTo(HaveKey("status"))

		By("merging controller labels over rendered template labels")
		Expect(obj.GetLabels()).To(Equal(map[string]string{
			"team":                            "override",
			"sha-label":                       "0123456",
			promoterv1alpha1.EnvironmentLabel: "environment-development",
		}))
		Expect(obj.GetAnnotations()).To(Equal(map[string]string{
			"note": "for environment/development",
			promoterv1alpha1.ResourceCreatedAtAnnotation: "2026-10-31T01:02:03.000000004Z",
		}))
		Expect(ResourceCreationTime(obj)).To(BeTemporally("==", time.Date(2026, 10, 31, 1, 2, 3, 4, time.UTC)))
		Expect(obj.GetOwnerReferences()).To(HaveLen(1))
		Expect(obj.GetOwnerReferences()[0].Name).To(Equal("smoke"))

		By("keeping the type of standalone expressions")
		parallelism, _, _ := unstructured.NestedInt64(obj.Object, "spec", "parallelism")
		Expect(parallelism).To(Equal(int64(2)))
		suspend, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
		Expect(suspend).To(BeFalse())
		nodeSelector, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "spec", "nodeSelector")
		Expect(nodeSelector).To(Equal(map[string]string{"kubernetes.io/os": "linux"}))
		backoff, _, _ := unstructured.NestedInt64(obj.Object, "spec", "backoffLimit")
		Expect(backoff).To(Equal(int64(0)), "non-string values are copied verbatim")

		By("dropping fields and list items whose expression is nil")
		Expect(obj.Object["spec"]).NotTo(HaveKey("activeDeadlineSeconds"))
		containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
		container, ok := containers[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(container["args"]).To(Equal([]any{"--sha", "0123456789abcdef0123456789abcdef01234567", "--env", "environment-development"}))
		Expect(container["env"]).To(Equal([]any{map[string]any{"name": "LITERAL", "value": "no ${ templating } here"}}))

		By("not mutating the template")
		Expect(tpl).To(HaveKey("status"))
		spec, ok := tpl["spec"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(spec["parallelism"]).To(Equal("${ 2 }"))
	})

	It("fails on unknown variables and reports the path", func() {
		tpl := newTemplate()
		_ = unstructured.SetNestedField(tpl, "${ Nope }", "spec", "suspendReason")
		_, err := BuildObject(params(tpl))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.suspendReason"))
	})

	It("requires labels and annotations to render to strings", func() {
		tpl := newTemplate()
		_ = unstructured.SetNestedField(tpl, "${ {a: 1} }", "metadata", "labels", "bad")
		_, err := BuildObject(params(tpl))
		Expect(err).NotTo(HaveOccurred(), "maps are stringified as JSON")
		Expect(unstructured.SetNestedField(tpl, int64(1), "metadata", "labels", "bad")).To(Succeed())
		_, err = BuildObject(params(tpl))
		Expect(err).To(MatchError(ContainSubstring("metadata.labels.bad must be a string")))
	})

	It("rejects a nil template", func() {
		p := params(newTemplate())
		p.Template = nil
		_, err := BuildObject(p)
		Expect(err).To(MatchError(ContainSubstring("template is nil")))
	})
})

var _ = Describe("TemplateData", func() {
	It("exposes the report variables only in ReportEnv", func() {
		d := TemplateData{Branch: "b", Phase: promoterv1alpha1.CommitPhaseSuccess, ResourceName: "r", ExpressionURL: "u"}
		Expect(d.Env()).NotTo(HaveKey("Phase"))
		env := d.ReportEnv()
		Expect(env).To(HaveKeyWithValue("Phase", "success"))
		Expect(env).To(HaveKeyWithValue("ResourceName", "r"))
		Expect(env).To(HaveKeyWithValue("ExpressionUrl", "u"))
	})
})

var _ = Describe("ResourceLabels", func() {
	AfterEach(func() {
		settings.SetControllerInstanceIDForTest(nil)
	})

	It("includes the gate, environment, key, and sha labels", func() {
		settings.SetControllerInstanceIDForTest(nil)
		gate := &promoterv1alpha1.KubernetesResourceCommitStatus{}
		gate.Name = "smoke"
		gate.Spec.Key = "smoke-test"
		labels := ResourceLabels(gate, "environment/development", "abc")
		Expect(labels).To(HaveKeyWithValue("promoter.argoproj.io/kubernetes-resource-commit-status", "smoke"))
		Expect(labels).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, "environment-development"))
		Expect(labels).To(HaveKeyWithValue(promoterv1alpha1.CommitStatusLabel, "smoke-test"))
		Expect(labels).To(HaveKeyWithValue(promoterv1alpha1.ShaLabel, "abc"))
		Expect(labels).NotTo(HaveKey(promoterv1alpha1.InstanceIDLabel))
	})

	It("stamps the instance-id label in multi-install mode so partitioned informers see the resource", func() {
		settings.SetControllerInstanceIDForTest(new("wave-1"))
		gate := &promoterv1alpha1.KubernetesResourceCommitStatus{}
		gate.Name = "smoke"
		gate.Spec.Key = "smoke-test"
		Expect(ResourceLabels(gate, "environment/development", "abc")).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, "wave-1"))
	})
})

var _ = Describe("ResourceCreationTime", func() {
	It("prefers the created-at annotation and falls back to creationTimestamp", func() {
		now := time.Date(2026, 10, 31, 1, 2, 3, 500, time.UTC)
		obj := &unstructured.Unstructured{Object: map[string]any{}}
		obj.SetAnnotations(ResourceAnnotations(now))
		Expect(ResourceCreationTime(obj)).To(BeTemporally("==", now))

		fallback := &unstructured.Unstructured{Object: map[string]any{}}
		fallback.SetCreationTimestamp(metav1.NewTime(now.Truncate(time.Second)))
		fallback.SetAnnotations(map[string]string{promoterv1alpha1.ResourceCreatedAtAnnotation: "not-a-time"})
		Expect(ResourceCreationTime(fallback)).To(BeTemporally("==", now.Truncate(time.Second)))
	})
})

var _ = Describe("ShortSha", func() {
	It("truncates to seven characters", func() {
		Expect(ShortSha("0123456789")).To(Equal("0123456"))
		Expect(ShortSha("abc")).To(Equal("abc"))
	})
})
