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

package simulate

import (
	"context"
	"errors"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
)

// simCommitRenderer implements webrequest.CommitStatusEmitter via local CommitStatus rendering.
type simCommitRenderer struct{}

func (simCommitRenderer) EmitCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, repositoryRefName, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, td webrequest.TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	return renderCommitStatus(ctx, wrcs, repositoryRefName, branch, sha, phase, td)
}

// Simulate runs one WebRequestCommitStatus reconcile against args, using
// args.HTTPResponses in place of any real HTTP call. The returned Result.Status
// matches what the controller would write to WebRequestCommitStatus.Status.
//
// Safe for concurrent use (shared expr compile cache in internal/webrequest).
func Simulate(ctx context.Context, args Args) (*simulatortypes.Result, error) {
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
) (*simulatortypes.Result, error) {
	var rendered []simulatortypes.RenderedRequest
	exec := newMockHTTPEXecutor(newResolveFromSliceByBranch(args.HTTPResponses), &rendered)

	out, err := webrequest.ProcessWebRequestCommitStatusEnvironments(ctx, webrequest.ProcessWebRequestCommitStatusInput{
		HttpExec:               exec,
		WebRequestCommitStatus: wrcs,
		PromotionStrategy:      ps,
		NamespaceMeta:          args.NamespaceMetadata,
		CommitEmitter:          simCommitRenderer{},
	})
	if err != nil {
		return nil, fmt.Errorf("simulate environments reconcile: %w", err)
	}

	return &simulatortypes.Result{
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
) (*simulatortypes.Result, error) {
	var rendered []simulatortypes.RenderedRequest
	exec := newMockHTTPEXecutor(newResolveFromSliceFirst(args.HTTPResponses), &rendered)

	out, err := webrequest.ProcessWebRequestCommitStatusPromotionStrategyContext(ctx, webrequest.ProcessWebRequestCommitStatusInput{
		HttpExec:               exec,
		WebRequestCommitStatus: wrcs,
		PromotionStrategy:      ps,
		NamespaceMeta:          args.NamespaceMetadata,
		CommitEmitter:          simCommitRenderer{},
	})
	if err != nil {
		return nil, fmt.Errorf("simulate promotionstrategy reconcile: %w", err)
	}

	if out.ApplicableEnvsEmpty {
		return &simulatortypes.Result{}, nil
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
		statusCopy := st.DeepCopy()
		if statusCopy == nil {
			return nil, errors.New("internal error: DeepCopy returned nil for carried status snapshot")
		}
		return &simulatortypes.Result{
			Status:           *statusCopy,
			RenderedRequests: rendered,
			CommitStatuses:   out.CommitStatuses,
		}, nil
	}

	return &simulatortypes.Result{
		Status: promoterv1alpha1.WebRequestCommitStatusStatus{
			PromotionStrategyContext: out.PromotionStrategyContext,
		},
		RenderedRequests: rendered,
		CommitStatuses:   out.CommitStatuses,
	}, nil
}

func simulatorHTTPResponseToWeb(m simulatortypes.HTTPResponse) webrequest.HTTPResponse {
	return webrequest.HTTPResponse{
		StatusCode: m.Response.StatusCode,
		Body:       m.Response.Body,
		Headers:    m.Response.Headers,
	}
}

// newResolveFromSliceByBranch returns a resolve func that picks the first mock
// whose Branch matches the reconcile branch (TemplateData.Branch).
func newResolveFromSliceByBranch(entries []simulatortypes.HTTPResponse) func(branch string) (*webrequest.HTTPResponse, error) {
	return func(branch string) (*webrequest.HTTPResponse, error) {
		for i := range entries {
			if entries[i].Branch == branch {
				w := simulatorHTTPResponseToWeb(entries[i])
				return &w, nil
			}
		}
		return nil, nil
	}
}

// newResolveFromSliceFirst returns a resolve func for promotionstrategy context:
// always uses entries[0]; ignores branch; empty slice yields nil.
func newResolveFromSliceFirst(entries []simulatortypes.HTTPResponse) func(branch string) (*webrequest.HTTPResponse, error) {
	return func(branch string) (*webrequest.HTTPResponse, error) {
		_ = branch
		if len(entries) == 0 {
			return nil, nil
		}
		w := simulatorHTTPResponseToWeb(entries[0])
		return &w, nil
	}
}
