package resourcetemplate

import (
	_ "embed"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

//go:embed testdata/batch_v1_openapi.json
var batchV1OpenAPI []byte

const (
	componentsPrefix = "#/components/schemas/"
	jobComponent     = "io.k8s.api.batch.v1.Job"
)

// fixtureDocument is the subset of a /openapi/v3 group-version document the tests read.
type fixtureDocument struct {
	Components fixtureComponentsSection `json:"components"`
}

// fixtureComponentsSection holds the reusable schema components of the fixture document.
type fixtureComponentsSection struct {
	Schemas map[string]*spec.Schema `json:"schemas"`
}

// fixtureComponents parses the embedded /openapi/v3 document the way the kube-apiserver serves it.
func fixtureComponents() map[string]*spec.Schema {
	GinkgoHelper()
	var doc fixtureDocument
	Expect(json.Unmarshal(batchV1OpenAPI, &doc)).To(Succeed())
	return doc.Components.Schemas
}

// resolveFixture mirrors resolver.ClientDiscoveryResolver.ResolveSchema on the embedded document.
func resolveFixture(component string) *spec.Schema {
	GinkgoHelper()
	schemas := fixtureComponents()
	resolved, err := resolver.PopulateRefs(func(ref string) (*spec.Schema, bool) {
		s, ok := schemas[strings.TrimPrefix(ref, componentsPrefix)]
		return s, ok
	}, component)
	Expect(err).NotTo(HaveOccurred())
	return resolved
}

// expectStructural asserts the converted schema satisfies the apiextensions structural-schema invariants,
// which is what the API server enforces on CRD create.
func expectStructural(props *apiextensionsv1.JSONSchemaProps) {
	GinkgoHelper()
	internal := &apiextensions.JSONSchemaProps{}
	Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(props, internal, nil)).To(Succeed())
	structural, err := apiextschema.NewStructural(internal)
	Expect(err).NotTo(HaveOccurred())
	Expect(apiextschema.ValidateStructural(field.NewPath("openAPIV3Schema"), structural)).To(BeEmpty())
}

var _ = Describe("StructuralSchemaFromOpenAPISchema", func() {
	It("converts the resolver output into a structural schema", func() {
		props, err := StructuralSchemaFromOpenAPISchema(resolveFixture(jobComponent))
		Expect(err).NotTo(HaveOccurred())
		expectStructural(props)

		By("collapsing metadata and dropping status at the root")
		Expect(props.Type).To(Equal("object"))
		Expect(props.Properties).NotTo(HaveKey("status"))
		Expect(props.Properties["metadata"]).To(Equal(apiextensionsv1.JSONSchemaProps{Type: "object"}))
		Expect(props.Description).To(BeEmpty())

		spec := props.Properties["spec"]
		By("keeping the inlined struct fields without allOf wrappers or defaults")
		Expect(spec.AllOf).To(BeEmpty())
		Expect(spec.Default).To(BeNil())
		Expect(spec.Type).To(Equal("object"))
		Expect(spec.Required).To(ConsistOf("template"))
		template := spec.Properties["template"]
		Expect(template.Type).To(Equal("object"))
		Expect(template.Default).To(BeNil())
		Expect(template.Properties["metadata"].Type).To(Equal("object"))
		Expect(template.Properties["metadata"].Properties).To(HaveKey("name"), "nested metadata keeps its schema")
		Expect(template.Properties["spec"].Properties["restartPolicy"].Enum).To(HaveLen(2))

		By("stripping descriptions and unsupported formats")
		parallelism := spec.Properties["parallelism"]
		Expect(parallelism.Type).To(Equal("integer"))
		Expect(parallelism.Format).To(BeEmpty())
		Expect(parallelism.Description).To(BeEmpty())
		Expect(spec.Properties["startTime"].Format).To(Equal("date-time"))

		By("converting list-type map to atomic because defaults are gone")
		ports := spec.Properties["ports"]
		Expect(ports.Type).To(Equal("array"))
		Expect(*ports.XListType).To(Equal("atomic"))
		Expect(ports.XListMapKeys).To(BeEmpty())
		Expect(ports.Items.Schema.Properties["containerPort"].Default).To(BeNil())
		Expect(ports.Items.Schema.Properties["protocol"].Default).To(BeNil())
		Expect(ports.Items.Schema.Required).To(ConsistOf("containerPort"))

		By("converting IntOrString and Quantity to x-kubernetes-int-or-string")
		maxUnavailable := spec.Properties["maxUnavailable"]
		Expect(maxUnavailable.XIntOrString).To(BeTrue())
		Expect(maxUnavailable.Type).To(BeEmpty())
		Expect(maxUnavailable.OneOf).To(BeEmpty())
		Expect(maxUnavailable.Format).To(BeEmpty())
		resources := spec.Properties["resources"]
		Expect(resources.Type).To(Equal("object"))
		Expect(resources.AdditionalProperties.Schema.XIntOrString).To(BeTrue())
		Expect(resources.AdditionalProperties.Schema.OneOf).To(BeEmpty())

		By("keeping the resolver's recursion placeholder permissive")
		tree := spec.Properties["tree"]
		Expect(tree.Type).To(Equal("object"))
		Expect(tree.Properties).To(HaveKey("name"))
		children := tree.Properties["children"]
		Expect(children.Type).To(Equal("array"))
		Expect(children.Items.Schema.Type).To(Equal("object"))
		Expect(children.Items.Schema.Properties).To(BeEmpty())
		Expect(children.Items.Schema.XPreserveUnknownFields).NotTo(BeNil())
		Expect(*children.Items.Schema.XPreserveUnknownFields).To(BeTrue())

		By("keeping x-kubernetes-map-type and dropping patch extensions")
		Expect(*spec.Properties["selector"].XMapType).To(Equal("atomic"))
		raw, err := json.Marshal(props)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("x-kubernetes-patch"))
		Expect(string(raw)).NotTo(ContainSubstring("x-kubernetes-group-version-kind"))
		Expect(string(raw)).NotTo(ContainSubstring("$ref"))
	})

	It("marks property-less objects as preserve-unknown-fields", func() {
		props, err := StructuralSchemaFromOpenAPISchema(&spec.Schema{SchemaProps: spec.SchemaProps{
			Type: []string{"object"},
			Properties: map[string]spec.Schema{
				"spec": {SchemaProps: spec.SchemaProps{
					Type: []string{"object"},
					Properties: map[string]spec.Schema{
						"raw": {SchemaProps: spec.SchemaProps{Type: []string{"object"}}},
					},
				}},
			},
		}})
		Expect(err).NotTo(HaveOccurred())
		expectStructural(props)
		raw := props.Properties["spec"].Properties["raw"]
		Expect(raw.Type).To(Equal("object"))
		Expect(raw.XPreserveUnknownFields).NotTo(BeNil())
	})

	It("rejects a nil schema", func() {
		_, err := StructuralSchemaFromOpenAPISchema(nil)
		Expect(err).To(MatchError(ContainSubstring("nil")))
	})
})

var _ = Describe("StructuralSchemaFromCRD", func() {
	It("removes status, collapses metadata, drops descriptions, and keeps defaults", func() {
		source := &apiextensionsv1.JSONSchemaProps{
			Type:        "object",
			Description: "A Widget",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"apiVersion": {Type: "string"},
				"kind":       {Type: "string"},
				"metadata":   {Type: "object"},
				"spec": {
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"replicas": {Type: "integer", Description: "count", Default: &apiextensionsv1.JSON{Raw: []byte("1")}},
						"size":     {XIntOrString: true, AnyOf: []apiextensionsv1.JSONSchemaProps{{Type: "integer"}, {Type: "string"}}},
						"opaque":   {Type: "object"},
					},
				},
				"status": {Type: "object", XPreserveUnknownFields: new(true)},
			},
		}
		props, err := StructuralSchemaFromCRD(source)
		Expect(err).NotTo(HaveOccurred())
		expectStructural(props)
		Expect(props.Properties).NotTo(HaveKey("status"))
		Expect(props.Properties["metadata"]).To(Equal(apiextensionsv1.JSONSchemaProps{Type: "object"}))
		Expect(props.Description).To(BeEmpty())
		replicas := props.Properties["spec"].Properties["replicas"]
		Expect(replicas.Description).To(BeEmpty())
		Expect(replicas.Default).NotTo(BeNil())
		Expect(string(replicas.Default.Raw)).To(Equal("1"))
		Expect(props.Properties["spec"].Properties["size"].XIntOrString).To(BeTrue())
		Expect(props.Properties["spec"].Properties["opaque"].XPreserveUnknownFields).To(BeNil(), "CRD schemas are copied as is")
	})
})

var _ = Describe("PermissiveSchema", func() {
	It("is structural", func() {
		expectStructural(PermissiveSchema())
	})
})
