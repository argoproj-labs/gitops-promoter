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
	"errors"
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

// failingEmitter accepts every CommitStatus except failBranch's, which it rejects as the API server can.
type failingEmitter struct {
	failBranch string
}

func (e *failingEmitter) EmitCommitStatus(_ context.Context, _ *promoterv1alpha1.WebRequestCommitStatus, _, branch, _ string, _ promoterv1alpha1.CommitStatusPhase, _ TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	if branch == e.failBranch {
		return nil, errors.New("injected CommitStatus write failure")
	}
	return &promoterv1alpha1.CommitStatus{}, nil
}

// countingExecutor answers every HTTP request with a 200 and counts them.
type countingExecutor struct {
	requests int
}

func (e *countingExecutor) Execute(context.Context, *promoterv1alpha1.WebRequestCommitStatus, TemplateData) (HTTPResponse, error) {
	e.requests++
	return HTTPResponse{StatusCode: 200}, nil
}

var _ = Describe("A CommitStatus write failure", func() {
	branches := []string{"environment/development", "environment/staging", "environment/production"}

	newPromotionStrategy := func() *promoterv1alpha1.PromotionStrategy {
		ps := &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				RepositoryReference:    promoterv1alpha1.ObjectReference{Name: "repo"},
				ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "external-approval"}},
			},
		}
		for _, b := range branches {
			ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: b})
			ps.Status.Environments = append(ps.Status.Environments, promoterv1alpha1.EnvironmentStatus{
				Branch:   b,
				Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc123"}},
			})
		}
		return ps
	}

	It("keeps the trigger state of the environments the failed reconcile did not reach", func() {
		ctx := GinkgoT().Context()
		exec := &countingExecutor{}
		emitter := &failingEmitter{}
		r := NewReconciler(exec, emitter)
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				Key:      "external-approval",
				ReportOn: constants.CommitRefProposed,
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Trigger: &promoterv1alpha1.TriggerModeSpec{
						RequeueDuration: metav1.Duration{Duration: time.Minute},
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `(TriggerOutput["trackedSha"] ?? "") != "abc123"`,
							Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: "abc123" }`},
						},
					},
				},
			},
		}
		ps := newPromotionStrategy()

		By("requesting once per environment")
		_, _, _, err := r.ReconcileWebRequestCommitStatusEnvironments(ctx, wrcs, ps, NamespaceMetadata{})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.requests).To(Equal(3))

		By("failing the staging CommitStatus write on a reconcile that sends no requests")
		emitter.failBranch = "environment/staging"
		_, _, _, err = r.ReconcileWebRequestCommitStatusEnvironments(ctx, wrcs, ps, NamespaceMetadata{})
		Expect(err).To(HaveOccurred())
		Expect(wrcs.Status.Environments).To(HaveLen(3), "production's status must survive the failed reconcile")

		By("not re-sending any request once the write succeeds")
		emitter.failBranch = ""
		_, _, _, err = r.ReconcileWebRequestCommitStatusEnvironments(ctx, wrcs, ps, NamespaceMetadata{})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.requests).To(Equal(3))
	})

	It("keeps the promotionstrategy context result of a request that was already sent", func() {
		ctx := GinkgoT().Context()
		exec := &countingExecutor{}
		emitter := &failingEmitter{failBranch: "environment/staging"}
		r := NewReconciler(exec, emitter)
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				Key:      "external-approval",
				ReportOn: constants.CommitRefProposed,
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{Expression: `Response != nil ? Response.StatusCode == 200 : Phase == "success"`},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: time.Minute}},
				},
			},
		}
		ps := newPromotionStrategy()

		By("sending the request and then failing the staging CommitStatus write")
		_, _, _, err := r.ReconcileWebRequestCommitStatusPromotionStrategy(ctx, wrcs, ps, NamespaceMetadata{})
		Expect(err).To(HaveOccurred())
		Expect(exec.requests).To(Equal(1))

		By("not re-sending the request once the write succeeds")
		emitter.failBranch = ""
		_, _, _, err = r.ReconcileWebRequestCommitStatusPromotionStrategy(ctx, wrcs, ps, NamespaceMetadata{})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.requests).To(Equal(1))
	})
})
