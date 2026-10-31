package resourcetemplate

import (
	"github.com/google/cel-go/common/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/openapi"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var jobGVK = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

// jobSchema is a cut-down batch/v1 Job OpenAPI schema with the field types the tests exercise.
func jobSchema() *spec.Schema {
	str := spec.StringProperty()
	integer := spec.Int32Property()
	boolean := spec.BoolProperty()
	container := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
		"name":  *str,
		"image": *str,
		"args":  {SchemaProps: spec.SchemaProps{Type: []string{"array"}, Items: &spec.SchemaOrArray{Schema: str}}},
		"env": {SchemaProps: spec.SchemaProps{Type: []string{"array"}, Items: &spec.SchemaOrArray{Schema: &spec.Schema{SchemaProps: spec.SchemaProps{
			Type: []string{"object"}, Properties: map[string]spec.Schema{"name": *str, "value": *str},
		}}}}},
	}}}
	podSpec := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
		"restartPolicy": *str,
		"containers":    {SchemaProps: spec.SchemaProps{Type: []string{"array"}, Items: &spec.SchemaOrArray{Schema: container}}},
		"nodeSelector":  {SchemaProps: spec.SchemaProps{Type: []string{"object"}, AdditionalProperties: &spec.SchemaOrBool{Schema: str}}},
	}}}
	condition := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{"type": *str, "status": *str}}}
	return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
		"spec": {SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
			"backoffLimit":          *integer,
			"parallelism":           *integer,
			"activeDeadlineSeconds": *spec.Int64Property(),
			"suspend":               *boolean,
			"template":              {SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{"spec": *podSpec}}},
		}}},
		"status": {SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
			"succeeded":  *integer,
			"failed":     *integer,
			"conditions": {SchemaProps: spec.SchemaProps{Type: []string{"array"}, Items: &spec.SchemaOrArray{Schema: condition}}},
		}}},
	}}}
}

// staticResolver serves jobSchema for batch/v1 Job and nothing else.
type staticResolver struct{}

func (staticResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	if gvk == jobGVK {
		return jobSchema(), nil
	}
	return nil, resolver.ErrSchemaNotFound
}

func newTestCompiler() *Compiler {
	GinkgoHelper()
	c, err := NewCompiler(staticResolver{}, log.Log)
	Expect(err).NotTo(HaveOccurred())
	return c
}

func jobDeclType() *apiservercel.DeclType {
	return common.SchemaDeclType(&openapi.Schema{Schema: common.WithTypeAndObjectMeta(jobSchema())}, true)
}

var _ = Describe("Compiler", func() {
	var c *Compiler

	BeforeEach(func() {
		c = newTestCompiler()
	})

	It("types Resource from the resolved schema and falls back to dyn otherwise", func() {
		Expect(c.ResourceType(jobGVK)).NotTo(BeNil())
		Expect(c.ResourceType(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})).To(BeNil())

		_, err := c.Compile(PhaseVariables, jobGVK, "Resource.status.conditons.size() > 0")
		Expect(err).To(HaveOccurred(), "a typo in a typed Resource field is a compile error")
		Expect(err.Error()).To(ContainSubstring("conditons"))

		_, err = c.Compile(PhaseVariables, schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, "Resource.data.anything")
		Expect(err).NotTo(HaveOccurred(), "an untyped Resource accepts any field")
	})

	It("types the Promoter variables from the generated OpenAPI definitions", func() {
		p, err := c.Compile(TemplateVariables, jobGVK, "Environment.proposed.hydrated.sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.OutputType.TypeName()).To(Equal("string"))

		_, err = c.Compile(TemplateVariables, jobGVK, "Environment.propsed")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("propsed"))

		p, err = c.Compile(TemplateVariables, jobGVK, "PromotionStrategy.spec.environments.map(e, e.branch)")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.OutputType.Kind()).To(Equal(types.ListKind))
	})

	It("offers the Kubernetes CEL libraries", func() {
		p, err := c.Compile(TemplateVariables, jobGVK, "quantity('500m').add(quantity('1')).asApproximateFloat()")
		Expect(err).NotTo(HaveOccurred())
		out, err := p.Eval(map[string]any{})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(1.5))

		p, err = c.Compile(TemplateVariables, jobGVK, "Branch.lowerAscii().replace('/', '-').matches('^[a-z0-9-]+$')")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Eval(map[string]any{"Branch": "environment/Dev"})).To(BeTrue())
	})

	It("converts results to JSON-compatible values", func() {
		activation := map[string]any{"Sha": "abc", "Environment": map[string]any{"branch": "b", "proposed": map[string]any{"hydrated": map[string]any{"sha": "abc"}}}}
		eval := func(expression string) any {
			GinkgoHelper()
			p, err := c.Compile(TemplateVariables, jobGVK, expression)
			Expect(err).NotTo(HaveOccurred())
			out, err := p.Eval(activation)
			Expect(err).NotTo(HaveOccurred())
			return out
		}
		Expect(eval("1 + 2")).To(Equal(int64(3)))
		Expect(eval("['a', Sha]")).To(Equal([]any{"a", "abc"}))
		Expect(eval("{'name': 'SHA', 'value': Sha}")).To(Equal(map[string]any{"name": "SHA", "value": "abc"}))
		Expect(eval("Environment.proposed.hydrated")).To(Equal(map[string]any{"sha": "abc"}))
		Expect(eval("Environment.?active.?hydrated.?sha")).To(BeNil(), "an absent optional is null")
		Expect(eval("Environment.?active.?hydrated.?sha.orValue('none')")).To(Equal("none"))
		Expect(eval("null")).To(BeNil())
		Expect(eval("duration('90s')")).To(Equal("1m30s"))
	})

	It("rejects results that are not a boolean or object for the phase expression", func() {
		_, err := EvaluatePhaseExpression(c, jobGVK, "'done'", map[string]any{})
		Expect(err).To(MatchError(ContainSubstring("must return a boolean or an object")))
	})
})

var _ = Describe("CheckOutputType", func() {
	job := jobDeclType()
	spec := FieldType(job, "spec")
	podSpec := FieldType(FieldType(spec, "template"), "spec")

	compile := func(expression string) *Program {
		GinkgoHelper()
		p, err := newTestCompiler().Compile(TemplateVariables, jobGVK, expression)
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	It("accepts matching scalars, maps for objects, lists for arrays, and null everywhere", func() {
		Expect(CheckOutputType(compile("2").OutputType, FieldType(spec, "parallelism"))).To(Succeed())
		Expect(CheckOutputType(compile("true").OutputType, FieldType(spec, "suspend"))).To(Succeed())
		Expect(CheckOutputType(compile("null").OutputType, FieldType(spec, "suspend"))).To(Succeed())
		Expect(CheckOutputType(compile("{'kubernetes.io/os': 'linux'}").OutputType, FieldType(podSpec, "nodeSelector"))).To(Succeed())
		Expect(CheckOutputType(compile("[{'name': 'A', 'value': 'b'}]").OutputType, FieldType(ElemType(FieldType(podSpec, "containers")), "env"))).To(Succeed())
		Expect(CheckOutputType(compile("Branch == 'x' ? optional.of({'name': 'A'}) : optional.none()").OutputType, ElemType(FieldType(ElemType(FieldType(podSpec, "containers")), "env")))).To(Succeed(), "optionals are checked by their value type")
		Expect(CheckOutputType(compile("Environment.?active.?hydrated.?sha").OutputType, ElemType(FieldType(ElemType(FieldType(podSpec, "containers")), "args")))).To(Succeed())
		Expect(CheckOutputType(compile("1").OutputType, nil)).To(Succeed(), "unknown fields are not checked")
	})

	It("rejects mismatched scalars, also inside optionals", func() {
		err := CheckOutputType(compile("'two'").OutputType, FieldType(spec, "parallelism"))
		Expect(err).To(MatchError(ContainSubstring("returns string but the field expects int")))
		err = CheckOutputType(compile("1").OutputType, FieldType(spec, "suspend"))
		Expect(err).To(MatchError(ContainSubstring("returns int but the field expects bool")))
		err = CheckOutputType(compile("Environment.?active.?hydrated.?sha").OutputType, FieldType(spec, "parallelism"))
		Expect(err).To(MatchError(ContainSubstring("returns string but the field expects int")))
		err = CheckOutputType(compile("'x'").OutputType, FieldType(podSpec, "containers"))
		Expect(err).To(MatchError(ContainSubstring("expects a list")))
	})
})
