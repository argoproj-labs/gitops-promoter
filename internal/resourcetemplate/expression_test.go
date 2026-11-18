package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
)

var _ = Describe("parseSegments", func() {
	It("splits literals and expressions", func() {
		segments, err := parseSegments("a-${ Sha }-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(Equal([]segment{{text: "a-"}, {text: " Sha ", isExpr: true}, {text: "-b"}}))
	})

	It("handles nested braces and braces inside string literals", func() {
		segments, err := parseSegments(`${ {a: '}', b: "{"}['a'] }`)
		Expect(err).NotTo(HaveOccurred())
		Expect(segments).To(HaveLen(1))
		Expect(segments[0].isExpr).To(BeTrue())
		Expect(segments[0].text).To(Equal(` {a: '}', b: "{"}['a'] `))
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
			Environment: &promoterv1alpha1.EnvironmentStatus{
				Branch:   "environment/development",
				Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "0123456789abcdef0123456789abcdef01234567"}},
			},
		}.Env()
		in = NewInterpolator(exprcache.New(16), TemplateExpressionCachePrefix, env)
	})

	It("returns strings without expressions unchanged", func() {
		Expect(in.Value("plain")).To(Equal("plain"))
		Expect(in.Value("")).To(Equal(""))
	})

	It("concatenates embedded expressions as text", func() {
		Expect(in.Value("smoke-${ ShortSha }-${ Branch | replace('/', '-') }")).To(Equal("smoke-0123456-environment-development"))
		Expect(in.Value("n=${ 1 + 2 } ok=${ true } f=${ 1.5 }")).To(Equal("n=3 ok=true f=1.5"))
	})

	It("keeps the type of a standalone expression", func() {
		Expect(in.Value("${ 1 + 2 }")).To(Equal(int64(3)))
		Expect(in.Value("  ${ true }\n")).To(BeTrue(), "surrounding whitespace is ignored")
		Expect(in.Value("${ ['a', Sha] }")).To(Equal([]any{"a", "0123456789abcdef0123456789abcdef01234567"}))
		Expect(in.Value("${ {name: 'SHA', value: ShortSha} }")).To(Equal(map[string]any{"name": "SHA", "value": "0123456"}))
	})

	It("converts typed values through their JSON representation", func() {
		v, err := in.Value("${ Environment.Proposed.Hydrated }")
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(HaveKeyWithValue("sha", "0123456789abcdef0123456789abcdef01234567"))
	})

	It("returns nil for a standalone nil so the caller can drop the field", func() {
		Expect(in.Value("${ Environment.Active.Hydrated.Sha == '' ? nil : 'x' }")).To(BeNil())
	})

	It("rejects nil inside an embedded expression", func() {
		_, err := in.Value("prefix-${ nil }")
		Expect(err).To(MatchError(ContainSubstring("result is nil")))
	})

	It("offers keyBy for name/value lists", func() {
		in = NewInterpolator(exprcache.New(16), TemplateExpressionCachePrefix, TemplateData{Resource: map[string]any{"status": map[string]any{
			"results": []any{
				map[string]any{"name": "vulnerabilities", "value": "0"},
				map[string]any{"name": "endpoints", "value": []any{"a", "b"}},
				map[string]any{"value": "no name"},
				"not an object",
			},
		}}}.ReportEnv())
		Expect(in.Value("${ keyBy(Resource.status.results, 'name', 'value') }")).To(Equal(map[string]any{"vulnerabilities": "0", "endpoints": []any{"a", "b"}}))
		Expect(in.Value("${ keyBy(Resource.status.results, 'name', '').endpoints.name }")).To(Equal("endpoints"))
		Expect(in.Value("${ keyBy(Resource.status?.missing ?? [], 'name', 'value') }")).To(Equal(map[string]any{}))
		Expect(in.Value("${ let r = keyBy(Resource.status.results, 'name', 'value'); r.vulnerabilities == '0' && len(r.endpoints) == 2 }")).To(BeTrue())
	})

	It("stringifies standalone results in String", func() {
		Expect(in.String("${ 42 }")).To(Equal("42"))
		Expect(in.String("${ {a: 1} }")).To(Equal(`{"a":1}`))
	})

	It("reports compile and runtime errors with the expression", func() {
		_, err := in.Value("${ Nope }")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"Nope"`))
		_, err = in.Value("${ Environment.Nope }")
		Expect(err).To(HaveOccurred(), "fields of typed variables are checked at compile time")
		Expect(err.Error()).To(ContainSubstring(`"Environment.Nope"`))
		_, err = in.Value("${ 1 + }")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"1 +"`))
	})
})
