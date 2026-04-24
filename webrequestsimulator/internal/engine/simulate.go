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

package engine

import (
	"context"
	"errors"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/types"
)

// simCommitRenderer implements webrequest.CommitStatusEmitter via local CommitStatus rendering.
type simCommitRenderer struct{}

func (simCommitRenderer) EmitCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, repositoryRefName, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, td webrequest.TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	return renderCommitStatus(ctx, wrcs, repositoryRefName, branch, sha, phase, td)
}

// renderedRequestsCollector implements webrequest.RenderedHTTPSink by appending into the slice
// that becomes types.Result.RenderedRequests.
type renderedRequestsCollector struct {
	out *[]types.RenderedRequest
}

func (c *renderedRequestsCollector) RecordRenderedHTTP(r webrequest.RenderedHTTPRequest) {
	*c.out = append(*c.out, types.RenderedRequest{
		Branch: r.Branch, Method: r.Method, URL: r.URL, Headers: r.Headers, Body: r.Body,
	})
}

// Simulate runs one WebRequestCommitStatus reconcile against args, using
// args.HTTPResponse in place of any real HTTP call. The returned Result.Status
// matches what the controller would write to WebRequestCommitStatus.Status.
//
// Safe for concurrent use: a fresh Evaluator is created per call.
func Simulate(ctx context.Context, args Args) (*types.Result, error) {
	if args.WebRequestCommitStatus == nil {
		return nil, errors.New("WebRequestCommitStatus is required")
	}
	if args.PromotionStrategy == nil {
		return nil, errors.New("PromotionStrategy is required")
	}

	wrcs := args.WebRequestCommitStatus
	ps := args.PromotionStrategy

	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		return simulatePromotionStrategy(ctx, args, wrcs, ps)
	}
	return simulateEnvironments(ctx, args, wrcs, ps)
}

func simulateEnvironments(
	ctx context.Context,
	args Args,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
) (*types.Result, error) {
	evaluator := webrequest.NewEvaluator()
	exec := &mockHTTPEXecutor{response: args.HTTPResponse}

	var rendered []types.RenderedRequest
	renderedHTTP := &renderedRequestsCollector{out: &rendered}

	out, err := webrequest.ProcessWebRequestCommitStatusEnvironments(ctx, webrequest.ProcessWebRequestCommitStatusEnvironmentsInput{
		Evaluator:              evaluator,
		HttpExec:               exec,
		WebRequestCommitStatus: wrcs,
		PromotionStrategy:      ps,
		NamespaceMeta:          args.NamespaceMetadata,
		CommitEmitter:          simCommitRenderer{},
		RenderedHTTPSink:       renderedHTTP,
	})
	if err != nil {
		return nil, err
	}

	return &types.Result{
		Status: promoterv1alpha1.WebRequestCommitStatusStatus{
			Environments: out.Environments,
		},
		RenderedRequests: rendered,
		CommitStatuses:   out.CommitStatuses,
	}, nil
}

func simulatePromotionStrategy(
	ctx context.Context,
	args Args,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
) (*types.Result, error) {
	evaluator := webrequest.NewEvaluator()
	exec := &mockHTTPEXecutor{response: args.HTTPResponse}

	var rendered []types.RenderedRequest
	renderedHTTP := &renderedRequestsCollector{out: &rendered}

	out, err := webrequest.ProcessWebRequestCommitStatusPromotionStrategyContext(ctx, webrequest.ProcessWebRequestCommitStatusPromotionStrategyInput{
		Evaluator:              evaluator,
		HttpExec:               exec,
		WebRequestCommitStatus: wrcs,
		PromotionStrategy:      ps,
		NamespaceMeta:          args.NamespaceMetadata,
		CommitEmitter:          simCommitRenderer{},
		RenderedHTTPSink:       renderedHTTP,
	})
	if err != nil {
		return nil, err
	}

	if out.ApplicableEnvsEmpty {
		return &types.Result{}, nil
	}
	if out.PollingAllSuccessSkip {
		// Core does not write WRCS status on this path; mirror the pre-refactor simulator snapshot for round-tripping.
		st := wrcs.Status.DeepCopy()
		if st == nil {
			st = &promoterv1alpha1.WebRequestCommitStatusStatus{}
		}
		st.Environments = nil
		if wrcs.Status.PromotionStrategyContext != nil {
			st.PromotionStrategyContext = wrcs.Status.PromotionStrategyContext.DeepCopy()
		}
		cpy := st.DeepCopy()
		if cpy == nil {
			return nil, fmt.Errorf("internal error: DeepCopy returned nil for carried status snapshot")
		}
		return &types.Result{
			Status:           *cpy,
			RenderedRequests: rendered,
			CommitStatuses:   out.CommitStatuses,
		}, nil
	}

	return &types.Result{
		Status: promoterv1alpha1.WebRequestCommitStatusStatus{
			PromotionStrategyContext: out.PromotionStrategyContext,
		},
		RenderedRequests: rendered,
		CommitStatuses:   out.CommitStatuses,
	}, nil
}
