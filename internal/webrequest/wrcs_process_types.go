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

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ProcessWebRequestCommitStatusInput carries shared dependencies for WRCS processing
// (per-environment context and promotionstrategy context).
type ProcessWebRequestCommitStatusInput struct {
	Evaluator              *Evaluator
	HttpExec               HTTPEXecutor
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	// NamespaceMeta is passed into TemplateData for template rendering.
	NamespaceMeta         NamespaceMetadata
	CommitEmitter         CommitStatusEmitter
	RenderedHTTPCollector RenderedHTTPCollector // optional; when non-nil, CollectRenderedHTTP is called after a successful template render when the trigger fires
}

// ProcessWebRequestCommitStatusEnvironmentsOutput is the computed status and CommitStatus list for one reconcile.
type ProcessWebRequestCommitStatusEnvironmentsOutput struct {
	Environments         []promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
	CommitStatuses       []*promoterv1alpha1.CommitStatus
	TransitionedBranches []string
}

// ProcessWebRequestCommitStatusPromotionStrategyOutput is the computed status for promotionstrategy context.
type ProcessWebRequestCommitStatusPromotionStrategyOutput struct {
	PromotionStrategyContext *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus
	CommitStatuses           []*promoterv1alpha1.CommitStatus
	TransitionedBranches     []string
	ApplicableEnvsEmpty      bool
	PollingAllSuccessSkip    bool
}

// RenderedHTTPRequest is a fully rendered HTTP request template snapshot (diagnostics).
// Branch is set per environment in environments context; empty for promotionstrategy shared request.
type RenderedHTTPRequest struct {
	Branch  string
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// CommitStatusEmitter creates or updates one CommitStatus per reconcile step (SSA upsert in the controller,
// local render in the simulator).
type CommitStatusEmitter interface {
	EmitCommitStatus(
		ctx context.Context,
		wrcs *promoterv1alpha1.WebRequestCommitStatus,
		repositoryRefName, branch, sha string,
		phase promoterv1alpha1.CommitStatusPhase,
		td TemplateData,
	) (*promoterv1alpha1.CommitStatus, error)
}

// RenderedHTTPCollector receives fully rendered HTTP templates when the trigger fires (e.g. simulator output).
// Leave nil in production: HTTPEXecutor already renders on the wire; collecting here would duplicate template work.
type RenderedHTTPCollector interface {
	CollectRenderedHTTP(r RenderedHTTPRequest)
}
