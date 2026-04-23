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

package controller

import promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"

// SimulationNamespaceMetadata holds the namespace labels and annotations that the simulator exposes
// to templates and expressions via templateData.NamespaceMetadata.
type SimulationNamespaceMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// SimulationMockResponse is a user-supplied mock HTTP response the simulator injects when a step
// performs a synthetic HTTP call. Body is any (plain string or JSON-decoded map/slice/scalar).
// Headers follow http.Header shape.
type SimulationMockResponse struct {
	Body       any                 `json:"body,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StatusCode int                 `json:"statusCode"`
}

// RenderedHTTPRequest is the outcome of rendering spec.httpRequest templates for a single step.
// Body is a pointer so callers can distinguish "no body template configured" (nil) from
// "body template rendered to empty string" (pointer to "").
type RenderedHTTPRequest struct {
	Body    *string           `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// RenderedCommitStatus is the outcome of rendering spec.descriptionTemplate and spec.urlTemplate for a
// single applicable environment in a single simulation step.
type RenderedCommitStatus struct {
	Branch      string `json:"branch"`
	Sha         string `json:"sha,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// TriggerEvalResult captures what trigger.when.expression returned (or the error from evaluating it).
// In trigger mode it gates mock injection together with Evaluated and Error; in polling mode the
// trigger block is unused and injection does not consult this field.
type TriggerEvalResult struct {
	Error      string `json:"error,omitempty"`
	Evaluated  bool   `json:"evaluated"`
	ShouldFire bool   `json:"shouldFire"`
}

// WebRequestStepEvaluation is the per-environment (or per-shared-request, for promotionstrategy context)
// outcome of running one simulation step.
type WebRequestStepEvaluation struct {
	RenderedRequest  *RenderedHTTPRequest    `json:"renderedRequest,omitempty"`
	MockResponse     *SimulationMockResponse `json:"mockResponse,omitempty"`
	TriggerOutput    map[string]any          `json:"triggerOutput,omitempty"`
	ResponseOutput   map[string]any          `json:"responseOutput,omitempty"`
	SuccessOutput    map[string]any          `json:"successOutput,omitempty"`
	PhasePerBranch   map[string]string       `json:"phasePerBranch,omitempty"`
	Branch           string                  `json:"branch,omitempty"`
	Phase            string                  `json:"phase,omitempty"`
	Errors           []string                `json:"errors,omitempty"`
	TriggerEval      TriggerEvalResult       `json:"triggerEval"`
	ResponseInjected bool                    `json:"responseInjected"`
}

// WebRequestStepResult is the outcome of one simulation step (two by default, or three when
// after-state-change is enabled). For environments context it
// contains one Evaluation per applicable environment and one RenderedCommitStatus per environment.
// For promotionstrategy context it contains a single Evaluation (the shared HTTP/trigger/success run)
// and one RenderedCommitStatus per applicable environment (with phases resolved via PhasePerBranch).
type WebRequestStepResult struct {
	Label          string                     `json:"label"`
	Context        string                     `json:"context"`
	Evaluations    []WebRequestStepEvaluation `json:"evaluations,omitempty"`
	CommitStatuses []RenderedCommitStatus     `json:"commitStatuses,omitempty"`
	Errors         []string                   `json:"errors,omitempty"`
}

// SimulateWebRequestOptions configures the optional third reconcile ("after-state-change") and an
// alternate mock for that step. Set PromotionStrategyUpdated and/or WebRequestCommitStatusUpdated to
// append that step (swap those resources in for the third step only; prior outputs still carry forward).
// If both are nil, the simulator runs two steps and MockResponseUpdated must be nil.
type SimulateWebRequestOptions struct {
	// PromotionStrategyUpdated swaps the PromotionStrategy used in after-state-change. Typical use: simulate a
	// new Proposed.Note.DrySha arriving between reconciles to exercise fingerprint-based
	// invalidation in success.when carry-forward.
	PromotionStrategyUpdated *promoterv1alpha1.PromotionStrategy
	// WebRequestCommitStatusUpdated swaps the WebRequestCommitStatus used in after-state-change. Typical use:
	// model the controller having written back updated status between reconciles (new
	// Status.Environments[*].TriggerOutput / ResponseOutput / LastRequestTime / conditions) so
	// templates and expressions that reference .WebRequestCommitStatus.Status.* see the new
	// values. Less commonly, it also models an admin editing the spec between reconciles.
	WebRequestCommitStatusUpdated *promoterv1alpha1.WebRequestCommitStatus
	// MockResponseUpdated, when non-nil, is the mock HTTP response for the after-state-change step
	// when that step injects (requires PromotionStrategyUpdated and/or WebRequestCommitStatusUpdated).
	// When nil, the primary mock passed to SimulateWebRequestTemplates is used for every step.
	MockResponseUpdated *SimulationMockResponse
}
