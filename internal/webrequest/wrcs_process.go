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
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

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

// RenderedHTTPSink receives fully rendered HTTP templates when the trigger fires (e.g. simulator output).
// Leave nil in production: HTTPEXecutor already renders on the wire; accepting here would duplicate template work.
type RenderedHTTPSink interface {
	Accept(r RenderedHTTPRequest)
}

// ProcessWebRequestCommitStatusEnvironmentsInput carries dependencies for the per-environment context path.
type ProcessWebRequestCommitStatusEnvironmentsInput struct {
	Evaluator              *Evaluator
	HttpExec               HTTPEXecutor
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	// NamespaceMeta is passed into TemplateData for template rendering.
	NamespaceMeta    NamespaceMetadata
	CommitEmitter    CommitStatusEmitter
	RenderedHTTPSink RenderedHTTPSink // optional; when non-nil, Accept is called after a successful template render when the trigger fires
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
	// ApplicableEnvsEmpty means there are no environments referencing this key; callers clear Environments and PSC.
	ApplicableEnvsEmpty bool

	// PollingAllSuccessSkip is the polling+proposed optimization: no HTTP, no PSC mutation on the live object.
	// CommitStatuses is populated. The controller leaves wrcs.Status unchanged on this path.
	PollingAllSuccessSkip bool

	PromotionStrategyContext *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus
	CommitStatuses           []*promoterv1alpha1.CommitStatus
	TransitionedBranches     []string
}

// renderHTTPRequestTemplates renders URL, headers, and body from WebRequestCommitStatus HTTP templates.
func renderHTTPRequestTemplates(wrcs *promoterv1alpha1.WebRequestCommitStatus, td TemplateData) (RenderedHTTPRequest, error) {
	req := RenderedHTTPRequest{
		Branch: td.Branch,
		Method: wrcs.Spec.HTTPRequest.Method,
	}

	url, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.URLTemplate, td)
	if err != nil {
		return RenderedHTTPRequest{}, fmt.Errorf("failed to render URL template: %w", err)
	}
	req.URL = url

	if wrcs.Spec.HTTPRequest.BodyTemplate != "" {
		body, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.BodyTemplate, td)
		if err != nil {
			return RenderedHTTPRequest{}, fmt.Errorf("failed to render body template: %w", err)
		}
		req.Body = body
	}

	if len(wrcs.Spec.HTTPRequest.HeaderTemplates) > 0 {
		req.Headers = make(map[string]string, len(wrcs.Spec.HTTPRequest.HeaderTemplates))
		for name, headerTemplate := range wrcs.Spec.HTTPRequest.HeaderTemplates {
			value, err := utils.RenderStringTemplate(headerTemplate, td)
			if err != nil {
				return RenderedHTTPRequest{}, fmt.Errorf("failed to render header template %q: %w", name, err)
			}
			req.Headers[name] = value
		}
	}

	return req, nil
}

// detectPromotionStrategyTransitionsAndLastSuccessfulShas builds lastSuccessfulShas and the list of
// branches that transitioned to success this reconcile (promotionstrategy context).
func detectPromotionStrategyTransitionsAndLastSuccessfulShas(
	applicableEnvs []promoterv1alpha1.Environment,
	lastReconciledCtxStatus *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus,
	phase promoterv1alpha1.CommitStatusPhase,
	phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase,
	lastReconciledPhase string,
	lastReconciledPhasePerBranch map[string]promoterv1alpha1.CommitStatusPhase,
	currentShaPerBranch map[string]string,
) ([]string, map[string]string) {
	lastSuccessfulShas := lastSuccessfulShasForPromotionStrategyContext(
		applicableEnvs, lastReconciledCtxStatus, phase, phasePerBranch, currentShaPerBranch,
	)
	var transitioned []string
	for _, env := range applicableEnvs {
		branch := env.Branch
		envPhase := resolvePhaseForBranch(branch, phase, phasePerBranch)
		lastReconciledEnvPhase := resolvePhaseForBranch(branch, promoterv1alpha1.CommitStatusPhase(lastReconciledPhase), lastReconciledPhasePerBranch)
		if lastReconciledEnvPhase != promoterv1alpha1.CommitPhaseSuccess && envPhase == promoterv1alpha1.CommitPhaseSuccess {
			transitioned = append(transitioned, branch)
		}
	}
	return transitioned, lastSuccessfulShas
}

// ProcessWebRequestCommitStatusEnvironments runs the per-environment WebRequestCommitStatus reconcile logic.
// It does not mutate wrcs.Status; callers apply Environments and handle PromotionStrategyContext clearing.
func ProcessWebRequestCommitStatusEnvironments(ctx context.Context, in ProcessWebRequestCommitStatusEnvironmentsInput) (*ProcessWebRequestCommitStatusEnvironmentsOutput, error) {
	logger := log.FromContext(ctx)
	wrcs := in.WebRequestCommitStatus
	ps := in.PromotionStrategy

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

	psEnvStatusMap := getEnvsByBranch(ps)
	applicableEnvs := getApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)
	currentShas, err := getCurrentShasByBranch(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current SHAs: %w", err)
	}

	out := &ProcessWebRequestCommitStatusEnvironmentsOutput{
		Environments:   make([]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, 0, len(applicableEnvs)),
		CommitStatuses: make([]*promoterv1alpha1.CommitStatus, 0, len(applicableEnvs)),
	}

	for _, env := range applicableEnvs {
		branch := env.Branch
		reportedSha := currentShas[branch]

		lastReconciledEnvStatus := statusByEnv[branch]
		lastState := LastReconciledStateFromEnvironment(ctx, lastReconciledEnvStatus)
		lastSuccessfulSha := ""
		if lastReconciledEnvStatus != nil {
			lastSuccessfulSha = lastReconciledEnvStatus.LastSuccessfulSha
		}

		td := TemplateData{
			Branch:                 branch,
			Phase:                  lastState.Phase,
			PromotionStrategy:      ps,
			WebRequestCommitStatus: wrcsSnapshot,
			NamespaceMetadata:      in.NamespaceMeta,
			TriggerOutput:          lastState.TriggerData,
			ResponseOutput:         lastState.ResponseData,
			SuccessOutput:          lastState.SuccessData,
		}

		if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == constants.CommitRefProposed {
			if lastReconciledEnvStatus != nil && lastState.Phase == string(promoterv1alpha1.CommitPhaseSuccess) && lastSuccessfulSha == reportedSha {
				logger.V(4).Info("Skipping already successful SHA in polling mode", "branch", branch, "sha", reportedSha)
				out.Environments = append(out.Environments, *lastReconciledEnvStatus)
				cs, err := in.CommitEmitter.EmitCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, promoterv1alpha1.CommitPhaseSuccess, td)
				if err != nil {
					return nil, fmt.Errorf("failed to upsert CommitStatus for skipped environment %q: %w", branch, err)
				}
				out.CommitStatuses = append(out.CommitStatuses, cs)
				continue
			}
		}

		decision, err := EvaluateTriggerDecision(ctx, in.Evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
		if err != nil {
			return nil, fmt.Errorf("trigger decision for environment %q: %w", branch, err)
		}

		if decision.ShouldFire && in.RenderedHTTPSink != nil {
			req, err := renderHTTPRequestTemplates(wrcs, td)
			if err != nil {
				return nil, fmt.Errorf("failed to render HTTP request for environment %q: %w", branch, err)
			}
			in.RenderedHTTPSink.Accept(req)
		}

		result, err := FireOrCarryForward(ctx, in.Evaluator, wrcs, td, decision, lastState, in.HttpExec)
		if err != nil {
			return nil, err
		}
		if result.Phase == promoterv1alpha1.CommitPhaseSuccess {
			lastSuccessfulSha = reportedSha
		}

		if lastState.Phase != string(promoterv1alpha1.CommitPhaseSuccess) && result.Phase == promoterv1alpha1.CommitPhaseSuccess {
			out.TransitionedBranches = append(out.TransitionedBranches, branch)
			logger.Info("Validation transitioned to success", "branch", branch, "sha", reportedSha)
		}

		triggerDataJSON, err := MarshalJSONMap(decision.NewTriggerData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal trigger data: %w", err)
		}

		out.Environments = append(out.Environments, promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
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

		commitTd := td.WithLatestOutputs(result.ResponseDataJSON, decision.NewTriggerData, result.SuccessDataJSON)
		commitTd.Phase = string(result.Phase)
		cs, err := in.CommitEmitter.EmitCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, result.Phase, commitTd)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		out.CommitStatuses = append(out.CommitStatuses, cs)

		logger.Info("Processed environment", "branch", branch, "reportedSha", reportedSha, "phase", result.Phase, "triggered", decision.ShouldFire)
	}

	return out, nil
}

// ProcessWebRequestCommitStatusPromotionStrategyContext runs context=promotionstrategy reconcile logic.
// It does not mutate wrcs.Status. Callers interpret ApplicableEnvsEmpty, PollingAllSuccessSkip, and PSC;
// on PollingAllSuccessSkip, wrcs.Status is unchanged from the caller's input (same as production).
func ProcessWebRequestCommitStatusPromotionStrategyContext(ctx context.Context, in ProcessWebRequestCommitStatusPromotionStrategyInput) (*ProcessWebRequestCommitStatusPromotionStrategyOutput, error) {
	logger := log.FromContext(ctx)
	wrcs := in.WebRequestCommitStatus
	ps := in.PromotionStrategy

	applicableEnvs := getApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)
	if len(applicableEnvs) == 0 {
		return &ProcessWebRequestCommitStatusPromotionStrategyOutput{ApplicableEnvsEmpty: true}, nil
	}

	wrcsSnapshot := wrcs.DeepCopy()
	if wrcsSnapshot == nil {
		return nil, fmt.Errorf("unexpected nil from DeepCopy for WebRequestCommitStatus %s/%s", wrcs.Namespace, wrcs.Name)
	}

	psEnvStatusMap := getEnvsByBranch(ps)
	currentShaPerBranch, err := getCurrentShasByBranch(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current SHAs (context=promotionstrategy): %w", err)
	}

	lastReconciledCtxStatus := wrcsSnapshot.Status.PromotionStrategyContext.DeepCopy()
	lastState := LastReconciledStateFromContext(ctx, lastReconciledCtxStatus)

	if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == constants.CommitRefProposed && lastReconciledCtxStatus != nil {
		if allBranchesSucceededForCurrentShas(applicableEnvs, lastReconciledCtxStatus, currentShaPerBranch) {
			logger.V(4).Info("All environments already successful for current SHAs (context=promotionstrategy), skipping HTTP request")
			baseTd := TemplateData{
				Phase:                  string(promoterv1alpha1.CommitPhaseSuccess),
				PromotionStrategy:      ps,
				WebRequestCommitStatus: wrcsSnapshot,
				NamespaceMetadata:      in.NamespaceMeta,
				TriggerOutput:          lastState.TriggerData,
				ResponseOutput:         lastState.ResponseData,
				SuccessOutput:          lastState.SuccessData,
			}
			commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(applicableEnvs))
			for _, env := range applicableEnvs {
				perEnvTd := baseTd
				perEnvTd.Branch = env.Branch
				cs, err := in.CommitEmitter.EmitCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, env.Branch, currentShaPerBranch[env.Branch], promoterv1alpha1.CommitPhaseSuccess, perEnvTd)
				if err != nil {
					return nil, fmt.Errorf("failed to upsert CommitStatus for skipped environment %q (context=promotionstrategy): %w", env.Branch, err)
				}
				commitStatuses = append(commitStatuses, cs)
			}
			return &ProcessWebRequestCommitStatusPromotionStrategyOutput{
				PollingAllSuccessSkip: true,
				CommitStatuses:        commitStatuses,
			}, nil
		}
	}

	td := TemplateData{
		Phase:                  lastState.Phase,
		PromotionStrategy:      ps,
		WebRequestCommitStatus: wrcsSnapshot,
		NamespaceMetadata:      in.NamespaceMeta,
		TriggerOutput:          lastState.TriggerData,
		ResponseOutput:         lastState.ResponseData,
		SuccessOutput:          lastState.SuccessData,
	}

	decision, err := EvaluateTriggerDecision(ctx, in.Evaluator, wrcs.Spec.Mode, td, lastState.LastRequestTime)
	if err != nil {
		return nil, fmt.Errorf("trigger decision (context=promotionstrategy): %w", err)
	}

	if decision.ShouldFire && in.RenderedHTTPSink != nil {
		req, err := renderHTTPRequestTemplates(wrcs, td)
		if err != nil {
			return nil, fmt.Errorf("failed to render shared HTTP request (context=promotionstrategy): %w", err)
		}
		in.RenderedHTTPSink.Accept(req)
	}

	result, err := FireOrCarryForward(ctx, in.Evaluator, wrcs, td, decision, lastState, in.HttpExec)
	if err != nil {
		return nil, err
	}

	triggerDataJSON, err := MarshalJSONMap(decision.NewTriggerData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trigger data: %w", err)
	}

	transitionedEnvironments, lastSuccessfulShas := detectPromotionStrategyTransitionsAndLastSuccessfulShas(
		applicableEnvs, lastReconciledCtxStatus, result.Phase, result.PhasePerBranch, lastState.Phase, lastState.PhasePerBranch, currentShaPerBranch,
	)
	if len(transitionedEnvironments) > 0 {
		logger.Info("Validation transitioned to success (context=promotionstrategy)", "branches", transitionedEnvironments)
	}

	resolvedPhases := getPhasesByBranch(applicableEnvs, result.Phase, result.PhasePerBranch)

	psc := &promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{
		PhasePerBranch:         phasePerBranchSliceFromMap(resolvedPhases),
		LastRequestTime:        result.LastRequestTime,
		LastResponseStatusCode: result.LastResponseStatusCode,
		TriggerOutput:          triggerDataJSON,
		ResponseOutput:         result.ResponseDataJSON,
		SuccessOutput:          result.SuccessDataJSON,
		LastSuccessfulShas:     lastSuccessfulShasSliceFromMap(lastSuccessfulShas),
	}

	commitTd := td.WithLatestOutputs(result.ResponseDataJSON, decision.NewTriggerData, result.SuccessDataJSON)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(applicableEnvs))
	for _, env := range applicableEnvs {
		branch := env.Branch
		envPhase := resolvedPhases[branch]
		perEnvTd := commitTd
		perEnvTd.Branch = branch
		perEnvTd.Phase = string(envPhase)
		cs, err := in.CommitEmitter.EmitCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, currentShaPerBranch[branch], envPhase, perEnvTd)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q (context=promotionstrategy): %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)
	}

	return &ProcessWebRequestCommitStatusPromotionStrategyOutput{
		PromotionStrategyContext: psc,
		CommitStatuses:           commitStatuses,
		TransitionedBranches:     transitionedEnvironments,
	}, nil
}
