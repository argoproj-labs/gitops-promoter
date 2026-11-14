package utils_test

import (
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("test rendering a template", func() {
	tests := map[string]struct {
		data     any
		template string
		expected string
		options  []string
		wantErr  bool
	}{
		"can render template successfully": {
			template: "Name: {{ .Name }}",
			data: map[string]string{
				"Name": "John",
			},
			expected: "Name: John",
		},
		"can render using sprig functions": {
			template: "Name: {{ trunc 1 .Name }}",
			data: map[string]string{
				"Name": "John",
			},
			expected: "Name: J",
		},
		"cannot render using sensitive sprig functions": {
			template: "{{ env HOME }}",
			wantErr:  true,
		},
		"can render template with options": {
			template: "{{ .Foo }}",
			data: map[string]string{
				"Bar": "John",
			},
			options:  []string{"missingkey=zero"},
			expected: "",
			wantErr:  false,
		},
	}

	for name, test := range tests {
		It(name, func() {
			result, err := utils.RenderStringTemplate(test.template, test.data, test.options...)
			if test.wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(result).To(Equal(test.expected))
		})
	}
})

var _ = Describe("RenderURLTemplate", func() {
	data := map[string]string{"Environment": "environment/prod", "Host": "promoter.example.com"}

	It("renders an https URL", func() {
		rendered, err := utils.RenderURLTemplate("https://{{ .Host }}/?env={{ urlQueryEscape .Environment }}", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(rendered).To(Equal("https://promoter.example.com/?env=environment%2Fprod"))
	})

	It("renders an http URL", func() {
		rendered, err := utils.RenderURLTemplate("http://{{ .Host }}/", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(rendered).To(Equal("http://promoter.example.com/"))
	})

	It("passes options to the template", func() {
		_, err := utils.RenderURLTemplate("https://{{ .Missing }}/", data, "missingkey=error")
		Expect(err).To(MatchError(ContainSubstring("failed to render URL template")))
	})

	It("rejects a template that does not parse", func() {
		_, err := utils.RenderURLTemplate("https://{{ .Host", data)
		Expect(err).To(MatchError(ContainSubstring("failed to render URL template")))
	})

	It("rejects anything that is not an http or https URL", func() {
		for _, template := range []string{"javascript:alert(1)", "ftp://{{ .Host }}/", "{{ .Host }}/path", ""} {
			_, err := utils.RenderURLTemplate(template, data)
			Expect(err).To(MatchError(ContainSubstring("URL scheme is not http or https")), template)
		}
	})

	It("rejects a URL that cannot be parsed", func() {
		_, err := utils.RenderURLTemplate("https://{{ .Host }}/%zz", data)
		Expect(err).To(MatchError(ContainSubstring("failed to parse URL")))
	})
})
