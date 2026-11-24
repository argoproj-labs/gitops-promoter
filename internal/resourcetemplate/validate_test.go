package resourcetemplate

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const validJobTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  labels:
    short-sha: ${ ShortSha }
    environment: ${ Branch.replace('/', '-') }
  annotations:
    team: ${ NamespaceMetadata.?labels[?'team'].orValue('none') }
spec:
  parallelism: ${ 1 + 1 }
  backoffLimit: 0
  suspend: ${ Branch == 'environment/development' }
  activeDeadlineSeconds: "${ Branch == 'main' ? optional.of(600) : optional.none() }"
  template:
    spec:
      restartPolicy: Never
      nodeSelector:
        zone: ${ Environment.branch }
      containers:
        - name: smoke
          image: busybox
          args: ["--sha", "${ Sha }", "--branch=${ Branch }"]
          env:
            - name: PS
              value: ${ PromotionStrategy.metadata.name }
`

// aggregate renders errs for an assertion description without panicking on an empty list.
func aggregate(errs field.ErrorList) string {
	if agg := errs.ToAggregate(); agg != nil {
		return agg.Error()
	}
	return ""
}

var _ = Describe("ValidateSpec", func() {
	var compiler *Compiler
	var ks *KindSchema

	BeforeEach(func() {
		compiler = newTestCompiler()
		var err error
		ks, err = compiler.ResolveKindSchema(jobGVK)
		Expect(err).NotTo(HaveOccurred())
	})

	// validate parses template (YAML) and validates it with the given phase expression.
	validate := func(template, phase string, mutate func(*promoterv1alpha1.KubernetesResourceCommitStatusSpec)) ([]string, field.ErrorList) {
		GinkgoHelper()
		raw, err := yaml.YAMLToJSON([]byte(template))
		Expect(err).NotTo(HaveOccurred())
		parsed, gvk, err := ParseTemplate(apiextensionsv1.JSON{Raw: raw})
		Expect(err).NotTo(HaveOccurred())
		Expect(gvk).To(Equal(jobGVK))
		krcsSpec := &promoterv1alpha1.KubernetesResourceCommitStatusSpec{
			Template: apiextensionsv1.JSON{Raw: raw},
			Phase:    promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: phase},
		}
		if mutate != nil {
			mutate(krcsSpec)
		}
		return ValidateSpec(context.Background(), compiler, gvk, ks, parsed, krcsSpec, field.NewPath("spec"))
	}
	const okPhase = "Resource.?status.?succeeded.orValue(0) > 0"

	errorAt := func(errs field.ErrorList, path, detail string) {
		GinkgoHelper()
		for _, e := range errs {
			if e.Field == path && strings.Contains(e.Detail, detail) {
				return
			}
		}
		msg := "none"
		if agg := errs.ToAggregate(); agg != nil {
			msg = agg.Error()
		}
		Fail("no error at " + path + " containing " + detail + "; errors: " + msg)
	}

	requiredAt := func(errs field.ErrorList, path string) {
		GinkgoHelper()
		Expect(errs).To(ContainElement(And(HaveField("Type", field.ErrorTypeRequired), HaveField("Field", path))), aggregate(errs))
	}

	It("accepts a template whose expressions and literals match the schema", func() {
		warnings, errs := validate(validJobTemplate, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = "Job ${ ResourceName } is ${ Phase } (${ Resource.?status.?succeeded.orValue(0) } succeeded)"
			s.Url = "https://ci.example.com/${ Sha }"
		})
		Expect(errs).To(BeEmpty(), aggregate(errs))
		Expect(warnings).To(BeEmpty())
	})

	It("rejects a standalone expression of the wrong type for the field", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, spec: {parallelism: "${ Sha }", suspend: "${ 1 }"}}`, okPhase, nil)
		errorAt(errs, "spec.template.spec.parallelism", "returns string but the field expects int")
		errorAt(errs, "spec.template.spec.suspend", "returns int but the field expects bool")
	})

	It("rejects a literal of the wrong type for the field", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, spec: {parallelism: "2", suspend: 1, backoffLimit: 1.5, template: "x"}}`, okPhase, nil)
		errorAt(errs, "spec.template.spec.parallelism", "literal returns string but the field expects int")
		errorAt(errs, "spec.template.spec.suspend", "literal returns int but the field expects bool")
		errorAt(errs, "spec.template.spec.backoffLimit", "literal returns double but the field expects int")
		errorAt(errs, "spec.template.spec.template", "literal returns string but the field expects an object")
	})

	It("rejects text with embedded expressions in a non-string field", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, spec: {parallelism: "x${ 1 }"}}`, okPhase, nil)
		errorAt(errs, "spec.template.spec.parallelism", "text with embedded expressions")
	})

	It("rejects fields the schema does not know, at any depth", func() {
		_, errs := validate(`
apiVersion: batch/v1
kind: Job
sepc: {}
spec:
  paralellism: 1
  template:
    spec:
      containers:
        - name: a
          imagePullPolicy: Always
`, okPhase, nil)
		errorAt(errs, "spec.template.sepc", `has no field "sepc"`)
		errorAt(errs, "spec.template.spec.paralellism", "unknown field")
		errorAt(errs, "spec.template.spec.template.spec.containers[0].imagePullPolicy", "unknown field")
		Expect(errs).To(HaveLen(3))
	})

	It("types map values and list items", func() {
		_, errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      nodeSelector:
        anything-goes: ${ 1 }
      containers:
        - name: a
          args: ["${ Sha }", "${ 1 }"]
`, okPhase, nil)
		errorAt(errs, "spec.template.spec.template.spec.nodeSelector.anything-goes", "returns int but the field expects string")
		errorAt(errs, "spec.template.spec.template.spec.containers[0].args[1]", "returns int but the field expects string")
		Expect(errs).To(HaveLen(2))
	})

	It("rejects unknown variables and unknown fields of typed variables", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, metadata: {labels: {a: "${ Shaa }", b: "${ Environment.propsed.hydrated.sha }"}}}`, okPhase, nil)
		errorAt(errs, "spec.template.metadata.labels[a]", "undeclared reference to 'Shaa'")
		errorAt(errs, "spec.template.metadata.labels[b]", "undefined field 'propsed'")
	})

	It("rejects a null result inside text", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, metadata: {labels: {a: "x-${ null }"}}}`, okPhase, nil)
		errorAt(errs, "spec.template.metadata.labels[a]", "result is null")
	})

	It("warns about template fields the controller ignores", func() {
		warnings, errs := validate(`{apiVersion: batch/v1, kind: Job, metadata: {name: x, namespace: y, finalizers: [f], labels: {ok: "1"}}, status: {succeeded: 1}}`, okPhase, nil)
		Expect(errs).To(BeEmpty(), aggregate(errs))
		Expect(warnings).To(ConsistOf(
			ContainSubstring("spec.template.metadata.name is ignored"),
			ContainSubstring("spec.template.metadata.namespace is ignored"),
			ContainSubstring("spec.template.metadata.finalizers is ignored"),
			ContainSubstring("spec.template.status is ignored"),
		))
	})

	It("rejects labels and annotations that are not strings", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, metadata: {labels: {num: 1}, annotations: "x"}}`, okPhase, nil)
		errorAt(errs, "spec.template.metadata.labels[num]", "must be a string")
		errorAt(errs, "spec.template.metadata.annotations", "must be an object of strings")
	})

	It("checks the phase expression's compilation and output type", func() {
		_, errs := validate(validJobTemplate, "Resource.status.nope == 1", nil)
		errorAt(errs, "spec.phase.expression", "undefined field 'nope'")
		_, errs = validate(validJobTemplate, "'success'", nil)
		errorAt(errs, "spec.phase.expression", "must return a boolean or an object")
		_, errs = validate(validJobTemplate, "{'phase': Resource.?status.?succeeded.orValue(0) > 0 ? 'success' : 'pending'}", nil)
		Expect(errs).To(BeEmpty(), aggregate(errs))
	})

	It("checks description and url", func() {
		_, errs := validate(validJobTemplate, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = "${ Phasee }"
			s.Url = "${ Resource.status.nope }"
		})
		errorAt(errs, "spec.description", "undeclared reference to 'Phasee'")
		errorAt(errs, "spec.url", "undefined field 'nope'")
	})

	It("rejects expressions that can remove a required field", func() {
		_, errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      containers: "${ null }"
`, okPhase, nil)
		errorAt(errs, "spec.template.spec.template.spec.containers", "would remove a required field")
		_, errs = validate(`
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      containers: "${ Branch == 'x' ? optional.of([]) : optional.none() }"
`, okPhase, nil)
		errorAt(errs, "spec.template.spec.template.spec.containers", "would remove a required field")
	})

	It("requires labels, annotations, description, and url expressions to render as text", func() {
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, metadata: {labels: {a: "${ PromotionStrategy.spec.environments }", b: "${ null }", c: "${ 1 + 1 }", d: "x-${ [1] }"}}}`, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = "${ Resource.?status.?conditions.orValue([]) }"
			s.Url = "${ Resource.?status.?succeeded }"
		})
		errorAt(errs, "spec.template.metadata.labels[a]", "returns list of object, but this value must be text")
		errorAt(errs, "spec.template.metadata.labels[b]", "returns null, but this value must be text")
		errorAt(errs, "spec.description", "but this value must be text")
		errorAt(errs, "spec.url", "returns optional int, but this value must be text")
		Expect(errs).To(HaveLen(4), aggregate(errs))
	})

	It("lets cel.bind name a lookup that description and url reuse", func() {
		_, errs := validate(validJobTemplate, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = `${ cel.bind(c, Resource.?status.?conditions.orValue([]).filter(c, c.type == 'Failed'), c.size() > 0 ? c[0].status : 'ok') }`
			s.Url = `https://ci.example.com/${ cel.bind(short, Sha.substring(0, 7), short + '/' + short) }`
		})
		Expect(errs).To(BeEmpty(), aggregate(errs))
	})

	It("suggests the declared field nearest to a misspelled one", func() {
		_, errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      containers:
        - name: a
          imag: busybox
          zzzzzzzz: 1
`, okPhase, nil)
		errorAt(errs, "spec.template.spec.template.spec.containers[0].imag", `(did you mean "image"?)`)
		for _, e := range errs {
			if e.Field == "spec.template.spec.template.spec.containers[0].zzzzzzzz" {
				Expect(e.Detail).NotTo(ContainSubstring("did you mean"))
			}
		}
	})

	It("reports required fields missing from objects written out in the template", func() {
		_, errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      restartPolicy: Never
`, okPhase, nil)
		requiredAt(errs, "spec.template.spec.template.spec.containers")
		Expect(errs).To(HaveLen(1), aggregate(errs))

		By("ignoring the placeholder default a built-in kind publishes on a required field")
		_, errs = validate(`{apiVersion: batch/v1, kind: Job, spec: {template: {spec: {containers: [{image: busybox}]}}}}`, okPhase, nil)
		requiredAt(errs, "spec.template.spec.template.spec.containers[0].name")
		Expect(errs).To(HaveLen(1), aggregate(errs))

		By("not looking inside an expression's result")
		_, errs = validate(`{apiVersion: batch/v1, kind: Job, spec: {template: {spec: {containers: "${ [{'image': 'busybox'}] }"}}}}`, okPhase, nil)
		Expect(errs).To(BeEmpty(), aggregate(errs))

		By("accepting a required field whose value is an expression")
		_, errs = validate(`{apiVersion: batch/v1, kind: Job, spec: {template: {spec: {containers: [{name: "${ Branch.replace('/', '-') }"}]}}}}`, okPhase, nil)
		Expect(errs).To(BeEmpty(), aggregate(errs))
	})

	It("checks only compilation when the kind's schema is unknown", func() {
		ks = nil
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, spec: {parallelism: "${ Sha }", paralellism: 1, x: "${ Shaa }"}}`, okPhase, nil)
		Expect(errs).To(HaveLen(1))
		errorAt(errs, "spec.template.spec.x", "undeclared reference to 'Shaa'")
	})
})

// widgetGVK is a CustomResourceDefinition kind whose schema carries constraints, defaults, and CEL rules, which
// only CRD schemas publish.
var widgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

func widgetSchema() *spec.Schema {
	size := *spec.Int64Property()
	size.Minimum, size.Maximum = new(1.0), new(10.0)
	mode := *spec.StringProperty()
	mode.Enum = []any{"fast", "slow"}
	mode.Default = "fast"
	name := *spec.StringProperty()
	name.Pattern = "^[a-z${}]+$"
	name.MaxLength = new(int64(8))
	tags := spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"array"}, MaxItems: new(int64(2)), Items: &spec.SchemaOrArray{Schema: spec.StringProperty()}}}
	limits := spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
		"min": *spec.Int64Property(),
		"max": *spec.Int64Property(),
	}}}
	limits.AddExtension("x-kubernetes-validations", []any{map[string]any{"rule": "self.min <= self.max", "message": "min must not exceed max"}})
	widgetSpec := spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Required: []string{"size", "mode"}, Properties: map[string]spec.Schema{
		"size": size, "mode": mode, "name": name, "tags": tags, "limits": limits,
	}}}
	widgetSpec.AddExtension("x-kubernetes-validations", []any{map[string]any{"rule": "self.size <= 5 || self.mode == 'slow'", "message": "big widgets must be slow"}})
	root := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{
		"apiVersion": *spec.StringProperty(),
		"kind":       *spec.StringProperty(),
		"metadata":   {SchemaProps: spec.SchemaProps{Type: []string{"object"}}},
		"spec":       widgetSpec,
	}}}
	root.AddExtension("x-kubernetes-validations", []any{map[string]any{"rule": "self.metadata.name.startsWith('w')", "message": "root rules are skipped"}})
	return root
}

// originResolver serves widgetSchema and reports it as a CRD, or as a built-in kind when crd is false.
type originResolver struct{ crd bool }

func (r originResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	s, _, err := r.ResolveSchemaWithOrigin(gvk)
	return s, err
}

func (r originResolver) ResolveSchemaWithOrigin(gvk schema.GroupVersionKind) (*spec.Schema, bool, error) {
	if gvk == widgetGVK {
		return widgetSchema(), r.crd, nil
	}
	return nil, false, resolver.ErrSchemaNotFound
}

var _ = Describe("ValidateSpec on a CustomResourceDefinition kind", func() {
	validate := func(crd bool, template string) field.ErrorList {
		GinkgoHelper()
		compiler, err := NewCompiler(originResolver{crd: crd}, log.Log)
		Expect(err).NotTo(HaveOccurred())
		ks, err := compiler.ResolveKindSchema(widgetGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(ks.CRD).To(Equal(crd))
		raw, err := yaml.YAMLToJSON([]byte(template))
		Expect(err).NotTo(HaveOccurred())
		parsed, gvk, err := ParseTemplate(apiextensionsv1.JSON{Raw: raw})
		Expect(err).NotTo(HaveOccurred())
		_, errs := ValidateSpec(context.Background(), compiler, gvk, ks, parsed, &promoterv1alpha1.KubernetesResourceCommitStatusSpec{
			Template: apiextensionsv1.JSON{Raw: raw},
			Phase:    promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
		}, field.NewPath("spec"))
		return errs
	}
	errorAt := func(errs field.ErrorList, path, detail string) {
		GinkgoHelper()
		for _, e := range errs {
			if e.Field == path && strings.Contains(e.Error(), detail) {
				return
			}
		}
		Fail("no error at " + path + " containing " + detail + "; errors: " + aggregate(errs))
	}

	requiredAt := func(errs field.ErrorList, path string) {
		GinkgoHelper()
		Expect(errs).To(ContainElement(And(HaveField("Type", field.ErrorTypeRequired), HaveField("Field", path))), aggregate(errs))
	}

	It("accepts a valid widget", func() {
		errs := validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 3, mode: fast, name: abc, tags: [a, b], limits: {min: 1, max: 2}}}`)
		Expect(errs).To(BeEmpty(), aggregate(errs))
	})

	It("counts schema defaults for CRD kinds only", func() {
		errs := validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {name: abc}}`)
		requiredAt(errs, "spec.template.spec.size")
		Expect(errs).To(HaveLen(1), aggregate(errs))

		errs = validate(false, `{apiVersion: example.com/v1, kind: Widget, spec: {name: abc}}`)
		requiredAt(errs, "spec.template.spec.size")
		requiredAt(errs, "spec.template.spec.mode")
	})

	It("checks the constraints of values without expressions", func() {
		errs := validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 20, mode: slow, name: ABCDEFGHIJ, tags: [a, b, c]}}`)
		errorAt(errs, "spec.template.spec.size", "less than or equal to 10")
		errorAt(errs, "spec.template.spec.name", "may not be more than 8")
		errorAt(errs, "spec.template.spec.tags", "at most 2 items")
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 1, mode: slow, name: ABC}}`)
		errorAt(errs, "spec.template.spec.name", "should match")

		By("skipping values computed by expressions")
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: "${ 20 }", mode: slow, name: "${ Branch }", tags: [a, b, "${ Sha }"]}}`)
		Expect(errs).To(BeEmpty(), aggregate(errs))

		By("resolving $${ escapes before checking")
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 1, mode: slow, name: "$${ab}"}}`)
		Expect(errs).To(BeEmpty(), aggregate(errs))
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 1, mode: slow, name: "$${ABCDEFGHIJ}"}}`)
		errorAt(errs, "spec.template.spec.name", "may not be more than 8")
	})

	It("evaluates CEL rules over values without expressions, with defaults applied", func() {
		By("applying the default mode before the rule reads it")
		errs := validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 8}}`)
		errorAt(errs, "spec.template.spec", "big widgets must be slow")
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: 8, mode: slow, limits: {min: 5, max: 1}}}`)
		errorAt(errs, "spec.template.spec.limits", "min must not exceed max")
		Expect(errs).To(HaveLen(1), aggregate(errs))

		By("skipping rules on a node with an expression below it")
		errs = validate(true, `{apiVersion: example.com/v1, kind: Widget, spec: {size: "${ 8 }", limits: {min: "${ 5 }", max: 1}}}`)
		Expect(errs).To(BeEmpty(), aggregate(errs))
	})
})

var _ = Describe("schemaChild", func() {
	obj := func(props map[string]spec.Schema, ext map[string]any) *spec.Schema {
		s := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: props}}
		for k, v := range ext {
			s.AddExtension(k, v)
		}
		return s
	}
	str := *spec.StringProperty()

	It("permits only declared properties of a closed object", func() {
		s := obj(map[string]spec.Schema{"a": str}, nil)
		_, known := schemaChild(s, "a")
		Expect(known).To(BeTrue())
		_, known = schemaChild(s, "b")
		Expect(known).To(BeFalse())
	})

	It("permits any key when the schema is open or unknown", func() {
		for name, s := range map[string]*spec.Schema{
			"nil":              nil,
			"preserve-unknown": obj(map[string]spec.Schema{"a": str}, map[string]any{"x-kubernetes-preserve-unknown-fields": true}),
			"embedded":         obj(map[string]spec.Schema{"a": str}, map[string]any{"x-kubernetes-embedded-resource": true}),
			"int-or-string":    obj(map[string]spec.Schema{"a": str}, map[string]any{"x-kubernetes-int-or-string": true}),
			"no-properties":    obj(nil, nil),
			"additional":       {SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{"a": str}, AdditionalProperties: &spec.SchemaOrBool{Allows: true}}},
			"union":            {SchemaProps: spec.SchemaProps{Type: []string{"object"}, Properties: map[string]spec.Schema{"a": str}, OneOf: []spec.Schema{str}}},
			"untyped":          {SchemaProps: spec.SchemaProps{Properties: map[string]spec.Schema{"a": str}}},
		} {
			_, known := schemaChild(s, "b")
			Expect(known).To(BeTrue(), name)
		}
	})

	It("returns the additionalProperties schema for map values", func() {
		s := &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}, AdditionalProperties: &spec.SchemaOrBool{Schema: &str}}}
		child, known := schemaChild(s, "anything")
		Expect(known).To(BeTrue())
		Expect(child).To(Equal(&str))
	})
})
