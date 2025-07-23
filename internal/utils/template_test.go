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
