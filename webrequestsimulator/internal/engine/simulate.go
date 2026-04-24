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
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// Simulate runs one WebRequestCommitStatus reconcile against args, using
// args.HTTPResponse in place of any real HTTP call. The returned Result.Status
// matches what the controller would write to WebRequestCommitStatus.Status.
//
// Safe for concurrent use: a fresh Evaluator is created per call.
func Simulate(ctx context.Context, args Args) (*Result, error) {
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

// simulateEnvironments mirrors the controller's processEnvironments but replaces
// the real HTTP call with args.HTTPResponse when the trigger fires.
func simulateEnvironments(
	ctx context.Context,
	args Args,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
) (*Result, error) {
	evaluator := webrequest.NewEvaluator()
	exec := &mockHTTPEXecutor{response: args.HTTPResponse}
	namespaceMeta := args.NamespaceMetadata

	wrcsSnapshot := wrcs.DeepCopy()
	if wrcsSnapshot == nil {
		return nil, fmt.Errorf("unexpected nil from DeepCopy for WebRequestCommitStatus %s/%s", wrcs.Namespace, wrcs.Name)
	}

	lastReconciledStatus := wrcsSnapshot.Status.DeepCopy()
	if lastReconciledStatus == nil {
		lastReconciledStatus = &promoterv1alpha1.WebRequestCommitStatusStatus{}
	}
	statusByEnv := make(map[string]*promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, len(lastReconciledStatus.Environments))
	for i := range lastReconciledStatus.Environments {
		statusByEnv[lastReconciledStatus.Environments[i].Branch] = &lastReconciledStatus.Environments[i]
	}

	psEnvStatusMap := webrequest.GetEnvsByBranch(ps)
	applicableEnvs := webrequest.GetApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)
	currentShas, err := webrequest.GetCurrentShasByBranch(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current SHAs: %w", err)
	}

	out := &Result{
		Status: promoterv1alpha1.WebRequestCommitStatusStatus{
			Environments: make([]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, 0, len(applicableEnvs)),
		},
	}

	for _, env := range applicableEnvs {
		branch := env.Branch
		reportedSha := currentShas[branch]

		lastReconciledEnvStatus := statusByEnv[branch]
		lastState := webrequest.LastReconciledStateFromEnvironment(ctx, lastReconciledEnvStatus)
		lastSuccessfulSha := ""
		if lastReconciledEnvStatus != nil {
			lastSuccessfulSha = lastReconciledEnvStatus.LastSuccessfulSha
		}

		td := webrequest.TemplateData{
			Branch:                 branch,
			Phase:                  lastState.Phase,
			PromotionStrategy:      ps,
			WebRequestCommitStatus: wrcsSnapshot,
			NamespaceMetadata:      namespaceMeta,
			TriggerOutput:          lastState.TriggerData,
			ResponseOutput:         lastState.ResponseData,
			SuccessOutput:          lastState.SuccessData,
		}

		// Polling+proposed optimization: skip entirely when this SHA already succeeded.
		if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == constants.CommitRefProposed {
			if lastReconciledEnvStatus != nil && lastState.Phase == string(promoterv1alpha1.CommitPhaseSuccess) && lastSuccessfulSha == reportedSha {
				out.Status.Environments = append(out.Status.Environments, *lastReconciledEnvStatus)
				cs, err := renderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, promoterv1alpha1.CommitPhaseSuccess, td)
				if err != nil {
					return nil, fmt.Errorf("failed to render CommitStatus for skipped environment %q: %w", branch, err)
				}
				out.CommitStatuses = append(out.CommitStatuses, cs)
				continue
			}
		}

		decision, err := webrequest.EvaluateTriggerDecision(ctx, evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
		if err != nil {
			return nil, fmt.Errorf("trigger decision for environment %q: %w", branch, err)
		}

		var renderedReq *RenderedRequest
		if decision.ShouldFire {
			req, err := renderHTTPRequest(wrcs, td)
			if err != nil {
				return nil, fmt.Errorf("failed to render HTTP request for environment %q: %w", branch, err)
			}
			renderedReq = &req
		}

		result, err := webrequest.FireOrCarryForward(ctx, evaluator, wrcs, td, decision, lastState, exec)
		if err != nil {
			return nil, fmt.Errorf("environment %q: %w", branch, err)
		}
		if result.Phase == promoterv1alpha1.CommitPhaseSuccess {
			lastSuccessfulSha = reportedSha
		}

		triggerDataJSON, err := webrequest.MarshalJSONMap(decision.NewTriggerData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal trigger data: %w", err)
		}

		out.Status.Environments = append(out.Status.Environments, promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
			Branch:                 branch,
			ReportedSha:            reportedSha,
			LastSuccessfulSha:      lastSuccessfulSha,
			Phase:                  result.Phase,
			LastRequestTime:        result.LastRequestTime,
			LastResponseStatusCode: result.LastResponseStatusCode,
			TriggerOutput:          triggerDataJSON,
			ResponseOutput:         result.ResponseDataJSON,
			SuccessOutput:          result.SuccessDataJSON,
		})

		if renderedReq != nil {
			out.RenderedRequests = append(out.RenderedRequests, *renderedReq)
		}

		commitTd := td.WithLatestOutputs(result.ResponseDataJSON, decision.NewTriggerData, result.SuccessDataJSON)
		commitTd.Phase = string(result.Phase)
		cs, err := renderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, result.Phase, commitTd)
		if err != nil {
			return nil, fmt.Errorf("failed to render CommitStatus for environment %q: %w", branch, err)
		}
		out.CommitStatuses = append(out.CommitStatuses, cs)
	}

	return out, nil
}

// simulatePromotionStrategy mirrors the controller's processContextPromotionStrategy
// but replaces the shared HTTP call with args.HTTPResponse when the trigger fires.
func simulatePromotionStrategy(
	ctx context.Context,
	args Args,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
) (*Result, error) {
	evaluator := webrequest.NewEvaluator()
	exec := &mockHTTPEXecutor{response: args.HTTPResponse}
	namespaceMeta := args.NamespaceMetadata

	applicableEnvs := webrequest.GetApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)
	if len(applicableEnvs) == 0 {
		return &Result{}, nil
	}

	wrcsSnapshot := wrcs.DeepCopy()
	if wrcsSnapshot == nil {
		return nil, fmt.Errorf("unexpected nil from DeepCopy for WebRequestCommitStatus %s/%s", wrcs.Namespace, wrcs.Name)
	}

	psEnvStatusMap := webrequest.GetEnvsByBranch(ps)
	currentShaPerBranch, err := webrequest.GetCurrentShasByBranch(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current SHAs (context=promotionstrategy): %w", err)
	}

	lastReconciledCtxStatus := wrcsSnapshot.Status.PromotionStrategyContext.DeepCopy()
	lastState := webrequest.LastReconciledStateFromContext(ctx, lastReconciledCtxStatus)

	// Polling+proposed optimization: skip when all environments already succeeded for their current SHAs.
	if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == constants.CommitRefProposed && lastReconciledCtxStatus != nil {
		if webrequest.AllBranchesSucceededForCurrentShas(applicableEnvs, lastReconciledCtxStatus, currentShaPerBranch) {
			baseTd := webrequest.TemplateData{
				Phase:                  string(promoterv1alpha1.CommitPhaseSuccess),
				PromotionStrategy:      ps,
				WebRequestCommitStatus: wrcsSnapshot,
				NamespaceMetadata:      namespaceMeta,
				TriggerOutput:          lastState.TriggerData,
				ResponseOutput:         lastState.ResponseData,
				SuccessOutput:          lastState.SuccessData,
			}
			out := &Result{Status: wrcsSnapshot.Status}
			out.Status.Environments = nil
			out.Status.PromotionStrategyContext = lastReconciledCtxStatus.DeepCopy()
			for _, env := range applicableEnvs {
				perEnvTd := baseTd
				perEnvTd.Branch = env.Branch
				cs, err := renderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, env.Branch, currentShaPerBranch[env.Branch], promoterv1alpha1.CommitPhaseSuccess, perEnvTd)
				if err != nil {
					return nil, fmt.Errorf("failed to render CommitStatus for skipped environment %q (context=promotionstrategy): %w", env.Branch, err)
				}
				out.CommitStatuses = append(out.CommitStatuses, cs)
			}
			return out, nil
		}
	}

	td := webrequest.TemplateData{
		Phase:                  lastState.Phase,
		PromotionStrategy:      ps,
		WebRequestCommitStatus: wrcsSnapshot,
		NamespaceMetadata:      namespaceMeta,
		TriggerOutput:          lastState.TriggerData,
		ResponseOutput:         lastState.ResponseData,
		SuccessOutput:          lastState.SuccessData,
	}

	decision, err := webrequest.EvaluateTriggerDecision(ctx, evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
	if err != nil {
		return nil, fmt.Errorf("trigger decision (context=promotionstrategy): %w", err)
	}

	var renderedReq *RenderedRequest
	if decision.ShouldFire {
		req, err := renderHTTPRequest(wrcs, td)
		if err != nil {
			return nil, fmt.Errorf("failed to render shared HTTP request (context=promotionstrategy): %w", err)
		}
		renderedReq = &req
	}

	result, err := webrequest.FireOrCarryForward(ctx, evaluator, wrcs, td, decision, lastState, exec)
	if err != nil {
		return nil, fmt.Errorf("context=promotionstrategy: %w", err)
	}

	triggerDataJSON, err := webrequest.MarshalJSONMap(decision.NewTriggerData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trigger data: %w", err)
	}

	lastSuccessfulShas := webrequest.LastSuccessfulShasForPromotionStrategyContext(
		applicableEnvs, lastReconciledCtxStatus, result.Phase, result.PhasePerBranch, currentShaPerBranch,
	)
	resolvedPhases := webrequest.GetPhasesByBranch(applicableEnvs, result.Phase, result.PhasePerBranch)

	out := &Result{
		Status: promoterv1alpha1.WebRequestCommitStatusStatus{
			PromotionStrategyContext: &promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{
				PhasePerBranch:         webrequest.PhasePerBranchSliceFromMap(resolvedPhases),
				LastRequestTime:        result.LastRequestTime,
				LastResponseStatusCode: result.LastResponseStatusCode,
				TriggerOutput:          triggerDataJSON,
				ResponseOutput:         result.ResponseDataJSON,
				SuccessOutput:          result.SuccessDataJSON,
				LastSuccessfulShas:     webrequest.LastSuccessfulShasSliceFromMap(lastSuccessfulShas),
			},
		},
	}
	if renderedReq != nil {
		out.RenderedRequests = append(out.RenderedRequests, *renderedReq)
	}

	commitTd := td.WithLatestOutputs(result.ResponseDataJSON, decision.NewTriggerData, result.SuccessDataJSON)
	for _, env := range applicableEnvs {
		branch := env.Branch
		envPhase := resolvedPhases[branch]
		perEnvTd := commitTd
		perEnvTd.Branch = branch
		perEnvTd.Phase = string(envPhase)
		cs, err := renderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, currentShaPerBranch[branch], envPhase, perEnvTd)
		if err != nil {
			return nil, fmt.Errorf("failed to render CommitStatus for environment %q (context=promotionstrategy): %w", branch, err)
		}
		out.CommitStatuses = append(out.CommitStatuses, cs)
	}

	return out, nil
}
