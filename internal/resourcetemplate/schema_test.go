package resourcetemplate

import (
	_ "embed"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

//go:embed testdata/batch_v1_openapi.json
var batchV1OpenAPI []byte

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

var _ = Describe("StructuralSchemaFromOpenAPIV3", func() {
	jobGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

	It("converts the kube-apiserver document into a structural schema", func() {
		props, err := StructuralSchemaFromOpenAPIV3(batchV1OpenAPI, jobGVK)
		Expect(err).NotTo(HaveOccurred())
		expectStructural(props)

		By("collapsing metadata and dropping status at the root")
		Expect(props.Type).To(Equal("object"))
		Expect(props.Properties).NotTo(HaveKey("status"))
		Expect(props.Properties["metadata"]).To(Equal(apiextensionsv1.JSONSchemaProps{Type: "object"}))
		Expect(props.Description).To(BeEmpty())

		spec := props.Properties["spec"]
		By("flattening allOf wrappers and dropping defaults")
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

		By("collapsing recursive references to a permissive object")
		tree := spec.Properties["tree"]
		Expect(tree.Type).To(Equal("object"))
		Expect(tree.Properties).To(HaveKey("name"))
		children := tree.Properties["children"]
		Expect(children.Type).To(Equal("array"))
		Expect(children.Items.Schema.Type).To(Equal("object"))
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

	It("fails when the document does not declare the kind", func() {
		_, err := StructuralSchemaFromOpenAPIV3(batchV1OpenAPI, schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"})
		Expect(err).To(MatchError(ContainSubstring("no schema component declares")))
	})

	It("fails on malformed documents", func() {
		_, err := StructuralSchemaFromOpenAPIV3([]byte("{"), jobGVK)
		Expect(err).To(MatchError(ContainSubstring("failed to parse")))
		_, err = StructuralSchemaFromOpenAPIV3([]byte(`{"components":{"schemas":{}}}`), jobGVK)
		Expect(err).To(MatchError(ContainSubstring("no components.schemas")))
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
	})
})

var _ = Describe("PermissiveSchema", func() {
	It("is structural", func() {
		expectStructural(PermissiveSchema())
	})
})
