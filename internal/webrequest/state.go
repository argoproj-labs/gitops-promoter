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

// LastReconciledState holds deserialized state from the previous reconcile, extracted from either
// WebRequestCommitStatusEnvironmentStatus (per-env path) or
// WebRequestCommitStatusPromotionStrategyContextStatus (context=promotionstrategy path).
type LastReconciledState struct {
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

// LastReconciledStateFromEnvironment extracts the previous reconcile's state from a per-environment
// status entry, deserializing trigger and response output JSON into maps.
func LastReconciledStateFromEnvironment(ctx context.Context, status *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus) LastReconciledState {
	if status == nil {
		return LastReconciledState{}
	}
	logger := log.FromContext(ctx)
	s := LastReconciledState{
		Phase:                  string(status.Phase),
		LastRequestTime:        status.LastRequestTime,
		LastResponseStatusCode: status.LastResponseStatusCode,
		ResponseOutput:         status.ResponseOutput,
		SuccessOutput:          status.SuccessOutput,
	}
	var err error
	s.TriggerData, err = UnmarshalJSONMap(status.TriggerOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal trigger data")
	}
	s.ResponseData, err = UnmarshalJSONMap(status.ResponseOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal response data")
	}
	s.SuccessData, err = UnmarshalJSONMap(status.SuccessOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal success data")
	}
	return s
}

// LastReconciledStateFromContext extracts the previous reconcile's state from the
// promotionstrategy-level context status, including per-branch phase overrides.
// Phase is computed as an aggregate of PhasePerBranch (success only if all branches succeeded,
// failure if any failed, pending otherwise).
func LastReconciledStateFromContext(ctx context.Context, status *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus) LastReconciledState {
	if status == nil {
		return LastReconciledState{}
	}
	logger := log.FromContext(ctx)
	phaseMap := PhasePerBranchMapFromSlice(status.PhasePerBranch)
	s := LastReconciledState{
		Phase:                  AggregatePhase(phaseMap),
		LastRequestTime:        status.LastRequestTime,
		LastResponseStatusCode: status.LastResponseStatusCode,
		ResponseOutput:         status.ResponseOutput,
		SuccessOutput:          status.SuccessOutput,
		PhasePerBranch:         phaseMap,
	}
	var err error
	s.TriggerData, err = UnmarshalJSONMap(status.TriggerOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal trigger data (context=promotionstrategy)")
	}
	s.ResponseData, err = UnmarshalJSONMap(status.ResponseOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal response data (context=promotionstrategy)")
	}
	s.SuccessData, err = UnmarshalJSONMap(status.SuccessOutput)
	if err != nil {
		logger.Error(err, "Failed to unmarshal success data (context=promotionstrategy)")
	}
	return s
}

// PhasePerBranchMapFromSlice converts a slice of per-branch phase items into a map keyed by branch.
// Returns nil when items is empty.
func PhasePerBranchMapFromSlice(items []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem) map[string]promoterv1alpha1.CommitStatusPhase {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]promoterv1alpha1.CommitStatusPhase, len(items))
	for _, it := range items {
		m[it.Branch] = it.Phase
	}
	return m
}

// PhasePerBranchSliceFromMap converts a branch→phase map into a sorted slice of per-branch phase items.
// Returns nil when the map is empty.
func PhasePerBranchSliceFromMap(m map[string]promoterv1alpha1.CommitStatusPhase) []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem {
	if len(m) == 0 {
		return nil
	}
	branches := make([]string, 0, len(m))
	for b := range m {
		branches = append(branches, b)
	}
	slices.Sort(branches)
	out := make([]promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem, 0, len(m))
	for _, b := range branches {
		out = append(out, promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem{Branch: b, Phase: m[b]})
	}
	return out
}

// LastSuccessfulShasMapFromSlice converts a slice of last-successful-SHA items into a map keyed by branch.
// Returns nil when items is empty.
func LastSuccessfulShasMapFromSlice(items []promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem) map[string]string {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]string, len(items))
	for _, it := range items {
		m[it.Branch] = it.LastSuccessfulSha
	}
	return m
}

// LastSuccessfulShasSliceFromMap converts a branch→SHA map into a sorted slice of last-successful-SHA items.
// Returns nil when the map is empty.
func LastSuccessfulShasSliceFromMap(m map[string]string) []promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem {
	if len(m) == 0 {
		return nil
	}
	branches := make([]string, 0, len(m))
	for b := range m {
		branches = append(branches, b)
	}
	slices.Sort(branches)
	out := make([]promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem, 0, len(m))
	for _, b := range branches {
		out = append(out, promoterv1alpha1.WebRequestCommitStatusLastSuccessfulShaItem{Branch: b, LastSuccessfulSha: m[b]})
	}
	return out
}
