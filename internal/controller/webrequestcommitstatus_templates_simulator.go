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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// SimulationNamespaceMetadata holds the namespace labels and annotations that the simulator exposes
// to templates and expressions via templateData.NamespaceMetadata.
type SimulationNamespaceMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// SimulationMockResponse is a user-supplied mock HTTP response fed into the "with-response" step of the
// simulator. Body is any (plain string or JSON-decoded map/slice/scalar). Headers follow http.Header shape.
type SimulationMockResponse struct {
	Body       any                 `json:"body,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StatusCode int                 `json:"statusCode"`
}

// RenderedHTTPRequest is the outcome of rendering spec.httpRequest templates for a single step.
// Body is a pointer so callers can distinguish "no body template configured" (nil) from
// "body template rendered to empty string" (pointer to "").
type RenderedHTTPRequest struct {
	Body    *string           `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// RenderedCommitStatus is the outcome of rendering spec.descriptionTemplate and spec.urlTemplate for a
// single applicable environment in a single simulation step.
type RenderedCommitStatus struct {
	Branch      string `json:"branch"`
	Sha         string `json:"sha,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// TriggerEvalResult captures what trigger.when.expression returned (or the error from evaluating it).
// The simulator records this as information only — it does not gate whether Response is injected in step 2.
type TriggerEvalResult struct {
	Error      string `json:"error,omitempty"`
	Evaluated  bool   `json:"evaluated"`
	ShouldFire bool   `json:"shouldFire"`
}

// WebRequestStepEvaluation is the per-environment (or per-shared-request, for promotionstrategy context)
// outcome of running one simulation step.
type WebRequestStepEvaluation struct {
	RenderedRequest  *RenderedHTTPRequest    `json:"renderedRequest,omitempty"`
	MockResponse     *SimulationMockResponse `json:"mockResponse,omitempty"`
	TriggerOutput    map[string]any          `json:"triggerOutput,omitempty"`
	ResponseOutput   map[string]any          `json:"responseOutput,omitempty"`
	SuccessOutput    map[string]any          `json:"successOutput,omitempty"`
	PhasePerBranch   map[string]string       `json:"phasePerBranch,omitempty"`
	Branch           string                  `json:"branch,omitempty"`
	Phase            string                  `json:"phase,omitempty"`
	Errors           []string                `json:"errors,omitempty"`
	TriggerEval      TriggerEvalResult       `json:"triggerEval"`
	ResponseInjected bool                    `json:"responseInjected"`
}

// WebRequestStepResult is the outcome of one of the three simulation steps. For environments context it
// contains one Evaluation per applicable environment and one RenderedCommitStatus per environment.
// For promotionstrategy context it contains a single Evaluation (the shared HTTP/trigger/success run)
// and one RenderedCommitStatus per applicable environment (with phases resolved via PhasePerBranch).
type WebRequestStepResult struct {
	Label          string                     `json:"label"`
	Context        string                     `json:"context"`
	Evaluations    []WebRequestStepEvaluation `json:"evaluations,omitempty"`
	CommitStatuses []RenderedCommitStatus     `json:"commitStatuses,omitempty"`
	Errors         []string                   `json:"errors,omitempty"`
}

// SimulateWebRequestOptions holds the optional knobs that enable the 4th "after-state-change" step
// of SimulateWebRequestTemplates. Either or both may be non-nil: if set, step 4 is appended and the
// updated values are used in place of ps / wrcs for that step only (step 3's derived outputs still
// carry forward). If both are nil the simulator returns the default 3 steps.
type SimulateWebRequestOptions struct {
	// PromotionStrategyUpdated swaps the PromotionStrategy used in step 4. Typical use: simulate a
	// new Proposed.Note.DrySha arriving between reconciles to exercise fingerprint-based
	// invalidation in success.when carry-forward.
	PromotionStrategyUpdated *promoterv1alpha1.PromotionStrategy
	// WebRequestCommitStatusUpdated swaps the WebRequestCommitStatus used in step 4. Typical use:
	// model the controller having written back updated status between reconciles (new
	// Status.Environments[*].TriggerOutput / ResponseOutput / LastRequestTime / conditions) so
	// templates and expressions that reference .WebRequestCommitStatus.Status.* see the new
	// values. Less commonly, it also models an admin editing the spec between reconciles.
	WebRequestCommitStatusUpdated *promoterv1alpha1.WebRequestCommitStatus
}

// SimulateWebRequestTemplates runs the 3-step templates simulation (before-response → with-response
// → after-response) for a WebRequestCommitStatus against the given PromotionStrategy and namespace
// metadata, using mock as the injected HTTP response in the "with-response" step. The trigger expression
// is always evaluated and its result is surfaced informationally, but does not gate whether the mock
// Response is injected. If branchFilter is non-empty the environments context iterates only the matching
// environment; promotionstrategy context always runs the shared flow for all applicable environments.
// The simulator does not touch Kubernetes or the network.
//
// When opts.PromotionStrategyUpdated or opts.WebRequestCommitStatusUpdated is non-nil, an optional
// fourth "after-state-change" step is appended. Step 4 runs exactly like after-response (Response=nil,
// all prior outputs carried from step 3) but swaps the corresponding input for the updated value.
// This is how users exercise scenarios where upstream state changes between reconciles (new dry SHA,
// edited template, etc.) and verify their carry-forward logic behaves as expected.
func SimulateWebRequestTemplates(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	ns SimulationNamespaceMetadata,
	mock SimulationMockResponse,
	branchFilter string,
	opts SimulateWebRequestOptions,
) ([]WebRequestStepResult, error) {
	if wrcs == nil {
		return nil, errors.New("WebRequestCommitStatus is required")
	}
	if ps == nil {
		return nil, errors.New("PromotionStrategy is required")
	}

	// The evaluation helpers live on WebRequestCommitStatusReconciler, but they only touch
	// r.expressionCache — not the k8s client or settings manager. A bare reconciler is fine.
	r := &WebRequestCommitStatusReconciler{}

	nsMeta := namespaceMetadata(ns)

	applicableEnvs := r.getApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)
	if len(applicableEnvs) == 0 {
		return nil, fmt.Errorf("no applicable environments: WebRequestCommitStatus key %q does not match any entry in the PromotionStrategy ProposedCommitStatuses/ActiveCommitStatuses", wrcs.Spec.Key)
	}
	if branchFilter != "" {
		filtered := make([]promoterv1alpha1.Environment, 0, 1)
		for _, env := range applicableEnvs {
			if env.Branch == branchFilter {
				filtered = append(filtered, env)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("branch %q is not an applicable environment for this WebRequestCommitStatus", branchFilter)
		}
		applicableEnvs = filtered
	}

	psEnvStatusMap := buildPSEnvStatusMap(ps)
	currentShas, err := resolveCurrentShas(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("resolve current SHAs: %w", err)
	}

	// For step 4 we re-resolve SHAs against psUpdated (the updated status may point at new SHAs).
	// applicableEnvs is determined by wrcs.Spec.Key vs. ps.Spec — we keep the same env list rather
	// than re-resolving against psUpdated, because changing which envs are applicable mid-simulation
	// would require rerouting carry-forward state and is out of scope for the "state mutated"
	// scenario this step is meant to exercise.
	var updatedShas map[string]string
	if opts.PromotionStrategyUpdated != nil {
		updatedShas, err = resolveCurrentShas(applicableEnvs, buildPSEnvStatusMap(opts.PromotionStrategyUpdated), wrcs.Spec.ReportOn)
		if err != nil {
			return nil, fmt.Errorf("resolve updated SHAs for step 4: %w", err)
		}
	}

	inputs := contextSimInputs{
		wrcs:           wrcs,
		wrcsUpdated:    opts.WebRequestCommitStatusUpdated,
		ps:             ps,
		psUpdated:      opts.PromotionStrategyUpdated,
		nsMeta:         nsMeta,
		applicableEnvs: applicableEnvs,
		currentShas:    currentShas,
		updatedShas:    updatedShas,
		mock:           mock,
	}

	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		return simulatePromotionStrategyContext(ctx, r, inputs)
	}
	return simulateEnvironmentsContext(ctx, r, inputs)
}

// contextSimInputs bundles the arguments passed to simulateEnvironmentsContext and
// simulatePromotionStrategyContext. Introduced to stay under the revive argument-limit rule and
// keep call sites readable.
//
//nolint:govet // field ordering optimized for readability; internal struct with negligible allocation pressure
type contextSimInputs struct {
	wrcs           *promoterv1alpha1.WebRequestCommitStatus
	wrcsUpdated    *promoterv1alpha1.WebRequestCommitStatus
	ps             *promoterv1alpha1.PromotionStrategy
	psUpdated      *promoterv1alpha1.PromotionStrategy
	currentShas    map[string]string
	updatedShas    map[string]string
	applicableEnvs []promoterv1alpha1.Environment
	nsMeta         namespaceMetadata
	mock           SimulationMockResponse
}

// simStepLabels are the step labels in order. The 4th label is used only when psUpdated is provided.
var simStepLabels = []string{"before-response", "with-response", "after-response", "after-state-change"}

// SimStepAfterStateChange is the label for the optional 4th step — exposed so CLI consumers and
// tests can reference it without repeating the literal. The first three step labels are implementation
// details of the simulator, so they are not individually exported.
const SimStepAfterStateChange = "after-state-change"

// simEnvState tracks derived per-environment state carried forward between steps of the simulation.
// It mirrors the subset of lastReconciledState the simulator needs.
type simEnvState struct {
	TriggerOutput  map[string]any
	ResponseOutput map[string]any
	SuccessOutput  map[string]any
	PhasePerBranch map[string]promoterv1alpha1.CommitStatusPhase
	Phase          string
}

// jsonRoundtripMap encodes m to JSON and decodes it back into a fresh map. This is how the real
// controller persists output maps (status.*.triggerOutput / responseOutput / successOutput are
// *apiextensionsv1.JSON — marshaled on write, unmarshaled on the next reconcile's read). Doing
// the same thing between simulator steps ensures types match what the next step's expressions
// would see in production: e.g. time.Time values written by now() become RFC3339 strings, so
// date(TriggerOutput.lastRequestTime) works correctly across step boundaries.
func jsonRoundtripMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return m
	}
	out := make(map[string]any)
	if err := json.Unmarshal(raw, &out); err != nil {
		return m
	}
	return out
}

// persistSimEnvState models what the real controller does at the end of a reconcile: serialize
// the output maps to JSON (they are stored as *apiextensionsv1.JSON in status) and deserialize
// them again for the next reconcile to read. The simulator calls this at every step boundary so
// the next step sees the same types it would see in production.
func persistSimEnvState(s simEnvState) simEnvState {
	return simEnvState{
		TriggerOutput:  jsonRoundtripMap(s.TriggerOutput),
		ResponseOutput: jsonRoundtripMap(s.ResponseOutput),
		SuccessOutput:  jsonRoundtripMap(s.SuccessOutput),
		PhasePerBranch: s.PhasePerBranch,
		Phase:          s.Phase,
	}
}

// seedSimStateFromStatus builds the simulator's "prior state" map from a WebRequestCommitStatus's
// status block, mirroring what the real controller does at the start of each reconcile via
// lastReconciledStateFromEnvironment / lastReconciledStateFromContext. This lets simulation fixtures
// express "this is the state the controller left behind on the previous reconcile" — critical for
// scenarios that depend on persisted values like TriggerOutput.lastRequestTime (polling cooldown),
// TriggerOutput.lastFingerprint (fingerprint drift), or a pre-existing Phase ("already success").
//
// For mode.context = environments: returns one entry per branch found in status.environments,
// keyed by that branch.
// For mode.context = promotionstrategy: returns a single entry keyed by "" (matching the simulator's
// promotionstrategy-context state convention of a shared unkeyed state).
// Missing or empty status in either case yields an empty map, and the caller falls back to cold-start
// simEnvState{}.
func seedSimStateFromStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus) map[string]simEnvState {
	if wrcs == nil {
		return nil
	}
	out := make(map[string]simEnvState)

	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		if wrcs.Status.PromotionStrategyContext == nil {
			return nil
		}
		ls := lastReconciledStateFromContext(ctx, wrcs.Status.PromotionStrategyContext)
		out[""] = simEnvState{
			TriggerOutput:  ls.TriggerData,
			ResponseOutput: ls.ResponseData,
			SuccessOutput:  ls.SuccessData,
			Phase:          ls.Phase,
			PhasePerBranch: ls.PhasePerBranch,
		}
		return out
	}

	if len(wrcs.Status.Environments) == 0 {
		return nil
	}
	for i := range wrcs.Status.Environments {
		envStatus := &wrcs.Status.Environments[i]
		ls := lastReconciledStateFromEnvironment(ctx, envStatus)
		out[envStatus.Branch] = simEnvState{
			TriggerOutput:  ls.TriggerData,
			ResponseOutput: ls.ResponseData,
			SuccessOutput:  ls.SuccessData,
			Phase:          ls.Phase,
			PhasePerBranch: ls.PhasePerBranch,
		}
	}
	return out
}

// simulateEnvironmentsContext runs the simulation for mode.context = "environments".
// Each applicable environment has its own carry-forward state (TriggerOutput / ResponseOutput /
// SuccessOutput / Phase) propagated between steps. When in.psUpdated is non-nil a 4th "after-state-
// change" step is appended using in.psUpdated as the PromotionStrategy (with in.updatedShas as the
// reported SHAs) while preserving step 3's derived outputs.
func simulateEnvironmentsContext(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	in contextSimInputs,
) ([]WebRequestStepResult, error) {
	// Seed step-1 prior state from wrcs.Status when populated (mirrors how the real controller
	// starts a reconcile on a resource with existing status). When status is absent or empty,
	// statePerEnv entries fall back to zero-valued simEnvState — the cold-start behavior.
	seedFromBase := seedSimStateFromStatus(ctx, in.wrcs)
	statePerEnv := make(map[string]simEnvState, len(in.applicableEnvs))
	for _, env := range in.applicableEnvs {
		if seeded, ok := seedFromBase[env.Branch]; ok {
			statePerEnv[env.Branch] = seeded
		} else {
			statePerEnv[env.Branch] = simEnvState{}
		}
	}

	// Precompute a re-seed for step 4 when wrcsUpdated carries populated status. If wrcsUpdated is
	// nil or has no status, step 4 simply inherits step 3's carry-forward via statePerEnv.
	reseedForStep4 := seedSimStateFromStatus(ctx, in.wrcsUpdated)

	totalSteps := 3
	if in.psUpdated != nil || in.wrcsUpdated != nil {
		totalSteps = 4
	}

	results := make([]WebRequestStepResult, 0, totalSteps)
	for stepIdx := 0; stepIdx < totalSteps; stepIdx++ {
		label := simStepLabels[stepIdx]
		injectResponse := stepIdx == 1
		stepPS := in.ps
		stepShas := in.currentShas
		stepWRCS := in.wrcs
		if stepIdx == 3 {
			if in.psUpdated != nil {
				stepPS = in.psUpdated
				stepShas = in.updatedShas
			}
			if in.wrcsUpdated != nil {
				stepWRCS = in.wrcsUpdated
			}
			// Re-seed per-branch carry-forward when the updated WRCS carries status for the branch.
			// Branches not present in reseedForStep4 keep their step-3 carry-forward state.
			for branch, seeded := range reseedForStep4 {
				statePerEnv[branch] = seeded
			}
		}

		stepResult := WebRequestStepResult{
			Label:   label,
			Context: string(promoterv1alpha1.ContextEnvironments),
		}

		for _, env := range in.applicableEnvs {
			prior := statePerEnv[env.Branch]
			branch := env.Branch

			td := templateData{
				Branch:                 branch,
				Phase:                  prior.Phase,
				PromotionStrategy:      stepPS,
				WebRequestCommitStatus: stepWRCS,
				NamespaceMetadata:      in.nsMeta,
				TriggerOutput:          prior.TriggerOutput,
				ResponseOutput:         prior.ResponseOutput,
				SuccessOutput:          prior.SuccessOutput,
			}

			eval, next, cs := runOneStepForBranch(ctx, r, stepInputs{
				wrcs:           stepWRCS,
				td:             td,
				mock:           in.mock,
				branch:         branch,
				sha:            stepShas[branch],
				injectResponse: injectResponse,
			})
			// JSON-roundtrip at the step boundary to mirror how the real controller persists
			// status: time.Time written by now() becomes a string, etc. See persistSimEnvState.
			statePerEnv[branch] = persistSimEnvState(next)
			stepResult.Evaluations = append(stepResult.Evaluations, eval)
			stepResult.CommitStatuses = append(stepResult.CommitStatuses, cs)
		}

		results = append(results, stepResult)
	}

	return results, nil
}

// simulatePromotionStrategyContext runs the simulation for mode.context = "promotionstrategy".
// A single shared evaluation (trigger / HTTP request / success) is carried forward between steps,
// and CommitStatuses are rendered per applicable environment using PhasePerBranch when present.
// When in.psUpdated or in.wrcsUpdated is non-nil a 4th "after-state-change" step is appended with
// those values swapped in (and in.updatedShas as the reported SHAs when the PS changed) while
// preserving step 3's derived outputs.
func simulatePromotionStrategyContext(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	in contextSimInputs,
) ([]WebRequestStepResult, error) {
	// Seed step-1 prior state from wrcs.Status.PromotionStrategyContext when populated. Empty or
	// missing status falls back to zero-valued simEnvState (cold-start, pre-existing behavior).
	var prior simEnvState
	if seeded := seedSimStateFromStatus(ctx, in.wrcs); seeded != nil {
		if s, ok := seeded[""]; ok {
			prior = s
		}
	}
	reseedForStep4 := seedSimStateFromStatus(ctx, in.wrcsUpdated)

	totalSteps := 3
	if in.psUpdated != nil || in.wrcsUpdated != nil {
		totalSteps = 4
	}

	results := make([]WebRequestStepResult, 0, totalSteps)
	for stepIdx := 0; stepIdx < totalSteps; stepIdx++ {
		label := simStepLabels[stepIdx]
		injectResponse := stepIdx == 1
		stepPS := in.ps
		stepShas := in.currentShas
		stepWRCS := in.wrcs
		if stepIdx == 3 {
			if in.psUpdated != nil {
				stepPS = in.psUpdated
				stepShas = in.updatedShas
			}
			if in.wrcsUpdated != nil {
				stepWRCS = in.wrcsUpdated
			}
			// If the updated WRCS carries a shared promotionstrategy-context status, re-seed the
			// single shared prior from it. Otherwise step 4 inherits step 3's carry-forward.
			if seeded, ok := reseedForStep4[""]; ok {
				prior = seeded
			}
		}

		stepResult := WebRequestStepResult{
			Label:   label,
			Context: string(promoterv1alpha1.ContextPromotionStrategy),
		}

		td := templateData{
			Phase:                  prior.Phase,
			PromotionStrategy:      stepPS,
			WebRequestCommitStatus: stepWRCS,
			NamespaceMetadata:      in.nsMeta,
			TriggerOutput:          prior.TriggerOutput,
			ResponseOutput:         prior.ResponseOutput,
			SuccessOutput:          prior.SuccessOutput,
		}

		eval, next, _ := runOneStepForBranch(ctx, r, stepInputs{
			wrcs:           stepWRCS,
			td:             td,
			mock:           in.mock,
			injectResponse: injectResponse,
			psContext:      true,
		})
		// JSON-roundtrip at the step boundary to mirror how the real controller persists status:
		// time.Time written by now() becomes a string, etc. See persistSimEnvState.
		prior = persistSimEnvState(next)

		// Render one CommitStatus per applicable environment using resolved per-branch phase.
		resolved := resolveAllBranchPhases(in.applicableEnvs, promoterv1alpha1.CommitStatusPhase(eval.Phase), next.PhasePerBranch)
		commitTd := td.withLatestOutputs(nil, eval.TriggerOutput, nil)
		if next.ResponseOutput != nil {
			commitTd.ResponseOutput = next.ResponseOutput
		}
		if next.SuccessOutput != nil {
			commitTd.SuccessOutput = next.SuccessOutput
		}
		for _, env := range in.applicableEnvs {
			envPhase := resolved[env.Branch]
			envTd := commitTd
			envTd.Branch = env.Branch
			envTd.Phase = string(envPhase)
			rendered, err := renderCommitStatusTemplates(stepWRCS, envTd)
			if err != nil {
				stepResult.Errors = append(stepResult.Errors, fmt.Sprintf("render CommitStatus templates for branch %q: %v", env.Branch, err))
				stepResult.CommitStatuses = append(stepResult.CommitStatuses, RenderedCommitStatus{Branch: env.Branch, Sha: stepShas[env.Branch], Phase: string(envPhase)})
				continue
			}
			stepResult.CommitStatuses = append(stepResult.CommitStatuses, RenderedCommitStatus{
				Branch:      env.Branch,
				Sha:         stepShas[env.Branch],
				Phase:       string(envPhase),
				Description: rendered.Description,
				URL:         rendered.URL,
			})
		}

		stepResult.Evaluations = append(stepResult.Evaluations, eval)
		results = append(results, stepResult)
	}

	return results, nil
}

// stepInputs bundles the arguments runOneStepForBranch needs. Using a struct keeps the parameter list
// manageable and lets the simulator steps stay readable.
type stepInputs struct {
	wrcs           *promoterv1alpha1.WebRequestCommitStatus
	td             templateData
	mock           SimulationMockResponse
	branch         string
	sha            string
	injectResponse bool
	psContext      bool
}

// runOneStepForBranch executes a single simulation step for a single (branch, sha) pair — or, in
// promotionstrategy context, the shared evaluation with empty branch/sha. It always evaluates
// trigger.when (reporting its result informationally), optionally injects the mock HTTP response,
// runs trigger output / response output / success.when / success.when.output as configured, and
// renders the CommitStatus description and URL. It never touches the network or the Kubernetes API.
//
// Returns (eval, nextState, renderedCommitStatus). In promotionstrategy context the caller renders
// CommitStatuses itself (one per applicable environment using PhasePerBranch).
func runOneStepForBranch(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	in stepInputs,
) (WebRequestStepEvaluation, simEnvState, RenderedCommitStatus) {
	wrcs := in.wrcs
	td := in.td
	branch := in.branch
	sha := in.sha
	mock := in.mock
	injectResponse := in.injectResponse
	psContext := in.psContext
	eval := WebRequestStepEvaluation{Branch: branch}
	next := simEnvState{
		TriggerOutput:  td.TriggerOutput,
		ResponseOutput: td.ResponseOutput,
		SuccessOutput:  td.SuccessOutput,
		Phase:          td.Phase,
	}

	// 1. Trigger evaluation (information only) + trigger.when.output refresh.
	//
	// IMPORTANT: The result of trigger.when.output is the NEXT reconcile's TriggerOutput. The real
	// controller persists it to status at the end of the reconcile; within the current reconcile,
	// success.when continues to see the PRIOR TriggerOutput (from last reconcile's status). We
	// mirror that here: newTriggerData only goes into next.TriggerOutput, never td.TriggerOutput.
	//
	// This ordering is what makes fingerprint-based carry-forward work: in step N, success.when
	// compares Variables.fingerprint against TriggerOutput.lastFingerprint (written by step N-1),
	// not against the lastFingerprint that step N's when.output would write. Without this, a
	// state change that advances the fingerprint would never flip a previously-successful gate
	// because when.output would refresh lastFingerprint to match Variables.fingerprint in the
	// same step.
	if wrcs.Spec.Mode.Trigger != nil {
		shouldFire, newTriggerData, err := r.evaluateTriggerWhenBranch(ctx, wrcs.Spec.Mode.Trigger, td)
		eval.TriggerEval = TriggerEvalResult{Evaluated: true, ShouldFire: shouldFire}
		if err != nil {
			eval.TriggerEval.Error = err.Error()
			eval.Errors = append(eval.Errors, fmt.Sprintf("trigger evaluation: %v", err))
		} else if newTriggerData != nil {
			next.TriggerOutput = newTriggerData
		}
	}
	eval.TriggerOutput = next.TriggerOutput

	// 2. When injecting response, render HTTP templates and process response output.
	var resp *httpResponse
	if injectResponse {
		resp = injectMockResponse(ctx, r, wrcs, &td, &next, &eval, mock)
	}
	eval.ResponseOutput = td.ResponseOutput

	// 3. success.when expression + success.when.output.
	successExprData := successWhenExprData(td, resp)
	successExprData, err := r.enrichWhenExprEnv(ctx, wrcs.Spec.Success.When, successExprData)
	if err != nil {
		eval.Errors = append(eval.Errors, fmt.Sprintf("success.when.variables: %v", err))
	}
	phase, phasePerBranch, err := evaluateSuccessPhaseForSim(ctx, r, wrcs, successExprData, psContext)
	if err != nil {
		eval.Errors = append(eval.Errors, fmt.Sprintf("success.when expression: %v", err))
	} else {
		next.Phase = string(phase)
		eval.Phase = string(phase)
		if phasePerBranch != nil {
			stringMap := make(map[string]string, len(phasePerBranch))
			for b, p := range phasePerBranch {
				stringMap[b] = string(p)
			}
			eval.PhasePerBranch = stringMap
			next.PhasePerBranch = phasePerBranch
		}
	}

	// Like TriggerOutput, the result of success.when.output is the NEXT reconcile's SuccessOutput
	// and is not visible to success.when itself within this reconcile.
	if wrcs.Spec.Success.When.Output != nil && strings.TrimSpace(wrcs.Spec.Success.When.Output.Expression) != "" {
		extracted, err := r.evaluateSuccessDataExpression(ctx, wrcs.Spec.Success.When.Output.Expression, successExprData)
		if err != nil {
			eval.Errors = append(eval.Errors, fmt.Sprintf("success.when.output expression: %v", err))
		} else {
			next.SuccessOutput = extracted
		}
	}
	eval.SuccessOutput = next.SuccessOutput

	// 4. Render CommitStatus description/url with the latest outputs. In promotionstrategy context
	// the caller renders per-branch CommitStatuses itself; we return an empty value here.
	if psContext {
		return eval, next, RenderedCommitStatus{}
	}
	commitTd := td.withLatestOutputs(nil, next.TriggerOutput, nil)
	if next.ResponseOutput != nil {
		commitTd.ResponseOutput = next.ResponseOutput
	}
	if next.SuccessOutput != nil {
		commitTd.SuccessOutput = next.SuccessOutput
	}
	commitTd.Phase = eval.Phase
	rendered, err := renderCommitStatusTemplates(wrcs, commitTd)
	if err != nil {
		eval.Errors = append(eval.Errors, fmt.Sprintf("render CommitStatus templates: %v", err))
		return eval, next, RenderedCommitStatus{Branch: branch, Sha: sha, Phase: eval.Phase}
	}
	return eval, next, RenderedCommitStatus{
		Branch:      branch,
		Sha:         sha,
		Phase:       eval.Phase,
		Description: rendered.Description,
		URL:         rendered.URL,
	}
}

// injectMockResponse renders the HTTP request templates and applies the mock response as if the
// controller had just performed the HTTP call. It populates eval.RenderedRequest, eval.MockResponse,
// runs the optional response.output expression to refresh ResponseOutput on both td and next, and
// returns a pointer to the synthetic httpResponse for success.when to consume.
func injectMockResponse(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td *templateData,
	next *simEnvState,
	eval *WebRequestStepEvaluation,
	mock SimulationMockResponse,
) *httpResponse {
	eval.ResponseInjected = true
	eval.MockResponse = &mock

	rendered, err := renderHTTPRequestTemplates(wrcs, *td)
	if err != nil {
		eval.Errors = append(eval.Errors, fmt.Sprintf("render HTTP request templates: %v", err))
	} else {
		eval.RenderedRequest = &RenderedHTTPRequest{
			Method:  wrcs.Spec.HTTPRequest.Method,
			URL:     rendered.URL,
			Body:    rendered.Body,
			Headers: rendered.Headers,
		}
	}

	resp := httpResponse(mock)

	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.Response != nil {
		extracted, err := r.evaluateResponseDataExpression(ctx, wrcs.Spec.Mode.Trigger.Response.Output.Expression, resp)
		if err != nil {
			eval.Errors = append(eval.Errors, fmt.Sprintf("response.output expression: %v", err))
		} else {
			td.ResponseOutput = extracted
			next.ResponseOutput = extracted
		}
	}
	return &resp
}

// evaluateSuccessPhaseForSim dispatches success.when expression evaluation to the appropriate helper
// based on psContext. It mirrors WebRequestCommitStatusReconciler.evaluateSuccessPhase but reads the
// context mode from the caller rather than from wrcs.Spec, so the simulator does not depend on its
// own dispatch logic matching the reconciler's for defaulting.
func evaluateSuccessPhaseForSim(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	exprData map[string]any,
	psContext bool,
) (promoterv1alpha1.CommitStatusPhase, map[string]promoterv1alpha1.CommitStatusPhase, error) {
	if psContext {
		return r.evaluateValidationExpressionForPromotionStrategy(ctx, wrcs.Spec.Success.When.Expression, exprData)
	}
	passed, err := r.evaluateValidationExpression(ctx, wrcs.Spec.Success.When.Expression, exprData)
	if err != nil {
		return "", nil, err
	}
	if passed {
		return promoterv1alpha1.CommitPhaseSuccess, nil, nil
	}
	return promoterv1alpha1.CommitPhasePending, nil, nil
}
