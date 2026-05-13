/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webrequest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("BuildRenderedHTTPRequestFromTemplates", func() {
	var (
		wrcs *promoterv1alpha1.WebRequestCommitStatus
		td   TemplateData
	)

	BeforeEach(func() {
		wrcs = &promoterv1alpha1.WebRequestCommitStatus{
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: "https://example.com",
				},
			},
		}
		td = TemplateData{Branch: "main"}
	})

	Describe("URL rendering", func() {
		It("renders a URL template with TemplateData", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/{{ .Branch }}/end"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/main/end"))
		})

		It("wraps URL template parse errors", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"
			wrcs.Spec.HTTPRequest.URLTemplate = "{{ invalid"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render URL template"))
		})

		It("propagates Branch onto the rendered request", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"
			td.Branch = "prod"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Branch).To(Equal("prod"))
		})
	})

	Describe("Body rendering", func() {
		It("renders an empty body when BodyTemplate is unset", func() {
			wrcs.Spec.HTTPRequest.Method = "POST"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Body).To(Equal(""))
		})

		It("renders a body template with TemplateData", func() {
			wrcs.Spec.HTTPRequest.Method = "POST"
			wrcs.Spec.HTTPRequest.BodyTemplate = `{"branch": "{{ .Branch }}"}`

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Body).To(Equal(`{"branch": "main"}`))
		})

		It("wraps body template parse errors", func() {
			wrcs.Spec.HTTPRequest.Method = "POST"
			wrcs.Spec.HTTPRequest.BodyTemplate = "{{ invalid"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render body template"))
		})
	})

	Describe("Headers rendering", func() {
		It("renders nil Headers when no header templates are set", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Headers).To(BeNil())
		})

		It("renders header templates with TemplateData", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"
			wrcs.Spec.HTTPRequest.HeaderTemplates = map[string]string{
				"X-Branch":     "{{ .Branch }}",
				"Content-Type": "application/json",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Headers).To(HaveKeyWithValue("X-Branch", "main"))
			Expect(req.Headers).To(HaveKeyWithValue("Content-Type", "application/json"))
		})

		It("wraps header template parse errors and includes the header name", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"
			wrcs.Spec.HTTPRequest.HeaderTemplates = map[string]string{
				"X-Bad": "{{ invalid",
			}

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render header template"))
			Expect(err.Error()).To(ContainSubstring(`"X-Bad"`))
		})
	})

	Describe("static Method field", func() {
		It("passes the static method through unchanged", func() {
			wrcs.Spec.HTTPRequest.Method = "GET"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("GET"))
			Expect(req.URL).To(Equal("https://example.com"))
		})

		It("accepts POST", func() {
			wrcs.Spec.HTTPRequest.Method = "POST"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("POST"))
		})

		It("errors when neither Method nor MethodTemplate is set", func() {
			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
		})

		It("errors when Method is an unsupported value", func() {
			wrcs.Spec.HTTPRequest.Method = "DELETE"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
		})
	})

	Describe("MethodTemplate", func() {
		It("renders a constant template", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "GET"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("GET"))
		})

		DescribeTable("accepts every method allowed by the static enum",
			func(method string) {
				wrcs.Spec.HTTPRequest.MethodTemplate = method

				req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

				Expect(err).ToNot(HaveOccurred())
				Expect(req.Method).To(Equal(method))
			},
			Entry("GET", "GET"),
			Entry("POST", "POST"),
			Entry("PUT", "PUT"),
			Entry("PATCH", "PATCH"),
		)

		It("trims surrounding whitespace and uppercases the result", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "  get  "

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("GET"))
		})

		It("trims leading and trailing newlines produced by multi-line templates", func() {
			// Multi-line templates are common; the post-render trim must handle the trailing newline
			// from a template that ends with a literal newline (e.g. `methodTemplate: |` YAML scalar).
			wrcs.Spec.HTTPRequest.MethodTemplate = "\n  POST  \n"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("POST"))
		})

		It("reads TemplateData fields beyond Branch (e.g. TriggerOutput) when rendering", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = `{{- if .TriggerOutput -}}` +
				`{{- $m := index .TriggerOutput "method" -}}` +
				`{{- if $m -}}{{- $m -}}{{- else -}}GET{{- end -}}` +
				`{{- else -}}GET{{- end -}}`
			td.TriggerOutput = map[string]any{"method": "PATCH"}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("PATCH"))
		})

		It("renders search GET when ResponseOutput.changeId is empty", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = `{{- if .ResponseOutput -}}` +
				`{{- $cid := index .ResponseOutput "changeId" -}}` +
				`{{- if and $cid (ne $cid "") -}}POST{{- else -}}GET{{- end -}}` +
				`{{- else -}}GET{{- end -}}`

			By("nil ResponseOutput → GET")
			td.ResponseOutput = nil
			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)
			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("GET"))

			By("empty changeId → GET")
			td.ResponseOutput = map[string]any{"changeId": ""}
			req, err = BuildRenderedHTTPRequestFromTemplates(wrcs, td)
			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("GET"))
		})

		It("renders close POST when ResponseOutput.changeId is set", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = `{{- if .ResponseOutput -}}` +
				`{{- $cid := index .ResponseOutput "changeId" -}}` +
				`{{- if and $cid (ne $cid "") -}}POST{{- else -}}GET{{- end -}}` +
				`{{- else -}}GET{{- end -}}`
			td.ResponseOutput = map[string]any{"changeId": "uuid-abc"}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("POST"))
		})

		It("errors when the template renders to an unsupported method", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "DELETE"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
			Expect(err.Error()).To(ContainSubstring("DELETE"))
		})

		It("errors when the template renders to an empty string", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "   "

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
		})

		It("errors when the template fails to parse", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "{{ invalid"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render method template"))
		})

		It("takes precedence over the static Method field when both are set", func() {
			// The CRD CEL XValidation rule rejects this combination at admission time, but the
			// renderer is defensive: if both are set, MethodTemplate wins.
			wrcs.Spec.HTTPRequest.Method = "GET"
			wrcs.Spec.HTTPRequest.MethodTemplate = "POST"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal("POST"))
		})
	})
})
