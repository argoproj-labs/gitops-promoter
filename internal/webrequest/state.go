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
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// lastReconciledState holds deserialized state from the previous reconcile, extracted from either
// WebRequestCommitStatusEnvironmentStatus (per-env path) or
// WebRequestCommitStatusPromotionStrategyContextStatus (context=promotionstrategy path).
type lastReconciledState struct {
	TriggerData            map[string]any
	ResponseData           map[string]any
	SuccessData            map[string]any
	LastRequestTime        *metav1.Time
	LastResponseStatusCode *int
	ResponseOutput         *apiextensionsv1.JSON
	SuccessOutput          *apiextensionsv1.JSON
	PhasePerBranch         map[string]promoterv1alpha1.CommitStatusPhase
	Phase                  string
}

// lastReconciledStateFromEnvironment extracts the previous reconcile's state from a per-environment
// status entry, deserializing trigger and response output JSON into maps.
func lastReconciledStateFromEnvironment(ctx context.Context, status *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus) lastReconciledState {
	if status == nil {
		return lastReconciledState{}
	}
	logger := log.FromContext(ctx)
	s := lastReconciledState{
		Phase:                  string(status.Phase),
		LastRequestTime:        status.LastRequestTime,
		LastResponseStatusCode: status.LastResponseStatusCode,
		ResponseOutput:         status.ResponseOutput,
		SuccessOutput:          status.SuccessOutput,
	}
	var err error
	s.TriggerData, err = unmarshalJSONMap(status.TriggerOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal trigger data")
	}
	s.ResponseData, err = unmarshalJSONMap(status.ResponseOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal response data")
	}
	s.SuccessData, err = unmarshalJSONMap(status.SuccessOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal success data")
	}
	return s
}

// lastReconciledStateFromContext extracts the previous reconcile's state from the
// promotionstrategy-level context status, including per-branch phase overrides.
// Phase is computed as an aggregate of PhasePerBranch (success only if all branches succeeded,
// failure if any failed, pending otherwise).
func lastReconciledStateFromContext(ctx context.Context, status *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus) lastReconciledState {
	if status == nil {
		return lastReconciledState{}
	}
	logger := log.FromContext(ctx)
	phaseMap := phasePerBranchMapFromSlice(status.PhasePerBranch)
	s := lastReconciledState{
		Phase:                  aggregatePhase(phaseMap),
		LastRequestTime:        status.LastRequestTime,
		LastResponseStatusCode: status.LastResponseStatusCode,
		ResponseOutput:         status.ResponseOutput,
		SuccessOutput:          status.SuccessOutput,
		PhasePerBranch:         phaseMap,
	}
	var err error
	s.TriggerData, err = unmarshalJSONMap(status.TriggerOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal trigger data (context=promotionstrategy)")
	}
	s.ResponseData, err = unmarshalJSONMap(status.ResponseOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal response data (context=promotionstrategy)")
	}
	s.SuccessData, err = unmarshalJSONMap(status.SuccessOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal success data (context=promotionstrategy)")
	}
	return s
}

// phasePerBranchMapFromSlice converts a slice of per-branch phase items into a map keyed by branch.
// Returns nil when items is empty.
func phasePerBranchMapFromSlice(items []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem) map[string]promoterv1alpha1.CommitStatusPhase {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]promoterv1alpha1.CommitStatusPhase, len(items))
	for _, it := range items {
		m[it.Branch] = it.Phase
	}
	return m
}

// sortedStringKeys returns lexicographically sorted map keys (nil when m is empty).
func sortedStringKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// phasePerBranchSliceFromMap converts a branch→phase map into a sorted slice of per-branch phase items.
// Returns nil when the map is empty.
func phasePerBranchSliceFromMap(m map[string]promoterv1alpha1.CommitStatusPhase) []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem {
	if len(m) == 0 {
		return nil
	}
	branches := sortedStringKeys(m)
	out := make([]promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem, 0, len(m))
	for _, b := range branches {
		out = append(out, promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem{Branch: b, Phase: m[b]})
	}
	return out
}

// lastSuccessfulShasMapFromSlice converts a slice of last-successful-SHA items into a map keyed by branch.
// Returns nil when items is empty.
func lastSuccessfulShasMapFromSlice(items []promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem) map[string]string {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]string, len(items))
	for _, it := range items {
		m[it.Branch] = it.LastSuccessfulSha
	}
	return m
}

// lastSuccessfulShasSliceFromMap converts a branch→SHA map into a sorted slice of last-successful-SHA items.
// Returns nil when the map is empty.
func lastSuccessfulShasSliceFromMap(m map[string]string) []promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem {
	if len(m) == 0 {
		return nil
	}
	branches := sortedStringKeys(m)
	out := make([]promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem, 0, len(m))
	for _, b := range branches {
		out = append(out, promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem{Branch: b, LastSuccessfulSha: m[b]})
	}
	return out
}

// allBranchesSucceededForCurrentShas reports whether every applicable environment
// already has CommitPhaseSuccess for its current SHA (per ReportOn), using the last
// persisted PromotionStrategyContext. Used by WebRequestCommitStatus reconciliation
// for the promotionstrategy-context polling+proposed fast path that skips HTTP.
func allBranchesSucceededForCurrentShas(
	applicableEnvs []promoterv1alpha1.Environment,
	lastReconciledCtxStatus *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus,
	currentShaPerBranch map[string]string,
) bool {
	if lastReconciledCtxStatus == nil || len(lastReconciledCtxStatus.LastSuccessfulShas) == 0 {
		return false
	}
	phaseByBranch := phasePerBranchMapFromSlice(lastReconciledCtxStatus.PhasePerBranch)
	shaByBranch := lastSuccessfulShasMapFromSlice(lastReconciledCtxStatus.LastSuccessfulShas)
	for _, env := range applicableEnvs {
		if phaseByBranch[env.Branch] != promoterv1alpha1.CommitPhaseSuccess {
			return false
		}
		if shaByBranch[env.Branch] != currentShaPerBranch[env.Branch] {
			return false
		}
	}
	return true
}
