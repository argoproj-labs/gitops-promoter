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
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// GetEnvsByBranch builds a map from branch name to EnvironmentStatus for fast lookups.
func GetEnvsByBranch(ps *promoterv1alpha1.PromotionStrategy) map[string]*promoterv1alpha1.EnvironmentStatus {
	m := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		m[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}
	return m
}

// GetCurrentShasByBranch builds a map of branch to reported SHA for each applicable environment,
// validating that every branch has a status entry and a non-empty SHA.
func GetCurrentShasByBranch(
	applicableEnvs []promoterv1alpha1.Environment,
	psEnvStatusMap map[string]*promoterv1alpha1.EnvironmentStatus,
	reportOn string,
) (map[string]string, error) {
	shas := make(map[string]string, len(applicableEnvs))
	for _, env := range applicableEnvs {
		envStatus, found := psEnvStatusMap[env.Branch]
		if !found {
			return nil, fmt.Errorf("environment %q not found in PromotionStrategy status", env.Branch)
		}
		sha := resolveReportedSha(envStatus, reportOn)
		if sha == "" {
			return nil, fmt.Errorf("no SHA available for environment %q (reportOn: %q)", env.Branch, reportOn)
		}
		shas[env.Branch] = sha
	}
	return shas, nil
}

// resolveReportedSha returns the SHA to report on based on the reportOn setting.
func resolveReportedSha(envStatus *promoterv1alpha1.EnvironmentStatus, reportOn string) string {
	if reportOn == constants.CommitRefActive {
		return envStatus.Active.Hydrated.Sha
	}
	return envStatus.Proposed.Hydrated.Sha
}

// GetApplicableEnvironments returns the PromotionStrategy environments that match a given
// WebRequestCommitStatus key / reportOn combination. An environment is included if its key is
// referenced in global or environment-specific ProposedCommitStatuses (when reportOn is "proposed"
// or default) or ActiveCommitStatuses (when reportOn is "active").
func GetApplicableEnvironments(ps *promoterv1alpha1.PromotionStrategy, key string, reportOn string) []promoterv1alpha1.Environment {
	globalSelectors := ps.Spec.ProposedCommitStatuses
	getEnvSelectors := func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
		return e.ProposedCommitStatuses
	}
	if reportOn == constants.CommitRefActive {
		globalSelectors = ps.Spec.ActiveCommitStatuses
		getEnvSelectors = func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
			return e.ActiveCommitStatuses
		}
	}

	keyInSelectors := func(selectors []promoterv1alpha1.CommitStatusSelector) bool {
		for _, sel := range selectors {
			if sel.Key == key {
				return true
			}
		}
		return false
	}
	keyInGlobal := keyInSelectors(globalSelectors)

	applicable := make([]promoterv1alpha1.Environment, 0, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		if keyInGlobal || keyInSelectors(getEnvSelectors(env)) {
			applicable = append(applicable, env)
		}
	}
	return applicable
}

// parsePerBranchPhases extracts defaultPhase and optional per-branch overrides from an expression
// result object of the form { defaultPhase?, environments?: [{ branch, phase }] }.
func parsePerBranchPhases(obj map[string]any) (promoterv1alpha1.CommitStatusPhase, map[string]promoterv1alpha1.CommitStatusPhase, error) {
	defaultPhaseStr, err := getString(obj, "defaultPhase")
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression defaultPhase: %w", err)
	}
	defaultPhase, err := parsePhaseString(defaultPhaseStr, promoterv1alpha1.CommitPhasePending)
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression defaultPhase: %w", err)
	}
	envsVal, hasEnvs := obj["environments"]
	if !hasEnvs || envsVal == nil {
		return defaultPhase, nil, nil
	}
	sl, ok := envsVal.([]any)
	if !ok {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments must be an array, got %T", envsVal)
	}
	if len(sl) == 0 {
		return defaultPhase, nil, nil
	}
	m := make(map[string]promoterv1alpha1.CommitStatusPhase, len(sl))
	for i, item := range sl {
		entry, ok := item.(map[string]any)
		if !ok {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d] must be object with branch and phase, got %T", i, item)
		}
		branch, err := getString(entry, "branch")
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].branch: %w", i, err)
		}
		if branch == "" {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d]: branch is required", i)
		}
		phaseStr, err := getString(entry, "phase")
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].phase: %w", i, err)
		}
		phase, err := parsePhaseString(phaseStr, defaultPhase)
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].phase: %w", i, err)
		}
		if _, exists := m[branch]; exists {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d]: duplicate branch %q", i, branch)
		}
		m[branch] = phase
	}
	return defaultPhase, m, nil
}

// getString reads an optional string field from an expression object. Missing or nil key yields ("", nil).
// A present value with non-string type returns an error.
func getString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string, got %T", key, v)
	}
	return s, nil
}

// parsePhaseString converts a phase string to a CommitStatusPhase value. An empty string returns
// defaultPhase; unknown values return an error.
func parsePhaseString(phaseStr string, defaultPhase promoterv1alpha1.CommitStatusPhase) (promoterv1alpha1.CommitStatusPhase, error) {
	switch phaseStr {
	case "success":
		return promoterv1alpha1.CommitPhaseSuccess, nil
	case "pending":
		return promoterv1alpha1.CommitPhasePending, nil
	case "failure":
		return promoterv1alpha1.CommitPhaseFailure, nil
	case "":
		return defaultPhase, nil
	default:
		return promoterv1alpha1.CommitPhasePending, fmt.Errorf("unrecognized phase %q, must be one of: success, pending, failure", phaseStr)
	}
}

// ResolvePhaseForBranch returns the phase for a branch from phasePerBranch, falling back to defaultPhase
// when phasePerBranch is nil or the branch is not in the map.
func ResolvePhaseForBranch(branch string, defaultPhase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) promoterv1alpha1.CommitStatusPhase {
	if phasePerBranch != nil {
		if p, ok := phasePerBranch[branch]; ok {
			return p
		}
	}
	return defaultPhase
}

// GetPhasesByBranch builds a complete PhasePerBranch map for all applicable environments,
// merging the default phase with any per-branch overrides from the expression result.
func GetPhasesByBranch(envs []promoterv1alpha1.Environment, defaultPhase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) map[string]promoterv1alpha1.CommitStatusPhase {
	resolved := make(map[string]promoterv1alpha1.CommitStatusPhase, len(envs))
	for _, env := range envs {
		resolved[env.Branch] = ResolvePhaseForBranch(env.Branch, defaultPhase, phasePerBranch)
	}
	return resolved
}

// AggregatePhase computes an overall phase from a PhasePerBranch map:
// success only if all branches succeeded, failure if any failed, pending otherwise.
func AggregatePhase(phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) string {
	if len(phasePerBranch) == 0 {
		return string(promoterv1alpha1.CommitPhasePending)
	}
	allSuccess := true
	for _, p := range phasePerBranch {
		if p == promoterv1alpha1.CommitPhaseFailure {
			return string(promoterv1alpha1.CommitPhaseFailure)
		}
		if p != promoterv1alpha1.CommitPhaseSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		return string(promoterv1alpha1.CommitPhaseSuccess)
	}
	return string(promoterv1alpha1.CommitPhasePending)
}

// LastSuccessfulShasForPromotionStrategyContext builds the map of branch to last successful SHA
// for context=promotionstrategy: it seeds from the prior reconcile's PromotionStrategyContext status
// and sets each branch's entry to the current reported SHA when that branch's resolved phase is success.
func LastSuccessfulShasForPromotionStrategyContext(
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
		if ResolvePhaseForBranch(branch, phase, phasePerBranch) == promoterv1alpha1.CommitPhaseSuccess {
			lastSuccessfulShas[branch] = currentShaPerBranch[branch]
		}
	}
	return lastSuccessfulShas
}
