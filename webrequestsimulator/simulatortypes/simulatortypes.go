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

// Package simulatortypes holds the value types for webrequestsimulator.Simulate.
package simulatortypes

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// Input is the single argument to Simulate.
//
// WebRequestCommitStatus drives spec behavior AND previous-reconcile state.
// For environments context, Status.Environments[*] supplies previous
// TriggerOutput/ResponseOutput/SuccessOutput/Phase/LastRequestTime per branch.
// For promotionstrategy context, Status.PromotionStrategyContext supplies
// the same. Status is optional — when empty the simulator treats the run
// as the first reconcile.
//
// HTTPResponse is the stand-in response used whenever the trigger fires. It
// is required when trigger mode evaluates to true or polling mode is used;
// may be nil when the inputs are crafted so the trigger does not fire.
type Input struct {
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	NamespaceMetadata      NamespaceMetadata
	HTTPResponse           *HTTPResponse
}

// NamespaceMetadata holds the labels and annotations of the
// WebRequestCommitStatus's namespace, passed to URL/header/body/description
// templates as {{ .NamespaceMetadata.Labels }} and {{ .NamespaceMetadata.Annotations }}.
type NamespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// HTTPResponse is the stand-in HTTP response the simulator feeds to the
// success/response expressions in place of a real network call.
//
// Body may be pre-parsed (a map/slice/primitive) or a raw string. The controller
// JSON-decodes string bodies when it can, so mirror that shape here if you want
// the expression environment to match production exactly.
type HTTPResponse struct {
	Body       any
	Headers    map[string][]string
	StatusCode int
}

// Result mirrors what the controller produces for a single reconcile.
//
// Status matches exactly what the controller would write to
// WebRequestCommitStatus.Status — the canonical home for TriggerOutput,
// ResponseOutput, SuccessOutput, Phase, LastRequestTime, and (in
// promotionstrategy context) PhasePerBranch. Feed Status back as
// Input.WebRequestCommitStatus.Status on the next Simulate() call for round-tripping.
//
// Status.Environments is populated when Context=="environments"; Status.PromotionStrategyContext
// is populated when Context=="promotionstrategy". The distinction mirrors production exactly.
//
// RenderedRequests and CommitStatuses capture things the controller does NOT
// persist to WRCS.Status in production (the raw HTTP request sent, and the
// separate CommitStatus CRs the controller upserts). They are surfaced here
// so users can verify their templates render correctly.
//
// CommitStatuses contains the *v1alpha1.CommitStatus resources the controller
// would have upserted — one per applicable environment in both contexts.
// ObjectMeta (Name/Namespace/Labels) and Spec are fully populated; OwnerReferences
// and Status are left empty (the simulator has no live cluster to reference or observe).
type Result struct {
	Status           promoterv1alpha1.WebRequestCommitStatusStatus
	RenderedRequests []RenderedRequest
	CommitStatuses   []*promoterv1alpha1.CommitStatus
}

// RenderedRequest is an HTTP request the controller would have made this
// reconcile. Length of Result.RenderedRequests:
//   - Context=="environments": one entry per environment whose trigger fired (0..N)
//   - Context=="promotionstrategy": 0 or 1 (shared across all envs)
//
// Branch is the environment branch in environments context; empty in
// promotionstrategy context.
type RenderedRequest struct {
	Branch  string
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}
