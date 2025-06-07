package forgejo

import (
	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func buildStateToPhase(buildState forgejo.StatusState) v1alpha1.CommitStatusPhase {
	switch buildState {
	case forgejo.StatusSuccess:
		return v1alpha1.CommitPhaseSuccess
	case forgejo.StatusPending:
		return v1alpha1.CommitPhasePending
	default:
		return v1alpha1.CommitPhaseFailure
	}
}

func mapMergeRequestState(state string) v1alpha1.PullRequestState {
	switch state {
	case "opened":
		return v1alpha1.PullRequestOpen
	case "closed":
		return v1alpha1.PullRequestClosed
	default:
		return v1alpha1.PullRequestMerged
	}
}
