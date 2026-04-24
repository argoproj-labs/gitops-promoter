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
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ProcessWebRequestCommitStatusEnvironmentsInput carries dependencies for the per-environment context path.
type ProcessWebRequestCommitStatusEnvironmentsInput struct {
	Evaluator              *Evaluator
	HttpExec               HTTPEXecutor
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	// NamespaceMeta is passed into TemplateData for template rendering.
	NamespaceMeta    NamespaceMetadata
	CommitEmitter    CommitStatusEmitter
	RenderedHTTPSink RenderedHTTPSink // optional; when non-nil, CollectRenderedHTTP is called after a successful template render when the trigger fires
}

// ProcessWebRequestCommitStatusEnvironmentsOutput is the computed status and CommitStatus list for one reconcile.
type ProcessWebRequestCommitStatusEnvironmentsOutput struct {
	Environments         []promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
	CommitStatuses       []*promoterv1alpha1.CommitStatus
	TransitionedBranches []string
}

// ProcessWebRequestCommitStatusPromotionStrategyInput carries dependencies for context=promotionstrategy.
type ProcessWebRequestCommitStatusPromotionStrategyInput struct {
	Evaluator              *Evaluator
	HttpExec               HTTPEXecutor
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	NamespaceMeta          NamespaceMetadata
	CommitEmitter          CommitStatusEmitter
	RenderedHTTPSink       RenderedHTTPSink // optional; same semantics as environments path
}

// ProcessWebRequestCommitStatusPromotionStrategyOutput is the computed status for promotionstrategy context.
type ProcessWebRequestCommitStatusPromotionStrategyOutput struct {
	PromotionStrategyContext *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus
	CommitStatuses           []*promoterv1alpha1.CommitStatus
	TransitionedBranches     []string
	ApplicableEnvsEmpty      bool
	PollingAllSuccessSkip    bool
}
