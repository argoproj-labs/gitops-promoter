package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("template group naming", func() {
	It("maps source groups to template groups and back", func() {
		Expect(TemplateGroupFor("batch")).To(Equal("batch.templates.promoter.argoproj.io"))
		Expect(TemplateGroupFor("")).To(Equal("core.templates.promoter.argoproj.io"))
		Expect(TemplateGroupFor("argoproj.io")).To(Equal("argoproj.io.templates.promoter.argoproj.io"))

		group, ok := SourceGroupFor("batch.templates.promoter.argoproj.io")
		Expect(ok).To(BeTrue())
		Expect(group).To(Equal("batch"))
		group, ok = SourceGroupFor("core.templates.promoter.argoproj.io")
		Expect(ok).To(BeTrue())
		Expect(group).To(BeEmpty())
		_, ok = SourceGroupFor("batch")
		Expect(ok).To(BeFalse())
		_, ok = SourceGroupFor(".templates.promoter.argoproj.io")
		Expect(ok).To(BeFalse())
	})

	It("round-trips GVKs", func() {
		job := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
		template := TemplateGVKFor(job)
		Expect(template).To(Equal(schema.GroupVersionKind{Group: "batch.templates.promoter.argoproj.io", Version: "v1", Kind: "Job"}))
		back, err := SourceGVKFor(template)
		Expect(err).NotTo(HaveOccurred())
		Expect(back).To(Equal(job))

		_, err = SourceGVKFor(job)
		Expect(err).To(MatchError(ContainSubstring("not a wrapped template API group")))
	})

	It("resolves template references", func() {
		templateGVK, sourceGVK, err := SourceGVKForTemplateRef(promoterv1alpha1.KubernetesResourceTemplateReference{
			APIVersion: "core.templates.promoter.argoproj.io/v1", Kind: "ConfigMap", Name: "x",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(templateGVK.Group).To(Equal("core.templates.promoter.argoproj.io"))
		Expect(sourceGVK).To(Equal(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}))

		_, _, err = SourceGVKForTemplateRef(promoterv1alpha1.KubernetesResourceTemplateReference{APIVersion: "a/b/c", Kind: "X", Name: "x"})
		Expect(err).To(MatchError(ContainSubstring("invalid templateRef.apiVersion")))
	})

	It("names the template CRD after the source plural and group", func() {
		Expect(TemplateCRDName("jobs", "batch")).To(Equal("jobs.batch.templates.promoter.argoproj.io"))
		Expect(TemplateCRDName("configmaps", "")).To(Equal("configmaps.core.templates.promoter.argoproj.io"))
	})
})

var _ = Describe("resource names", func() {
	const sha = "0123456789abcdef0123456789abcdef01234567"

	It("fits a DNS-1123 label and stays unique per SHA", func() {
		long := "a-very-long-gate-name-that-goes-on-and-on-forever-and-ever"
		a := PerShaResourceName(long, "environment/development", sha)
		b := PerShaResourceName(long, "environment/development", "fedcba9876543210fedcba9876543210fedcba98")
		Expect(validation.IsDNS1123Label(a)).To(BeEmpty(), a)
		Expect(validation.IsDNS1123Label(b)).To(BeEmpty(), b)
		Expect(a).NotTo(Equal(b))
		Expect(len(a)).To(BeNumerically("<=", ResourceNameMaxLength))
		Expect(PerShaResourceName(long, "environment/development", sha)).To(Equal(a), "deterministic")
	})

	It("uses one name per environment in InPlace mode", func() {
		name := InPlaceResourceName("gate", "environment/development")
		Expect(validation.IsDNS1123Label(name)).To(BeEmpty())
		Expect(name).To(HavePrefix("gate-environment-development-"))
		Expect(InPlaceResourceName("gate", "environment/staging")).NotTo(Equal(name))
	})
})
