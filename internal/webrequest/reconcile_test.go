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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
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

// recordingEmitter records the TemplateData each CommitStatus is emitted with.
type recordingEmitter struct {
	emitted []TemplateData
}

func (e *recordingEmitter) EmitCommitStatus(_ context.Context, _ *promoterv1alpha1.WebRequestCommitStatus, _, _, _ string, _ promoterv1alpha1.CommitStatusPhase, td TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	e.emitted = append(e.emitted, td)
	return &promoterv1alpha1.CommitStatus{}, nil
}

// okExecutor answers every HTTP request with a 200 and body.
type okExecutor struct {
	body any
}

func (e okExecutor) Execute(context.Context, *promoterv1alpha1.WebRequestCommitStatus, TemplateData) (HTTPResponse, error) {
	return HTTPResponse{StatusCode: 200, Body: e.body}, nil
}

var _ = Describe("Polling mode skip of an already successful SHA", func() {
	const branch = "environment/development"

	newInputs := func(context promoterv1alpha1.ContextMode) (*promoterv1alpha1.WebRequestCommitStatus, *promoterv1alpha1.PromotionStrategy) {
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				Key:                 "external-approval",
				ReportOn:            constants.CommitRefProposed,
				DescriptionTemplate: `Build: {{ index .SuccessVariables "tag" }}`,
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Variables:  &promoterv1alpha1.OutputSpec{Expression: `{"tag": "v1.2.3", "branch": Branch}`},
						Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: context,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: time.Minute}},
				},
			},
		}
		ps := &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				RepositoryReference:    promoterv1alpha1.ObjectReference{Name: "repo"},
				ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "external-approval"}},
				Environments:           []promoterv1alpha1.Environment{{Branch: branch}},
			},
			Status: promoterv1alpha1.PromotionStrategyStatus{
				Environments: []promoterv1alpha1.EnvironmentStatus{{
					Branch:   branch,
					Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc123"}},
				}},
			},
		}
		return wrcs, ps
	}

	DescribeTable("still exposes success.when.variables to the CommitStatus templates",
		func(context promoterv1alpha1.ContextMode) {
			ctx := GinkgoT().Context()
			emitter := &recordingEmitter{}
			r := NewReconciler(okExecutor{}, emitter)
			wrcs, ps := newInputs(context)
			reconcile := r.ReconcileWebRequestCommitStatusEnvironments
			if context == promoterv1alpha1.ContextPromotionStrategy {
				reconcile = r.ReconcileWebRequestCommitStatusPromotionStrategy
			}

			By("making the HTTP request and succeeding")
			_, _, _, err := reconcile(ctx, wrcs, ps, NamespaceMetadata{})
			Expect(err).NotTo(HaveOccurred())
			Expect(emitter.emitted).To(HaveLen(1))
			Expect(emitter.emitted[0].SuccessVariables).To(HaveKeyWithValue("tag", "v1.2.3"))

			By("skipping the HTTP request on the next reconcile because the SHA already succeeded")
			_, _, _, err = reconcile(ctx, wrcs, ps, NamespaceMetadata{})
			Expect(err).NotTo(HaveOccurred())
			Expect(emitter.emitted).To(HaveLen(2))
			Expect(emitter.emitted[1].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
			Expect(emitter.emitted[1].SuccessVariables).To(Equal(emitter.emitted[0].SuccessVariables),
				"the skip must evaluate success.when.variables the same way the request did")
		},
		Entry("environments context", promoterv1alpha1.ContextEnvironments),
		Entry("promotionstrategy context", promoterv1alpha1.ContextPromotionStrategy),
	)

	// The documented pattern: variables read the response when there is one and otherwise fall back to the
	// SuccessOutput they persisted, so the skip, which has no response, still sees the values from the request.
	DescribeTable("keeps response-derived success.when.variables that fall back to SuccessOutput",
		func(context promoterv1alpha1.ContextMode) {
			ctx := GinkgoT().Context()
			emitter := &recordingEmitter{}
			r := NewReconciler(okExecutor{body: map[string]any{"buildTag": "v1.2.3", "buildRunId": "42"}}, emitter)
			wrcs, ps := newInputs(context)
			wrcs.Spec.Success.When = promoterv1alpha1.WhenWithOutputSpec{
				Variables: &promoterv1alpha1.OutputSpec{Expression: `
					let prev = SuccessOutput ?? { tag: "pending", runId: "", ok: false };
					Response != nil ? {
						tag:   Response.Body.buildTag   ?? prev.tag,
						runId: Response.Body.buildRunId ?? prev.runId,
						ok:    Response.StatusCode == 200,
					} : prev`},
				Expression: `Variables.ok`,
				Output:     &promoterv1alpha1.OutputSpec{Expression: `Variables`},
			}
			reconcile := r.ReconcileWebRequestCommitStatusEnvironments
			if context == promoterv1alpha1.ContextPromotionStrategy {
				reconcile = r.ReconcileWebRequestCommitStatusPromotionStrategy
			}

			By("making the HTTP request and succeeding")
			_, _, _, err := reconcile(ctx, wrcs, ps, NamespaceMetadata{})
			Expect(err).NotTo(HaveOccurred())
			Expect(emitter.emitted).To(HaveLen(1))
			Expect(emitter.emitted[0].SuccessVariables).To(HaveKeyWithValue("tag", "v1.2.3"))
			Expect(emitter.emitted[0].SuccessVariables).To(HaveKeyWithValue("runId", "42"))

			By("skipping the HTTP request on the next reconcile because the SHA already succeeded")
			_, _, _, err = reconcile(ctx, wrcs, ps, NamespaceMetadata{})
			Expect(err).NotTo(HaveOccurred())
			Expect(emitter.emitted).To(HaveLen(2))
			Expect(emitter.emitted[1].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
			Expect(emitter.emitted[1].SuccessVariables).To(HaveKeyWithValue("tag", "v1.2.3"))
			Expect(emitter.emitted[1].SuccessVariables).To(HaveKeyWithValue("runId", "42"))
		},
		Entry("environments context", promoterv1alpha1.ContextEnvironments),
		Entry("promotionstrategy context", promoterv1alpha1.ContextPromotionStrategy),
	)
})
