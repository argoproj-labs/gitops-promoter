package resourcetemplate

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kube-openapi/pkg/validation/spec"
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
		return ValidateSpec(compiler, gvk, ks, parsed, krcsSpec, field.NewPath("spec"))
	}
	const okPhase = "Resource.?status.?succeeded.orValue(0) > 0"

	errorAt := func(errs field.ErrorList, path, detail string) {
		GinkgoHelper()
		for _, e := range errs {
			if e.Field == path && strings.Contains(e.Detail, detail) {
				return
			}
		}
		Fail("no error at " + path + " containing " + detail + "; errors: " + errs.ToAggregate().Error())
	}

	It("accepts a template whose expressions and literals match the schema", func() {
		warnings, errs := validate(validJobTemplate, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = "Job ${ ResourceName } is ${ Phase } (${ Resource.?status.?succeeded.orValue(0) } succeeded)"
			s.Url = "https://ci.example.com/${ Sha }"
		})
		Expect(errs).To(BeEmpty(), errs.ToAggregate())
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
		Expect(errs).To(BeEmpty(), errs.ToAggregate())
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
		Expect(errs).To(BeEmpty(), errs.ToAggregate())
	})

	It("checks description and url", func() {
		_, errs := validate(validJobTemplate, okPhase, func(s *promoterv1alpha1.KubernetesResourceCommitStatusSpec) {
			s.Description = "${ Phasee }"
			s.Url = "${ Resource.status.nope }"
		})
		errorAt(errs, "spec.description", "undeclared reference to 'Phasee'")
		errorAt(errs, "spec.url", "undefined field 'nope'")
	})

	It("checks only compilation when the kind's schema is unknown", func() {
		ks = nil
		_, errs := validate(`{apiVersion: batch/v1, kind: Job, spec: {parallelism: "${ Sha }", paralellism: 1, x: "${ Shaa }"}}`, okPhase, nil)
		Expect(errs).To(HaveLen(1))
		errorAt(errs, "spec.template.spec.x", "undeclared reference to 'Shaa'")
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
