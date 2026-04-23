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

// Package simulator implements a single-reconcile simulator for
// WebRequestCommitStatus. It wires together the pure helpers from
// internal/webrequest (Evaluator, TemplateData, env resolution, state
// extraction) and substitutes a supplied HTTPResponse for the real HTTP call.
//
// The public wrapper lives at ./webrequestsimulator/; this package is
// repo-internal.
package simulator

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// Args carries everything the simulator needs.
//
// WebRequestCommitStatus drives spec behavior AND previous-reconcile state:
// for environments context, Status.Environments[*] supplies previous per-branch
// TriggerOutput/ResponseOutput/SuccessOutput/Phase/LastRequestTime. For
// promotionstrategy context, Status.PromotionStrategyContext supplies the same.
// Status is optional — when empty, the simulator treats the run as the first reconcile.
//
// HTTPResponse is the stand-in response fed to success/response expressions whenever
// the trigger fires. Required when trigger mode evaluates to true or polling mode
// is used; may be nil when the expression path won't fire.
type Args struct {
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	NamespaceMetadata      webrequest.NamespaceMetadata
	HTTPResponse           *webrequest.HTTPResponse
}

// Result is what Simulate returns for a single reconcile.
//
// Status matches exactly what the controller would write to
// WebRequestCommitStatus.Status — the canonical home for TriggerOutput,
// ResponseOutput, SuccessOutput, Phase, LastRequestTime, and (in
// promotionstrategy context) PhasePerBranch. Feed Status back in as
// Args.WebRequestCommitStatus.Status on the next call for round-tripping.
//
// Status.Environments is populated for context=environments; Status.PromotionStrategyContext
// is populated for context=promotionstrategy. This mirrors production exactly.
//
// RenderedRequests and CommitStatuses capture the request(s) and CommitStatus CRs
// the controller would have produced — these are NOT persisted to WRCS.Status in
// production, so the simulator surfaces them here.
//
// CommitStatuses contains the *v1alpha1.CommitStatus resources the controller
// would have upserted. ObjectMeta (Name/Namespace/Labels) and Spec are fully
// populated; OwnerReferences and Status are left empty (no live cluster to
// reference / observe).
type Result struct {
	Status           promoterv1alpha1.WebRequestCommitStatusStatus
	RenderedRequests []RenderedRequest
	CommitStatuses   []*promoterv1alpha1.CommitStatus
}

// RenderedRequest is an HTTP request the controller would have made this reconcile.
// Length of Result.RenderedRequests:
//   - context=environments: one entry per environment whose trigger fired (0..N)
//   - context=promotionstrategy: 0 or 1 (shared across all envs)
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
