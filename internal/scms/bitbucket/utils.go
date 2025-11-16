package bitbucket

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// phaseToBuildState converts a CommitStatusPhase to a Bitbucket build state.
// Bitbucket states: SUCCESSFUL, FAILED, INPROGRESS, STOPPED
// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-commit-statuses/#api-repositories-workspace-repo-slug-commit-commit-statuses-build-post-request-body
func phaseToBuildState(phase v1alpha1.CommitStatusPhase) string {
	switch phase {
	case v1alpha1.CommitPhaseSuccess:
		return "SUCCESSFUL"
	case v1alpha1.CommitPhasePending:
		return "INPROGRESS"
	default:
		return "FAILED"
	}
}

// buildStateToPhase converts a Bitbucket build state to a CommitStatusPhase.
// Bitbucket states: SUCCESSFUL, FAILED, INPROGRESS, STOPPED
// https://developer.atlassian.com/cloud/bitbucket/rest/api-group-commit-statuses/#api-repositories-workspace-repo-slug-commit-commit-statuses-build-post-request-body
func buildStateToPhase(buildState string) v1alpha1.CommitStatusPhase {
	switch buildState {
	case "SUCCESSFUL":
		return v1alpha1.CommitPhaseSuccess
	case "INPROGRESS":
		return v1alpha1.CommitPhasePending
	default:
		return v1alpha1.CommitPhaseFailure
	}
}
