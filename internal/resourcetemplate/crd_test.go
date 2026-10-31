package resourcetemplate

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// stubResolver serves fixture schemas for known kinds.
type stubResolver struct {
	schemas map[schema.GroupVersionKind]*spec.Schema
}

func (s stubResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	if sch, ok := s.schemas[gvk]; ok {
		return sch, nil
	}
	return nil, fmt.Errorf("no schema for %s", gvk)
}

func newFakeDiscovery() discovery.DiscoveryInterfaceWithContext {
	fd := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	fd.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "batch/v1",
			APIResources: []metav1.APIResource{
				{Name: "jobs", SingularName: "job", Namespaced: true, Kind: "Job"},
				{Name: "jobs/status", Namespaced: true, Kind: "Job"},
			},
		},
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "namespaces", SingularName: "namespace", Namespaced: false, Kind: "Namespace"},
			},
		},
	}
	return discovery.ToDiscoveryInterfaceWithContext(fd)
}

var _ = Describe("CRDGenerator", func() {
	var (
		ctx context.Context
		c   client.Client
		gen *CRDGenerator
	)

	BeforeEach(func() {
		ctx = context.Background()
		c = fake.NewClientBuilder().WithScheme(utils.GetScheme()).Build()
		gen = &CRDGenerator{
			Client:    c,
			Discovery: newFakeDiscovery(),
			Resolver: stubResolver{schemas: map[schema.GroupVersionKind]*spec.Schema{
				{Group: "batch", Version: "v1", Kind: "Job"}: resolveFixture(jobComponent),
			}},
			Log:           GinkgoLogr,
			SkipReadyWait: true,
		}
	})

	It("creates a wrapped CRD from the resolved schema and updates it idempotently", func() {
		resources := []promoterv1alpha1.ResourceGroupVersionKind{{Group: "batch", Version: "v1", Kind: "Job"}}
		Expect(gen.Ensure(ctx, resources)).To(Succeed())

		var crd apiextensionsv1.CustomResourceDefinition
		Expect(c.Get(ctx, types.NamespacedName{Name: "jobs.batch.templates.promoter.argoproj.io"}, &crd)).To(Succeed())
		Expect(crd.Spec.Group).To(Equal("batch.templates.promoter.argoproj.io"))
		Expect(crd.Spec.Names).To(Equal(apiextensionsv1.CustomResourceDefinitionNames{Plural: "jobs", Singular: "job", Kind: "Job", ListKind: "JobList"}))
		Expect(crd.Spec.Scope).To(Equal(apiextensionsv1.NamespaceScoped))
		Expect(crd.Labels).To(HaveKeyWithValue(promoterv1alpha1.TemplateCRDLabel, "true"))
		Expect(crd.Annotations).To(HaveKeyWithValue(promoterv1alpha1.TemplateCRDSourceAnnotation, "batch/Job"))
		Expect(crd.Spec.Versions).To(HaveLen(1))
		props := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
		Expect(props.Properties["spec"].Properties).To(HaveKey("template"), "the converted schema passed in-process validation and was kept")
		Expect(validateCRD(ctx, &crd)).To(BeEmpty())

		By("updating in place on a second run")
		Expect(gen.Ensure(ctx, resources)).To(Succeed())
		var again apiextensionsv1.CustomResourceDefinition
		Expect(c.Get(ctx, types.NamespacedName{Name: "jobs.batch.templates.promoter.argoproj.io"}, &again)).To(Succeed())
		Expect(again.Spec.Versions).To(HaveLen(1))
	})

	It("falls back to the permissive schema when the converted CRD fails in-process validation", func() {
		// Root metadata may only be a plain object; a string type is rejected by ValidateCustomResourceDefinition.
		crd := buildCRD("batch", apiextensionsv1.CustomResourceDefinitionNames{Plural: "jobs", Singular: "job", Kind: "Job", ListKind: "JobList"},
			[]apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"metadata": {Type: "string"},
						"spec":     {Type: "object", XPreserveUnknownFields: new(true)},
					},
				}},
			}})
		errs := validateCRD(ctx, crd)
		Expect(errs).NotTo(BeEmpty())
		Expect(errs.ToAggregate().Error()).To(ContainSubstring("metadata"))

		usePermissiveSchema(crd)
		Expect(validateCRD(ctx, crd)).To(BeEmpty())
		Expect(crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].XPreserveUnknownFields).NotTo(BeNil())
	})

	It("rejects cluster-scoped and unknown kinds", func() {
		err := gen.Ensure(ctx, []promoterv1alpha1.ResourceGroupVersionKind{{Group: "", Version: "v1", Kind: "Namespace"}})
		Expect(err).To(MatchError(ContainSubstring("cluster-scoped")))

		err = gen.Ensure(ctx, []promoterv1alpha1.ResourceGroupVersionKind{{Group: "batch", Version: "v1", Kind: "Nope"}})
		Expect(err).To(MatchError(ContainSubstring("not served")))
	})
})
