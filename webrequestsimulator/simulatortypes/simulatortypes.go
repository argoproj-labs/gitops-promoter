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
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
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
// HTTPResponses supplies stand-in HTTP responses when the trigger fires (or
// under polling when a request runs). Each element uses the same Branch + Resp
// layout as the simulator engine (see HTTPResponse). Context=environments: Branch
// must match the PromotionStrategy environment branch for that mock; the first
// matching entry wins when Branch values duplicate. Context=promotionstrategy:
// only HTTPResponses[0] is used; Branch on that entry is ignored; extra slice
// elements are ignored. An empty slice is valid when no HTTP request runs this
// reconcile; otherwise a matching mock is required or Simulate returns an error.
type Input struct {
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	NamespaceMetadata      NamespaceMetadata
	HTTPResponses          []HTTPResponse
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
// Branch is simulator metadata only: for Context=environments it selects which
// environment branch this mock applies to (must match PromotionStrategy
// environment branch strings). It is not part of Resp. For
// Context=promotionstrategy only HTTPResponses[0] is consulted; Branch there
// is ignored.
//
// Resp uses the same type as production (webrequest.HTTPResponse): StatusCode,
// Body (pre-parsed map/slice/primitive or raw string — the controller JSON-decodes
// string bodies when it can), and Headers.
type HTTPResponse struct {
	Branch string
	Resp   webrequest.HTTPResponse
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
