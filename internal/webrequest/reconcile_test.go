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
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	methodGET         = "GET"
	methodPOST        = "POST"
	invalidGoTemplate = "{{ invalid"
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
					// Default to a valid method so tests focused on URL/body/header rendering
					// don't need to set one themselves. Tests that exercise method resolution
					// override this explicitly.
					MethodTemplate: methodGET,
				},
			},
		}
		td = TemplateData{Branch: "main"}
	})

	Describe("URL rendering", func() {
		It("renders a URL template with TemplateData", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/{{ .Branch }}/end"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/main/end"))
		})

		It("wraps URL template parse errors", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = invalidGoTemplate

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render URL template"))
		})

		It("propagates Branch onto the rendered request", func() {
			td.Branch = "prod"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Branch).To(Equal("prod"))
		})
	})

	Describe("Body rendering", func() {
		It("renders an empty body when BodyTemplate is unset", func() {
			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Body).To(Equal(""))
		})

		It("renders a body template with TemplateData", func() {
			wrcs.Spec.HTTPRequest.BodyTemplate = `{"branch": "{{ .Branch }}"}`

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Body).To(Equal(`{"branch": "main"}`))
		})

		It("wraps body template parse errors", func() {
			wrcs.Spec.HTTPRequest.BodyTemplate = invalidGoTemplate

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render body template"))
		})
	})

	Describe("Headers rendering", func() {
		It("renders nil Headers when no header templates are set", func() {
			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Headers).To(BeNil())
		})

		It("renders header templates with TemplateData", func() {
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
			wrcs.Spec.HTTPRequest.HeaderTemplates = map[string]string{
				"X-Bad": invalidGoTemplate,
			}

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render header template"))
			Expect(err.Error()).To(ContainSubstring(`"X-Bad"`))
		})
	})

	Describe("QueryTemplates rendering", func() {
		It("leaves the URL unchanged when QueryTemplates is nil", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api"))
		})

		It("preserves inline query param order when QueryTemplates is nil", func() {
			// Without the guard, q.Encode() would rewrite ?b=2&a=1 → ?a=1&b=2 (sorted).
			// Order-sensitive or pre-signed URLs must not be rewritten when no queryTemplates are set.
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api?b=2&a=1"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api?b=2&a=1"))
		})

		It("appends a single static query parameter", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"key": "value",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api?key=value"))
		})

		It("appends multiple parameters sorted alphabetically by key", func() {
			// url.Values.Encode sorts keys; the result must be deterministic regardless
			// of Go map-iteration order.
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"z-param": "last",
				"a-param": "first",
				"m-param": "middle",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api?a-param=first&m-param=middle&z-param=last"))
		})

		It("renders TemplateData variables inside query parameter values", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"env": "{{ .Branch }}",
			}
			td.Branch = "staging"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api?env=staging"))
		})

		It("merges queryTemplates with existing inline params already in urlTemplate", func() {
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api?existing=yes"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"added": "new",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			// Both params must be present; Encode() sorts keys alphabetically.
			Expect(req.URL).To(Equal("https://example.com/api?added=new&existing=yes"))
		})

		It("queryTemplates value overrides an inline param with the same key", func() {
			// urlTemplate carries ?pagination.limit=30; queryTemplates overrides to 50.
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api?pagination.limit=30"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"pagination.limit": "50",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal("https://example.com/api?pagination.limit=50"))
		})

		It("percent-encodes special characters: literal + becomes %2B, space becomes +", func() {
			// url.Values.Encode uses application/x-www-form-urlencoded:
			//   literal + (used as RHACS field separator) → %2B
			//   space → +
			// Most HTTP servers (including RHACS) decode + as space in query values,
			// so this encoding is functionally correct.
			wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/api"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"query": "Namespace:dev+Platform Component:false",
			}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.URL).To(Equal(
				"https://example.com/api?query=Namespace%3Adev%2BPlatform+Component%3Afalse",
			))
		})

		It("wraps render errors and includes the failing parameter name", func() {
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"bad-param": invalidGoTemplate,
			}

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render query template"))
			Expect(err.Error()).To(ContainSubstring(`"bad-param"`))
		})

		// The template below models a two-phase change-management workflow that produces
		// two structurally different base URLs depending on ResponseOutput:
		//   search branch (no prior changeId) → base URL already carries an inline ?commit=<sha>
		//   close  branch (prior changeId set) → base URL has no inline params
		// queryTemplates are merged on top in both cases, so this table exercises:
		//   • merging queryTemplates with existing inline params (search branch)
		//   • appending queryTemplates to a clean URL (close branch)
		//   • queryTemplates overriding an inline param from the rendered URL (commit override)
		DescribeTable("conditional urlTemplate combined with queryTemplates",
			func(responseOutput map[string]any, triggerVariables map[string]any, queryTemplates map[string]string, expectedURL string) {
				wrcs.Spec.HTTPRequest.URLTemplate = `
{{- if .ResponseOutput -}}
  {{- $cid := index .ResponseOutput "changeId" -}}
  {{- if and $cid (ne $cid "") -}}https://change-management.example.com/close/{{ $cid }}
  {{- else -}}https://change-management.example.com/search?commit={{ index .TriggerVariables "sha" }}
  {{- end -}}
{{- else -}}https://change-management.example.com/search?commit={{ index .TriggerVariables "sha" }}
{{- end -}}`
				wrcs.Spec.HTTPRequest.QueryTemplates = queryTemplates
				td.ResponseOutput = responseOutput
				td.TriggerVariables = triggerVariables

				req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

				Expect(err).ToNot(HaveOccurred())
				Expect(req.URL).To(Equal(expectedURL))
			},
			Entry("search branch: static queryTemplates are merged with the inline commit param",
				/*responseOutput*/ nil,
				/*triggerVariables*/ map[string]any{"sha": "abc123"},
				/*queryTemplates*/ map[string]string{
					"format":           "json",
					"pagination.limit": "50",
				},
				// Encode() sorts keys: commit < format < pagination.limit
				"https://change-management.example.com/search?commit=abc123&format=json&pagination.limit=50",
			),
			Entry("close branch: templated queryTemplates are appended to a param-free URL",
				/*responseOutput*/ map[string]any{"changeId": "CHG-9876"},
				/*triggerVariables*/ nil,
				/*queryTemplates*/ map[string]string{
					"format": "json",
					"env":    "{{ .Branch }}", // Branch is "main" from BeforeEach
				},
				// Encode() sorts keys: env < format
				"https://change-management.example.com/close/CHG-9876?env=main&format=json",
			),
			Entry("search branch: queryTemplates override the inline commit param from the rendered URL",
				/*responseOutput*/ nil,
				/*triggerVariables*/ map[string]any{"sha": "abc123"},
				/*queryTemplates*/ map[string]string{
					"commit": "override-sha", // q.Set replaces the inline ?commit=abc123
				},
				"https://change-management.example.com/search?commit=override-sha",
			),
		)

		It("round-trips an RHACS-style multi-field filter with pagination", func() {
			// Mirrors the real-world RHACS use-case from issue #1898: the query value
			// uses + as a field separator and contains spaces in field names.
			// We verify round-trip correctness by parsing the URL back and decoding
			// the values, which lets us stay independent of the exact + vs %20 encoding.
			wrcs.Spec.HTTPRequest.URLTemplate = "https://central.example.com/v1/alerts"
			wrcs.Spec.HTTPRequest.QueryTemplates = map[string]string{
				"query":             `Namespace:petclinic-{{ .Branch | splitList "/" | last }}+Platform Component:false+Entity Type:DEPLOYMENT+Violation State:ACTIVE`,
				"pagination.limit":  "50",
				"pagination.offset": "0",
			}
			td.Branch = "env/staging"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			parsed, err := url.Parse(req.URL)
			Expect(err).ToNot(HaveOccurred())
			q := parsed.Query()
			Expect(q.Get("pagination.limit")).To(Equal("50"))
			Expect(q.Get("pagination.offset")).To(Equal("0"))
			// url.Values.Get decodes %2B back to + and + back to space, so the round-tripped
			// value must exactly match the original unencoded template value.
			Expect(q.Get("query")).To(Equal(
				"Namespace:petclinic-staging+Platform Component:false+Entity Type:DEPLOYMENT+Violation State:ACTIVE",
			))
		})
	})

	Describe("Method resolution", func() {
		It("errors when neither Method nor MethodTemplate is set", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = ""

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
		})

		// Backward-compat regression test for the deprecated `Method` field.
		It("honors the deprecated static Method field as a fallback when MethodTemplate is empty", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = ""
			wrcs.Spec.HTTPRequest.Method = methodGET //nolint:staticcheck // SA1019: intentional deprecated-field regression coverage.

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodGET))
		})
	})

	Describe("MethodTemplate", func() {
		// The BeforeEach already sets MethodTemplate to a constant "GET" so this trivially
		// confirms rendering a constant template works.
		It("renders a constant template", func() {
			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodGET))
		})

		DescribeTable("accepts every method allowed by the static enum",
			func(method string) {
				wrcs.Spec.HTTPRequest.MethodTemplate = method

				req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

				Expect(err).ToNot(HaveOccurred())
				Expect(req.Method).To(Equal(method))
			},
			Entry(methodGET, methodGET),
			Entry(methodPOST, methodPOST),
			Entry("PUT", "PUT"),
			Entry("PATCH", "PATCH"),
			Entry("DELETE", "DELETE"),
		)

		It("trims surrounding whitespace and uppercases the result", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "  get  "

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodGET))
		})

		It("trims leading and trailing newlines produced by multi-line templates", func() {
			// Multi-line templates are common; the post-render trim must handle the trailing newline
			// from a template that ends with a literal newline (e.g. `methodTemplate: |` YAML scalar).
			wrcs.Spec.HTTPRequest.MethodTemplate = "\n  POST  \n"

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodPOST))
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
			Expect(req.Method).To(Equal(methodGET))

			By("empty changeId → GET")
			td.ResponseOutput = map[string]any{"changeId": ""}
			req, err = BuildRenderedHTTPRequestFromTemplates(wrcs, td)
			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodGET))
		})

		It("renders close POST when ResponseOutput.changeId is set", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = `{{- if .ResponseOutput -}}` +
				`{{- $cid := index .ResponseOutput "changeId" -}}` +
				`{{- if and $cid (ne $cid "") -}}POST{{- else -}}GET{{- end -}}` +
				`{{- else -}}GET{{- end -}}`
			td.ResponseOutput = map[string]any{"changeId": "uuid-abc"}

			req, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).ToNot(HaveOccurred())
			Expect(req.Method).To(Equal(methodPOST))
		})

		It("errors when the template renders to an unsupported method", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "HEAD"

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
			Expect(err.Error()).To(ContainSubstring("HEAD"))
		})

		It("errors when the template renders to an empty string", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = "   "

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid HTTP method"))
		})

		It("errors when the template fails to parse", func() {
			wrcs.Spec.HTTPRequest.MethodTemplate = invalidGoTemplate

			_, err := BuildRenderedHTTPRequestFromTemplates(wrcs, td)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to render method template"))
		})
	})
})
