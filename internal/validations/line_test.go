package validations_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

var _ = Describe("TagName", func() {
	It("appends -tag to the environment branch", func() {
		Expect(validations.TagName("environment/prod", "")).To(Equal("environment/prod-tag"))
	})

	It("nests the activePath under the tag, as the proposed branch does", func() {
		Expect(validations.TagName("environment/prod", "apps/payments")).To(Equal("environment/prod-tag/apps/payments"))
	})
})

var _ = Describe("Line", func() {
	at := time.Date(2026, 11, 13, 15, 2, 11, 0, time.UTC)

	Describe("Marshal", func() {
		It("writes the line from the proposal", func() {
			line := validations.Line{
				Target: "environment/prod-tag",
				Source: validations.SourceResource,
				By:     "CommitValidation/demo-prod",
				Reason: "CHG-1234 approved",
				At:     at,
			}
			Expect(line.Marshal()).To(Equal(
				`{"target":"environment/prod-tag","source":"Resource","by":"CommitValidation/demo-prod","reason":"CHG-1234 approved","at":"2026-11-13T15:02:11Z"}`))
		})

		It("omits what is not set", func() {
			Expect(validations.Line{Target: "environment/prod-tag"}.Marshal()).To(Equal(`{"target":"environment/prod-tag"}`))
		})

		It("writes the time in UTC", func() {
			line := validations.Line{Target: "t", At: at.In(time.FixedZone("plus2", 2*60*60))}
			Expect(line.Marshal()).To(Equal(`{"target":"t","at":"2026-11-13T15:02:11Z"}`))
		})

		It("keeps a reason with a newline on one line", func() {
			text, err := validations.Line{Target: "t", Reason: "first\nsecond"}.Marshal()
			Expect(err).NotTo(HaveOccurred())
			Expect(text).NotTo(ContainSubstring("\n"))
			parsed, ok := validations.ParseLine(text)
			Expect(ok).To(BeTrue())
			Expect(parsed.Reason).To(Equal("first\nsecond"))
		})

		It("refuses a validation with no target", func() {
			_, err := validations.Line{Source: validations.SourceGit}.Marshal()
			Expect(err).To(MatchError(ContainSubstring("no target")))
		})
	})

	Describe("ParseLine", func() {
		It("round-trips a line it wrote", func() {
			line := validations.Line{
				Target: "environment/prod-tag",
				Source: validations.SourceAutomatic,
				By:     "environment/staging successful",
				At:     at,
				ID:     "abc/7",
			}
			text, err := line.Marshal()
			Expect(err).NotTo(HaveOccurred())
			parsed, ok := validations.ParseLine(text)
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(line))
		})

		It("reads a hand-written line that names only the target as a Git validation", func() {
			parsed, ok := validations.ParseLine(`{"target":"environment/prod-tag","reason":"hotfix"}`)
			Expect(ok).To(BeTrue())
			Expect(parsed.Target).To(Equal("environment/prod-tag"))
			Expect(parsed.Source).To(Equal(validations.SourceGit))
			Expect(parsed.Reason).To(Equal("hotfix"))
			Expect(parsed.By).To(BeEmpty())
			Expect(parsed.At.IsZero()).To(BeTrue())
			Expect(parsed.Automatic()).To(BeFalse())
		})

		It("reads an unknown source as Git", func() {
			parsed, ok := validations.ParseLine(`{"target":"t","source":"Jenkins"}`)
			Expect(ok).To(BeTrue())
			Expect(parsed.Source).To(Equal(validations.SourceGit))
		})

		It("keeps the validation and drops a time it cannot read", func() {
			parsed, ok := validations.ParseLine(`{"target":"t","at":"yesterday"}`)
			Expect(ok).To(BeTrue())
			Expect(parsed.At.IsZero()).To(BeTrue())
		})

		It("tolerates surrounding whitespace and unknown fields", func() {
			parsed, ok := validations.ParseLine("  {\"target\":\"t\",\"ticket\":42}\t")
			Expect(ok).To(BeTrue())
			Expect(parsed.Target).To(Equal("t"))
		})

		DescribeTable("ignores text that is not a validation",
			func(text string) {
				_, ok := validations.ParseLine(text)
				Expect(ok).To(BeFalse())
			},
			Entry("a blank line", ""),
			Entry("whitespace", "   "),
			Entry("prose", "validated by jane"),
			Entry("broken JSON", `{"target":"t"`),
			Entry("a JSON array", `["target"]`),
			Entry("an object with no target", `{"reason":"hotfix"}`),
			Entry("an object with an empty target", `{"target":"  "}`),
			Entry("a target that is not a string", `{"target":7}`),
		)

		It("marks only Automatic lines as automatic", func() {
			automatic, _ := validations.ParseLine(`{"target":"t","source":"Automatic"}`)
			resource, _ := validations.ParseLine(`{"target":"t","source":"Resource"}`)
			Expect(automatic.Automatic()).To(BeTrue())
			Expect(resource.Automatic()).To(BeFalse())
		})
	})

	Describe("ParseNote", func() {
		It("returns every validation in order and skips the blank lines git notes append leaves", func() {
			note := `{"target":"environment/staging-tag","source":"Automatic"}

{"target":"environment/prod-tag","reason":"hotfix"}

not a validation
{"target":"environment/prod-tag","source":"Resource"}
`
			lines := validations.ParseNote(note)
			Expect(lines).To(HaveLen(3))
			Expect(lines[0].Target).To(Equal("environment/staging-tag"))
			Expect(lines[0].Source).To(Equal(validations.SourceAutomatic))
			Expect(lines[1].Source).To(Equal(validations.SourceGit))
			Expect(lines[2].Source).To(Equal(validations.SourceResource))
		})

		It("returns nothing for an empty note", func() {
			Expect(validations.ParseNote("")).To(BeEmpty())
		})
	})
})
