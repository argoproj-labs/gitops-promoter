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
	"maps"
	"slices"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// SimulationNamespaceMetadata holds the namespace labels and annotations that the simulator exposes
// to templates and expressions via templateData.NamespaceMetadata.
type SimulationNamespaceMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// SimulationMockResponse is a user-supplied mock HTTP response the simulator injects when a step
// performs a synthetic HTTP call. Body is any (plain string or JSON-decoded map/slice/scalar).
// Headers follow http.Header shape.
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
// In trigger mode it gates mock injection together with Evaluated and Error; in polling mode the
// trigger block is unused and injection does not consult this field.
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

// WebRequestStepResult is the outcome of one simulation step (two by default, or three when
// after-state-change is enabled). For environments context it
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

// SimulateWebRequestOptions configures the optional third reconcile ("after-state-change") and an
// alternate mock for that step. Set PromotionStrategyUpdated and/or WebRequestCommitStatusUpdated to
// append that step (swap those resources in for the third step only; prior outputs still carry forward).
// If both are nil, the simulator runs two steps and MockResponseUpdated must be nil.
type SimulateWebRequestOptions struct {
	// PromotionStrategyUpdated swaps the PromotionStrategy used in after-state-change. Typical use: simulate a
	// new Proposed.Note.DrySha arriving between reconciles to exercise fingerprint-based
	// invalidation in success.when carry-forward.
	PromotionStrategyUpdated *promoterv1alpha1.PromotionStrategy
	// WebRequestCommitStatusUpdated swaps the WebRequestCommitStatus used in after-state-change. Typical use:
	// model the controller having written back updated status between reconciles (new
	// Status.Environments[*].TriggerOutput / ResponseOutput / LastRequestTime / conditions) so
	// templates and expressions that reference .WebRequestCommitStatus.Status.* see the new
	// values. Less commonly, it also models an admin editing the spec between reconciles.
	WebRequestCommitStatusUpdated *promoterv1alpha1.WebRequestCommitStatus
	// MockResponseUpdated, when non-nil, is the mock HTTP response for the after-state-change step
	// when that step injects (requires PromotionStrategyUpdated and/or WebRequestCommitStatusUpdated).
	// When nil, the primary mock passed to SimulateWebRequestTemplates is used for every step.
	MockResponseUpdated *SimulationMockResponse
}

// SimulateWebRequestTemplates runs a reconcile-shaped templates simulation for a
// WebRequestCommitStatus against the given PromotionStrategy and namespace metadata, using mock as
// the HTTP response injected whenever a step's trigger says fire (or whenever polling mode is
// configured, since polling has no gate expression). The simulator emits two steps by default:
//
//   - "reconcile":      first reconcile. Evaluates trigger.when; injects the mock response iff the
//     trigger says fire (or polling mode is configured). Runs response.output /
//     success.when / trigger.when.output as configured and renders CommitStatus
//     templates with the resulting state.
//   - "next-reconcile": second reconcile with state carried forward from "reconcile". Like the real
//     controller, the mock response is injected iff the trigger fires (or polling mode is configured).
//     When the trigger is false (typical fingerprint carry-forward), no injection runs and
//     success.when sees Response=nil — the same pattern as reconcile.
//
// If branchFilter is non-empty the environments context iterates only the matching environment;
// promotionstrategy context always runs the shared flow for all applicable environments. The
// simulator does not touch Kubernetes or the network.
//
// When opts.PromotionStrategyUpdated or opts.WebRequestCommitStatusUpdated is non-nil, an optional
// third "after-state-change" step is appended. It represents a later reconcile after upstream state
// changed between reconciles (new dry SHA, edited template, etc.); like "reconcile" it injects iff
// the (re-evaluated) trigger fires or polling is configured, so users can verify fingerprint-based
// invalidation and re-fire logic behaves as expected. When opts.MockResponseUpdated is set, that
// step uses it as the injected mock instead of the primary mock (e.g. different status/body).
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
	if opts.MockResponseUpdated != nil &&
		opts.PromotionStrategyUpdated == nil &&
		opts.WebRequestCommitStatusUpdated == nil {
		return nil, errors.New("SimulateWebRequestOptions.MockResponseUpdated requires PromotionStrategyUpdated and/or WebRequestCommitStatusUpdated")
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
		applicableEnvs = slices.DeleteFunc(applicableEnvs, func(env promoterv1alpha1.Environment) bool {
			return env.Branch != branchFilter
		})
		if len(applicableEnvs) == 0 {
			return nil, fmt.Errorf("branch %q is not an applicable environment for this WebRequestCommitStatus", branchFilter)
		}
	}

	psEnvStatusMap := buildPSEnvStatusMap(ps)
	currentShas, err := resolveCurrentShas(applicableEnvs, psEnvStatusMap, wrcs.Spec.ReportOn)
	if err != nil {
		return nil, fmt.Errorf("resolve current SHAs: %w", err)
	}

	// For after-state-change we re-resolve SHAs against psUpdated (the updated status may point at new SHAs).
	// applicableEnvs is determined by wrcs.Spec.Key vs. ps.Spec — we keep the same env list rather
	// than re-resolving against psUpdated, because changing which envs are applicable mid-simulation
	// would require rerouting carry-forward state and is out of scope for the "state mutated"
	// scenario this step is meant to exercise.
	var updatedShas map[string]string
	if opts.PromotionStrategyUpdated != nil {
		updatedShas, err = resolveCurrentShas(applicableEnvs, buildPSEnvStatusMap(opts.PromotionStrategyUpdated), wrcs.Spec.ReportOn)
		if err != nil {
			return nil, fmt.Errorf("resolve updated SHAs for after-state-change: %w", err)
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
		mockUpdated:    opts.MockResponseUpdated,
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
	mockUpdated    *SimulationMockResponse
}

// simStepLabels are the step labels in order. The 3rd label is used only when psUpdated or
// wrcsUpdated is provided.
var simStepLabels = []string{"reconcile", "next-reconcile", "after-state-change"}

// SimStepAfterStateChange is the label for the optional final step — exposed so CLI consumers and
// tests can reference it without repeating the literal. The first two step labels are implementation
// details of the simulator, so they are not individually exported.
const SimStepAfterStateChange = "after-state-change"

// simStepCount returns how many steps to run: default is reconcile + next-reconcile; a third step
// runs when simulating upstream or WRCS changes between reconciles.
func simStepCount(psUpdated *promoterv1alpha1.PromotionStrategy, wrcsUpdated *promoterv1alpha1.WebRequestCommitStatus) int {
	if psUpdated != nil || wrcsUpdated != nil {
		return len(simStepLabels)
	}
	return len(simStepLabels) - 1
}

// resolvedStep bundles the per-step values both simulators need: which WRCS / PS / SHAs / mock apply
// to this step, what label to tag it with, and whether it is the optional after-state-change step.
// Returned by contextSimInputs.resolveStep so the two step loops don't repeat the "swap in updated
// values for the last step" logic.
type resolvedStep struct {
	label              string
	ps                 *promoterv1alpha1.PromotionStrategy
	wrcs               *promoterv1alpha1.WebRequestCommitStatus
	shas               map[string]string
	mock               SimulationMockResponse
	isAfterStateChange bool
}

// resolveStep returns the resources, label, and mock to use for stepIdx. For the after-state-change
// step it swaps in psUpdated / wrcsUpdated / updatedShas / mockUpdated when those are set, falling
// back to the primary values otherwise.
func (in contextSimInputs) resolveStep(stepIdx int) resolvedStep {
	label := simStepLabels[stepIdx]
	step := resolvedStep{
		label: label,
		ps:    in.ps,
		wrcs:  in.wrcs,
		shas:  in.currentShas,
		mock:  in.mock,
	}
	if label != SimStepAfterStateChange {
		return step
	}
	step.isAfterStateChange = true
	if in.psUpdated != nil {
		step.ps = in.psUpdated
		step.shas = in.updatedShas
	}
	if in.wrcsUpdated != nil {
		step.wrcs = in.wrcsUpdated
	}
	if in.mockUpdated != nil {
		step.mock = *in.mockUpdated
	}
	return step
}

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

// seedEnvStateFromStatus builds a per-branch simEnvState map from wrcs.Status.Environments,
// mirroring what the real controller does at the start of each reconcile in environments context
// via lastReconciledStateFromEnvironment. This lets simulation fixtures express "this is the state
// the controller left behind on the previous reconcile" — critical for scenarios that depend on
// persisted values like TriggerOutput.lastRequestTime (polling cooldown), TriggerOutput.lastFingerprint
// (fingerprint drift), or a pre-existing Phase ("already success"). Returns nil when wrcs is nil or
// has no environment status entries; callers fall back to cold-start simEnvState{} for branches not
// present in the returned map.
func seedEnvStateFromStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus) map[string]simEnvState {
	if wrcs == nil || len(wrcs.Status.Environments) == 0 {
		return nil
	}
	out := make(map[string]simEnvState, len(wrcs.Status.Environments))
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

// seedSharedStateFromStatus builds the single shared simEnvState from wrcs.Status.PromotionStrategyContext
// for promotionstrategy context simulations. Returns (zero, false) when wrcs is nil or has no shared
// context status; callers fall back to a cold-start simEnvState{}.
func seedSharedStateFromStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus) (simEnvState, bool) {
	if wrcs == nil || wrcs.Status.PromotionStrategyContext == nil {
		return simEnvState{}, false
	}
	ls := lastReconciledStateFromContext(ctx, wrcs.Status.PromotionStrategyContext)
	return simEnvState{
		TriggerOutput:  ls.TriggerData,
		ResponseOutput: ls.ResponseData,
		SuccessOutput:  ls.SuccessData,
		Phase:          ls.Phase,
		PhasePerBranch: ls.PhasePerBranch,
	}, true
}

// simulateEnvironmentsContext runs the simulation for mode.context = "environments".
// Each applicable environment has its own carry-forward state (TriggerOutput / ResponseOutput /
// SuccessOutput / Phase) propagated between steps. When in.psUpdated or in.wrcsUpdated is non-nil
// an "after-state-change" step is appended using the updated values (with in.updatedShas as the
// reported SHAs when the PS changed) while preserving the prior step's derived outputs.
func simulateEnvironmentsContext(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	in contextSimInputs,
) ([]WebRequestStepResult, error) {
	// Seed "reconcile" prior state from wrcs.Status when populated (mirrors how the real controller
	// starts a reconcile on a resource with existing status). When status is absent or empty,
	// statePerEnv entries fall back to zero-valued simEnvState — the cold-start behavior.
	seedFromBase := seedEnvStateFromStatus(ctx, in.wrcs)
	statePerEnv := make(map[string]simEnvState, len(in.applicableEnvs))
	for _, env := range in.applicableEnvs {
		statePerEnv[env.Branch] = seedFromBase[env.Branch]
	}

	// Precompute a re-seed for "after-state-change" when wrcsUpdated carries populated status. If
	// wrcsUpdated is nil or has no status, the step simply inherits the prior step's carry-forward
	// via statePerEnv.
	reseedForAfterStateChange := seedEnvStateFromStatus(ctx, in.wrcsUpdated)

	totalSteps := simStepCount(in.psUpdated, in.wrcsUpdated)
	results := make([]WebRequestStepResult, 0, totalSteps)
	for stepIdx := 0; stepIdx < totalSteps; stepIdx++ {
		step := in.resolveStep(stepIdx)
		if step.isAfterStateChange {
			// Re-seed per-branch carry-forward when the updated WRCS carries status for the branch.
			// Branches not present keep their prior-step carry-forward state.
			maps.Copy(statePerEnv, reseedForAfterStateChange)
		}

		stepResult := WebRequestStepResult{
			Label:   step.label,
			Context: string(promoterv1alpha1.ContextEnvironments),
		}

		for _, env := range in.applicableEnvs {
			prior := statePerEnv[env.Branch]
			branch := env.Branch

			td := templateData{
				Branch:                 branch,
				Phase:                  prior.Phase,
				PromotionStrategy:      step.ps,
				WebRequestCommitStatus: step.wrcs,
				NamespaceMetadata:      in.nsMeta,
				TriggerOutput:          prior.TriggerOutput,
				ResponseOutput:         prior.ResponseOutput,
				SuccessOutput:          prior.SuccessOutput,
			}

			eval, next, cs := runOneStepForBranch(ctx, r, stepInputs{
				wrcs:   step.wrcs,
				td:     td,
				mock:   step.mock,
				branch: branch,
				sha:    step.shas[branch],
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
// When in.psUpdated or in.wrcsUpdated is non-nil an "after-state-change" step is appended with
// those values swapped in (and in.updatedShas as the reported SHAs when the PS changed) while
// preserving the prior step's derived outputs.
func simulatePromotionStrategyContext(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	in contextSimInputs,
) ([]WebRequestStepResult, error) {
	// Seed "reconcile" prior state from wrcs.Status.PromotionStrategyContext when populated. Empty
	// or missing status falls back to zero-valued simEnvState (cold-start, pre-existing behavior).
	prior, _ := seedSharedStateFromStatus(ctx, in.wrcs)
	reseedForAfterStateChange, hasReseed := seedSharedStateFromStatus(ctx, in.wrcsUpdated)

	totalSteps := simStepCount(in.psUpdated, in.wrcsUpdated)
	results := make([]WebRequestStepResult, 0, totalSteps)
	for stepIdx := 0; stepIdx < totalSteps; stepIdx++ {
		step := in.resolveStep(stepIdx)
		if step.isAfterStateChange && hasReseed {
			// The updated WRCS carries a shared promotionstrategy-context status, so re-seed the
			// single shared prior from it. Otherwise the step inherits the prior step's carry-forward.
			prior = reseedForAfterStateChange
		}

		stepResult := WebRequestStepResult{
			Label:   step.label,
			Context: string(promoterv1alpha1.ContextPromotionStrategy),
		}

		td := templateData{
			Phase:                  prior.Phase,
			PromotionStrategy:      step.ps,
			WebRequestCommitStatus: step.wrcs,
			NamespaceMetadata:      in.nsMeta,
			TriggerOutput:          prior.TriggerOutput,
			ResponseOutput:         prior.ResponseOutput,
			SuccessOutput:          prior.SuccessOutput,
		}

		eval, next, _ := runOneStepForBranch(ctx, r, stepInputs{
			wrcs:      step.wrcs,
			td:        td,
			mock:      step.mock,
			psContext: true,
		})
		// JSON-roundtrip at the step boundary to mirror how the real controller persists status:
		// time.Time written by now() becomes a string, etc. See persistSimEnvState.
		prior = persistSimEnvState(next)

		// Render one CommitStatus per applicable environment using resolved per-branch phase.
		resolved := resolveAllBranchPhases(in.applicableEnvs, promoterv1alpha1.CommitStatusPhase(eval.Phase), next.PhasePerBranch)
		commitTd := buildCommitTemplateData(td, next, eval.TriggerOutput)
		for _, env := range in.applicableEnvs {
			envPhase := string(resolved[env.Branch])
			cs, err := renderCommitStatusForBranch(step.wrcs, commitTd, env.Branch, step.shas[env.Branch], envPhase)
			if err != nil {
				stepResult.Errors = append(stepResult.Errors, fmt.Sprintf("render CommitStatus templates for branch %q: %v", env.Branch, err))
			}
			stepResult.CommitStatuses = append(stepResult.CommitStatuses, cs)
		}

		stepResult.Evaluations = append(stepResult.Evaluations, eval)
		results = append(results, stepResult)
	}

	return results, nil
}

// buildCommitTemplateData returns a templateData for rendering the CommitStatus description/url,
// with the latest TriggerOutput applied and ResponseOutput/SuccessOutput overridden from the
// post-step state whenever those outputs were refreshed during the step. baseTd's Branch and Phase
// are left untouched; callers set them per environment via renderCommitStatusForBranch.
func buildCommitTemplateData(baseTd templateData, next simEnvState, triggerOutput map[string]any) templateData {
	commitTd := baseTd.withLatestOutputs(nil, triggerOutput, nil)
	if next.ResponseOutput != nil {
		commitTd.ResponseOutput = next.ResponseOutput
	}
	if next.SuccessOutput != nil {
		commitTd.SuccessOutput = next.SuccessOutput
	}
	return commitTd
}

// renderCommitStatusForBranch renders the CommitStatus description/url templates for a single
// (branch, sha, phase) using commitTd as the base. On template error it returns a
// RenderedCommitStatus with only Branch/Sha/Phase populated along with the error so the caller
// can record the failure in step-level diagnostics while still emitting the phase.
func renderCommitStatusForBranch(
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	commitTd templateData,
	branch, sha, phase string,
) (RenderedCommitStatus, error) {
	envTd := commitTd
	envTd.Branch = branch
	envTd.Phase = phase
	rendered, err := renderCommitStatusTemplates(wrcs, envTd)
	if err != nil {
		return RenderedCommitStatus{Branch: branch, Sha: sha, Phase: phase}, err
	}
	return RenderedCommitStatus{
		Branch:      branch,
		Sha:         sha,
		Phase:       phase,
		Description: rendered.Description,
		URL:         rendered.URL,
	}, nil
}

// stepInputs bundles the arguments runOneStepForBranch needs. Using a struct keeps the parameter list
// manageable and lets the simulator steps stay readable.
type stepInputs struct {
	wrcs      *promoterv1alpha1.WebRequestCommitStatus
	td        templateData
	mock      SimulationMockResponse
	branch    string
	sha       string
	psContext bool
}

// runOneStepForBranch executes a single simulation step for a single (branch, sha) pair — or, in
// promotionstrategy context, the shared evaluation with empty branch/sha. It evaluates trigger.when
// (reporting its result), injects the mock HTTP response iff either the trigger says fire or polling
// mode is configured, runs trigger output / response output / success.when / success.when.output as
// configured, and renders the CommitStatus description and URL. It never touches the network or the
// Kubernetes API.
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
	psContext := in.psContext
	eval := WebRequestStepEvaluation{Branch: branch}
	next := simEnvState{
		TriggerOutput:  td.TriggerOutput,
		ResponseOutput: td.ResponseOutput,
		SuccessOutput:  td.SuccessOutput,
		Phase:          td.Phase,
	}

	// 1. Trigger evaluation + trigger.when.output refresh.
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

	// 2. Decide whether to inject the mock response and, if so, render HTTP templates and process
	// response output. Matches the controller on every simulated reconcile: trigger mode injects iff
	// trigger.when.expression fires without error; polling mode always injects (no gate expression).
	var resp *httpResponse
	if shouldInjectMock(wrcs.Spec.Mode, eval.TriggerEval) {
		injection := injectMockResponse(ctx, r, wrcs, td, mock)
		eval.ResponseInjected = true
		eval.MockResponse = &mock
		eval.RenderedRequest = injection.renderedRequest
		eval.Errors = append(eval.Errors, injection.errors...)
		resp = injection.resp
		if injection.responseOutputSet {
			td.ResponseOutput = injection.responseOutput
			next.ResponseOutput = injection.responseOutput
		}
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
	commitTd := buildCommitTemplateData(td, next, next.TriggerOutput)
	cs, err := renderCommitStatusForBranch(wrcs, commitTd, branch, sha, eval.Phase)
	if err != nil {
		eval.Errors = append(eval.Errors, fmt.Sprintf("render CommitStatus templates: %v", err))
	}
	return eval, next, cs
}

// shouldInjectMock returns whether the simulator should perform the mock HTTP injection for this
// step. It mirrors the real controller: trigger mode injects iff trigger.when.expression fires
// without error; polling mode always injects (no gate expression); any other shape returns false.
func shouldInjectMock(mode promoterv1alpha1.ModeSpec, tr TriggerEvalResult) bool {
	switch {
	case mode.Trigger != nil:
		return tr.Evaluated && tr.Error == "" && tr.ShouldFire
	case mode.Polling != nil:
		return true
	}
	return false
}

// mockInjection is the outcome of running a mock HTTP injection for a single simulator step: the
// synthetic httpResponse that success.when will consume, the rendered HTTP request (if templates
// rendered successfully), any extracted response.output map, and the errors produced along the way.
// responseOutputSet distinguishes "response.output expression was configured and produced nil or an
// empty map" from "response.output expression was not configured"; only when true should the caller
// overwrite td.ResponseOutput / next.ResponseOutput.
//
//nolint:govet // field ordering optimized for readability; internal struct with negligible allocation pressure
type mockInjection struct {
	resp              *httpResponse
	renderedRequest   *RenderedHTTPRequest
	responseOutput    map[string]any
	errors            []string
	responseOutputSet bool
}

// injectMockResponse renders the HTTP request templates, synthesizes the mock response, and runs
// the optional response.output expression. It returns a mockInjection describing what changed so
// the caller can apply the updates to templateData / simEnvState / WebRequestStepEvaluation
// explicitly, making the "what did injection touch" data flow visible at the call site.
func injectMockResponse(
	ctx context.Context,
	r *WebRequestCommitStatusReconciler,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td templateData,
	mock SimulationMockResponse,
) mockInjection {
	out := mockInjection{}

	rendered, err := renderHTTPRequestTemplates(wrcs, td)
	if err != nil {
		out.errors = append(out.errors, fmt.Sprintf("render HTTP request templates: %v", err))
	} else {
		out.renderedRequest = &RenderedHTTPRequest{
			Method:  wrcs.Spec.HTTPRequest.Method,
			URL:     rendered.URL,
			Body:    rendered.Body,
			Headers: rendered.Headers,
		}
	}

	resp := httpResponse(mock)
	out.resp = &resp

	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.Response != nil {
		extracted, err := r.evaluateResponseDataExpression(ctx, wrcs.Spec.Mode.Trigger.Response.Output.Expression, resp)
		if err != nil {
			out.errors = append(out.errors, fmt.Sprintf("response.output expression: %v", err))
		} else {
			out.responseOutput = extracted
			out.responseOutputSet = true
		}
	}
	return out
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
