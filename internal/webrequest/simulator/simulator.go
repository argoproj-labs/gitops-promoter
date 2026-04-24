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

package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// Simulate runs one WebRequestCommitStatus reconcile against the supplied args,
// using args.HTTPResponse in place of any real HTTP call. The returned
// Result.Status exactly matches what the controller would write to
// WebRequestCommitStatus.Status, so the Status can be fed back into a follow-up
// Simulate() call for round-tripping.
//
// Safe for concurrent use: a fresh Evaluator is created per call.
func Simulate(ctx context.Context, args Args) (*Result, error) {
	if args.WebRequestCommitStatus == nil {
		return nil, errors.New("Args.WebRequestCommitStatus is required")
	}
	if args.PromotionStrategy == nil {
		return nil, errors.New("Args.PromotionStrategy is required")
	}

	wrcs := args.WebRequestCommitStatus
	ps := args.PromotionStrategy

	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		return simulatePromotionStrategy(ctx, args, wrcs, ps)
	}
	return simulateEnvironments(ctx, args, wrcs, ps)
}

// simulateValidationResult is the per-branch/per-reconcile outcome returned by
// simulateFireOrCarryForward. Mirrors the controller's httpValidationResult but
// does not wrap real HTTP state.
type simulateValidationResult struct {
	LastRequestTime        *metav1.Time
	LastResponseStatusCode *int
	ResponseDataJSON       *apiextensionsv1.JSON
	SuccessDataJSON        *apiextensionsv1.JSON
	PhasePerBranch         map[string]promoterv1alpha1.CommitStatusPhase
	Phase                  promoterv1alpha1.CommitStatusPhase
}

// simulateTriggerDecision is the simulator's copy of the controller's triggerDecision.
type simulateTriggerDecision struct {
	NewTriggerData map[string]any
	ShouldFire     bool
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
				cs, err := RenderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, promoterv1alpha1.CommitPhaseSuccess, td)
				if err != nil {
					return nil, fmt.Errorf("failed to render CommitStatus for skipped environment %q: %w", branch, err)
				}
				out.CommitStatuses = append(out.CommitStatuses, cs)
				continue
			}
		}

		decision, err := simulateEvaluateTriggerDecision(ctx, evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
		if err != nil {
			return nil, fmt.Errorf("trigger decision for environment %q: %w", branch, err)
		}

		var renderedReq *RenderedRequest
		if decision.ShouldFire {
			req, err := RenderHTTPRequest(wrcs, td)
			if err != nil {
				return nil, fmt.Errorf("failed to render HTTP request for environment %q: %w", branch, err)
			}
			renderedReq = &req
		}

		result, err := simulateFireOrCarryForward(ctx, evaluator, wrcs, td, decision, lastState, args.HTTPResponse)
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
		cs, err := RenderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, result.Phase, commitTd)
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
				cs, err := RenderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, env.Branch, currentShaPerBranch[env.Branch], promoterv1alpha1.CommitPhaseSuccess, perEnvTd)
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

	decision, err := simulateEvaluateTriggerDecision(ctx, evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
	if err != nil {
		return nil, fmt.Errorf("trigger decision (context=promotionstrategy): %w", err)
	}

	var renderedReq *RenderedRequest
	if decision.ShouldFire {
		req, err := RenderHTTPRequest(wrcs, td)
		if err != nil {
			return nil, fmt.Errorf("failed to render shared HTTP request (context=promotionstrategy): %w", err)
		}
		renderedReq = &req
	}

	result, err := simulateFireOrCarryForward(ctx, evaluator, wrcs, td, decision, lastState, args.HTTPResponse)
	if err != nil {
		return nil, fmt.Errorf("context=promotionstrategy: %w", err)
	}

	triggerDataJSON, err := webrequest.MarshalJSONMap(decision.NewTriggerData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trigger data: %w", err)
	}

	lastSuccessfulShas := buildLastSuccessfulShas(applicableEnvs, lastReconciledCtxStatus, result.Phase, result.PhasePerBranch, currentShaPerBranch)
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
		cs, err := RenderCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, currentShaPerBranch[branch], envPhase, perEnvTd)
		if err != nil {
			return nil, fmt.Errorf("failed to render CommitStatus for environment %q (context=promotionstrategy): %w", branch, err)
		}
		out.CommitStatuses = append(out.CommitStatuses, cs)
	}

	return out, nil
}

// simulateEvaluateTriggerDecision mirrors the controller's evaluateTriggerDecision.
func simulateEvaluateTriggerDecision(
	ctx context.Context,
	evaluator *webrequest.Evaluator,
	mode promoterv1alpha1.ModeSpec,
	td webrequest.TemplateData,
	lastRequestTime *metav1.Time,
) (simulateTriggerDecision, error) {
	shouldFire := true
	var newTriggerData map[string]any

	if mode.Polling != nil && lastRequestTime != nil {
		if elapsed := time.Since(lastRequestTime.Time); elapsed < mode.Polling.Interval.Duration {
			shouldFire = false
		}
	}

	if mode.Trigger != nil {
		sf, ntd, err := evaluator.EvaluateTriggerWhenBranch(ctx, mode.Trigger, td)
		if err != nil {
			return simulateTriggerDecision{}, fmt.Errorf("failed to evaluate trigger.when: %w", err)
		}
		shouldFire = sf
		newTriggerData = ntd
	}

	return simulateTriggerDecision{ShouldFire: shouldFire, NewTriggerData: newTriggerData}, nil
}

// simulateFireOrCarryForward mirrors the controller's fireOrCarryForward. When the
// trigger fires it uses mockResponse in place of a real HTTP call; when it does not
// fire it runs success.when with Response=nil so phase can be derived from the
// rest of the environment.
func simulateFireOrCarryForward(
	ctx context.Context,
	evaluator *webrequest.Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td webrequest.TemplateData,
	decision simulateTriggerDecision,
	lastState webrequest.LastReconciledState,
	mockResponse *webrequest.HTTPResponse,
) (simulateValidationResult, error) {
	if decision.ShouldFire {
		if mockResponse == nil {
			return simulateValidationResult{}, errors.New("Args.webrequest.HTTPResponse is required when the trigger fires (fill in a mock response, or craft inputs so the trigger does not fire)")
		}
		return simulateHandleRequestAndValidation(ctx, evaluator, wrcs, td, *mockResponse)
	}

	exprData := webrequest.SuccessWhenExprData(td, nil)
	exprData, err := evaluator.EnrichWhenExprEnv(ctx, wrcs.Spec.Success.When, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate success.when.variables: %w", err)
	}
	phase, phasePerBranch, err := simulateEvaluateSuccessPhase(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate success.when expression: %w", err)
	}
	successDataJSON, err := simulateEvaluateSuccessOutput(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate success.when.output expression: %w", err)
	}

	return simulateValidationResult{
		Phase:                  phase,
		PhasePerBranch:         phasePerBranch,
		LastRequestTime:        lastState.LastRequestTime,
		LastResponseStatusCode: lastState.LastResponseStatusCode,
		ResponseDataJSON:       lastState.ResponseOutput,
		SuccessDataJSON:        successDataJSON,
	}, nil
}

// simulateHandleRequestAndValidation mirrors handleHTTPRequestAndValidation but
// with mockResponse instead of a real HTTP roundtrip.
func simulateHandleRequestAndValidation(
	ctx context.Context,
	evaluator *webrequest.Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td webrequest.TemplateData,
	mockResponse webrequest.HTTPResponse,
) (simulateValidationResult, error) {
	now := metav1.Now()
	lastRequestTime := &now
	statusCode := mockResponse.StatusCode
	lastResponseStatusCode := &statusCode

	var responseDataJSON *apiextensionsv1.JSON
	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.Response != nil {
		extractedData, err := evaluator.EvaluateResponseDataExpression(ctx, wrcs.Spec.Mode.Trigger.Response.Output.Expression, mockResponse)
		if err != nil {
			return simulateValidationResult{}, fmt.Errorf("failed to evaluate response data expression: %w", err)
		}
		responseDataBytes, err := json.Marshal(extractedData)
		if err != nil {
			return simulateValidationResult{}, fmt.Errorf("failed to marshal response data: %w", err)
		}
		responseDataJSON = &apiextensionsv1.JSON{Raw: responseDataBytes}
	}

	if responseDataJSON != nil {
		if data, err := webrequest.UnmarshalJSONMap(responseDataJSON); err == nil && data != nil {
			td.ResponseOutput = data
		}
	}

	exprData := webrequest.SuccessWhenExprData(td, &mockResponse)
	exprData, err := evaluator.EnrichWhenExprEnv(ctx, wrcs.Spec.Success.When, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate success.when.variables: %w", err)
	}
	phase, phasePerBranch, err := simulateEvaluateSuccessPhase(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}
	successDataJSON, err := simulateEvaluateSuccessOutput(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return simulateValidationResult{}, fmt.Errorf("failed to evaluate success.when.output expression: %w", err)
	}

	return simulateValidationResult{
		Phase:                  phase,
		PhasePerBranch:         phasePerBranch,
		LastRequestTime:        lastRequestTime,
		LastResponseStatusCode: lastResponseStatusCode,
		ResponseDataJSON:       responseDataJSON,
		SuccessDataJSON:        successDataJSON,
	}, nil
}

// simulateEvaluateSuccessPhase mirrors evaluateSuccessPhase on the reconciler.
func simulateEvaluateSuccessPhase(
	ctx context.Context,
	evaluator *webrequest.Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	exprData map[string]any,
) (promoterv1alpha1.CommitStatusPhase, map[string]promoterv1alpha1.CommitStatusPhase, error) {
	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		phase, phasePerBranch, err := evaluator.EvaluateValidationExpressionForPromotionStrategy(ctx, wrcs.Spec.Success.When.Expression, exprData)
		if err != nil {
			return phase, phasePerBranch, fmt.Errorf("failed to evaluate validation expression (promotionstrategy context): %w", err)
		}
		return phase, phasePerBranch, nil
	}
	passed, err := evaluator.EvaluateValidationExpression(ctx, wrcs.Spec.Success.When.Expression, exprData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}
	if passed {
		return promoterv1alpha1.CommitPhaseSuccess, nil, nil
	}
	return promoterv1alpha1.CommitPhasePending, nil, nil
}

// simulateEvaluateSuccessOutput mirrors evaluateSuccessOutput on the reconciler.
func simulateEvaluateSuccessOutput(
	ctx context.Context,
	evaluator *webrequest.Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	exprData map[string]any,
) (*apiextensionsv1.JSON, error) {
	if wrcs.Spec.Success.When.Output == nil || wrcs.Spec.Success.When.Output.Expression == "" {
		return nil, nil
	}
	extractedData, err := evaluator.EvaluateSuccessDataExpression(ctx, wrcs.Spec.Success.When.Output.Expression, exprData)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate success data expression: %w", err)
	}
	out, err := webrequest.MarshalJSONMap(extractedData)
	if err != nil {
		return nil, fmt.Errorf("marshal success output data: %w", err)
	}
	return out, nil
}

// buildLastSuccessfulShas seeds the lastSuccessfulShas map from the previous
// reconcile and updates branches that succeeded this reconcile with the current SHA.
// This matches the subset of detectTransitionsAndUpdateShas that affects status
// (transition detection is used only for CTP touches in production, which the
// simulator has no business doing).
func buildLastSuccessfulShas(
	applicableEnvs []promoterv1alpha1.Environment,
	lastReconciledCtxStatus *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus,
	phase promoterv1alpha1.CommitStatusPhase,
	phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase,
	currentShaPerBranch map[string]string,
) map[string]string {
	lastSuccessfulShas := make(map[string]string, len(applicableEnvs))
	if lastReconciledCtxStatus != nil {
		for _, it := range lastReconciledCtxStatus.LastSuccessfulShas {
			lastSuccessfulShas[it.Branch] = it.LastSuccessfulSha
		}
	}
	for _, env := range applicableEnvs {
		branch := env.Branch
		if webrequest.ResolvePhaseForBranch(branch, phase, phasePerBranch) == promoterv1alpha1.CommitPhaseSuccess {
			lastSuccessfulShas[branch] = currentShaPerBranch[branch]
		}
	}
	return lastSuccessfulShas
}
