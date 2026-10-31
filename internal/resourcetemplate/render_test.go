package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

var _ = Describe("BuildObject", func() {
	jobGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

	newTemplate := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "batch.templates.promoter.argoproj.io/v1",
			"kind":       "Job",
			"metadata": map[string]any{
				"name":            "smoke",
				"namespace":       "team-a",
				"resourceVersion": "123",
				"labels":          map[string]any{"team": "a", "sha-label": "{{ .ShortSha }}"},
				"annotations":     map[string]any{"note": "for {{ .Branch }}"},
			},
			"spec": map[string]any{
				"backoffLimit": int64(0),
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{map[string]any{
							"name":  "test",
							"image": "busybox",
							"args":  []any{"--sha", "{{ .Sha }}", "--env", "{{ .Branch | replace \"/\" \"-\" }}"},
							"env":   []any{map[string]any{"name": "LITERAL", "value": "no templating here"}},
						}},
					},
				},
			},
			"status": map[string]any{"active": int64(1)},
		}}
	}

	params := func(t *unstructured.Unstructured) BuildParams {
		return BuildParams{
			Template:  t,
			SourceGVK: jobGVK,
			Name:      "smoke-env-abc",
			Namespace: "team-a",
			Labels:    map[string]string{promoterv1alpha1.EnvironmentLabel: "environment-development", "team": "override"},
			Owner:     metav1.OwnerReference{APIVersion: "promoter.argoproj.io/v1alpha1", Kind: "KubernetesResourceCommitStatus", Name: "smoke", UID: types.UID("uid")},
			Data: TemplateData{
				Branch:   "environment/development",
				Sha:      "0123456789abcdef0123456789abcdef01234567",
				ShortSha: "0123456",
			},
		}
	}

	It("renders string leaves, rebuilds metadata, and drops status", func() {
		tpl := newTemplate()
		obj, err := BuildObject(params(tpl))
		Expect(err).NotTo(HaveOccurred())

		Expect(obj.GetAPIVersion()).To(Equal("batch/v1"))
		Expect(obj.GetKind()).To(Equal("Job"))
		Expect(obj.GetName()).To(Equal("smoke-env-abc"))
		Expect(obj.GetNamespace()).To(Equal("team-a"))
		Expect(obj.GetResourceVersion()).To(BeEmpty())
		Expect(obj.Object).NotTo(HaveKey("status"))

		By("merging controller labels over rendered template labels")
		Expect(obj.GetLabels()).To(Equal(map[string]string{
			"team":                            "override",
			"sha-label":                       "0123456",
			promoterv1alpha1.EnvironmentLabel: "environment-development",
		}))
		Expect(obj.GetAnnotations()).To(Equal(map[string]string{"note": "for environment/development"}))
		Expect(obj.GetOwnerReferences()).To(HaveLen(1))
		Expect(obj.GetOwnerReferences()[0].Name).To(Equal("smoke"))

		By("rendering nested string values and leaving other types alone")
		args, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "template", "spec", "containers")
		Expect(found).To(BeFalse(), "containers is a list of objects, not strings")
		Expect(args).To(BeNil())
		Expect(err).To(HaveOccurred())
		containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
		container, ok := containers[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(container["args"]).To(Equal([]any{"--sha", "0123456789abcdef0123456789abcdef01234567", "--env", "environment-development"}))
		env, ok := container["env"].([]any)
		Expect(ok).To(BeTrue())
		envVar, ok := env[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(envVar["value"]).To(Equal("no templating here"))
		backoff, _, _ := unstructured.NestedInt64(obj.Object, "spec", "backoffLimit")
		Expect(backoff).To(Equal(int64(0)))

		By("not mutating the template")
		origArgs, _, _ := unstructured.NestedSlice(tpl.Object, "spec", "template", "spec", "containers")
		Expect(origArgs[0].(map[string]any)["args"]).To(ContainElement("{{ .Sha }}")) //nolint:forcetypeassert // test fixture shape is known
		Expect(tpl.Object).To(HaveKey("status"))
	})

	It("fails on unknown template variables and reports the path", func() {
		tpl := newTemplate()
		_ = unstructured.SetNestedField(tpl.Object, "{{ .Nope }}", "spec", "suspendReason")
		_, err := BuildObject(params(tpl))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.suspendReason"))
	})

	It("rejects a nil template", func() {
		p := params(newTemplate())
		p.Template = nil
		_, err := BuildObject(p)
		Expect(err).To(MatchError(ContainSubstring("template is nil")))
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

var _ = Describe("ShortSha", func() {
	It("truncates to seven characters", func() {
		Expect(ShortSha("0123456789")).To(Equal("0123456"))
		Expect(ShortSha("abc")).To(Equal("abc"))
	})
})
