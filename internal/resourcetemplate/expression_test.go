package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseSegments", func() {
	It("splits literals and expressions", func() {
		segments, err := parseSegments("a-${ Sha }-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(Equal([]segment{{text: "a-"}, {text: " Sha ", isExpr: true}, {text: "-b"}}))
	})

	It("handles nested braces and braces inside string literals", func() {
		segments, err := parseSegments(`${ {'a': '}', 'b': "{"}['a'] }`)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].isExpr).To(BeTrue())
		Expect(segments[0].text).To(Equal(` {'a': '}', 'b': "{"}['a'] `))
	})

	It("escapes $${ to a literal ${", func() {
		segments, err := parseSegments("$${ not an expression } ${ Sha }")
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(Equal([]segment{{text: "${ not an expression } "}, {text: " Sha ", isExpr: true}}))
	})

	It("rejects unterminated expressions", func() {
		_, err := parseSegments("${ Sha ")
		Expect(err).To(MatchError(ContainSubstring("missing closing }")))
		_, err = parseSegments(`${ 'open }`)
		Expect(err).To(MatchError(ContainSubstring("unterminated string literal")))
	})
})

var _ = Describe("Interpolator", func() {
	var in *Interpolator

	BeforeEach(func() {
		env := TemplateData{
			Branch:   "environment/development",
			Sha:      "0123456789abcdef0123456789abcdef01234567",
			ShortSha: "0123456",
			Environment: map[string]any{
				"branch":   "environment/development",
				"proposed": map[string]any{"hydrated": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}},
			},
		}.Env()
		in = NewInterpolator(newTestCompiler(), TemplateVariables, jobGVK, env)
	})

	It("returns strings without expressions unchanged", func() {
		Expect(in.Value("plain", nil)).To(Equal("plain"))
		Expect(in.Value("", nil)).To(Equal(""))
	})

	It("concatenates embedded expressions as text", func() {
		Expect(in.Value("smoke-${ ShortSha }-${ Branch.replace('/', '-') }", nil)).To(Equal("smoke-0123456-environment-development"))
		Expect(in.Value("n=${ 1 + 2 } ok=${ true } f=${ 1.5 }", nil)).To(Equal("n=3 ok=true f=1.5"))
	})

	It("keeps the type of a standalone expression", func() {
		Expect(in.Value("${ 1 + 2 }", nil)).To(Equal(int64(3)))
		Expect(in.Value("  ${ true }\n", nil)).To(BeTrue(), "surrounding whitespace is ignored")
		Expect(in.Value("${ ['a', Sha] }", nil)).To(Equal([]any{"a", "0123456789abcdef0123456789abcdef01234567"}))
		Expect(in.Value("${ {'name': 'SHA', 'value': ShortSha} }", nil)).To(Equal(map[string]any{"name": "SHA", "value": "0123456"}))
	})

	It("returns nil for a standalone null or absent optional so the caller can drop the field", func() {
		Expect(in.Value("${ null }", nil)).To(BeNil())
		Expect(in.Value("${ has(Environment.active) ? optional.of('x') : optional.none() }", nil)).To(BeNil())
		Expect(in.Value("${ Environment.?active.?hydrated.?sha }", nil)).To(BeNil())
		Expect(in.Value("${ Environment.?proposed.?hydrated.?sha }", nil)).To(Equal("0123456789abcdef0123456789abcdef01234567"))
	})

	It("rejects null inside an embedded expression", func() {
		_, err := in.Value("prefix-${ null }", nil)
		Expect(err).To(MatchError(ContainSubstring("result is null")))
	})

	It("checks standalone results against the field type", func() {
		spec := FieldType(jobDeclType(), "spec")
		Expect(in.Value("${ 2 }", FieldType(spec, "parallelism"))).To(Equal(int64(2)))
		_, err := in.Value("${ 'two' }", FieldType(spec, "parallelism"))
		Expect(err).To(MatchError(ContainSubstring("returns string but the field expects int")))
		_, err = in.Value("replicas-${ 2 }", FieldType(spec, "parallelism"))
		Expect(err).To(MatchError(ContainSubstring("text with embedded expressions")))
	})

	It("stringifies standalone results in String", func() {
		Expect(in.String("${ 42 }")).To(Equal("42"))
		Expect(in.String("${ {'a': 1} }")).To(Equal(`{"a":1}`))
	})

	It("reports compile and runtime errors with the expression", func() {
		_, err := in.Value("${ Nope }", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"Nope"`))
		_, err = in.Value("${ Environment.Nope }", nil)
		Expect(err).To(HaveOccurred(), "fields of typed variables are checked at compile time")
		Expect(err.Error()).To(ContainSubstring(`"Environment.Nope"`))
		_, err = in.Value("${ 1 + }", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"1 +"`))
		_, err = in.Value("${ Environment.active.hydrated.sha }", nil)
		Expect(err).To(HaveOccurred(), "an absent field fails at runtime")
		Expect(err.Error()).To(ContainSubstring("no such key"))
	})
})
