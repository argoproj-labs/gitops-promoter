package forgejo

import (
	"fmt"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func buildStateToPhase(buildState forgejo.StatusState) (promoterv1alpha1.CommitStatusPhase, error) {
	switch buildState {
	case forgejo.StatusSuccess:
		return promoterv1alpha1.CommitPhaseSuccess, nil
	case forgejo.StatusPending:
		return promoterv1alpha1.CommitPhasePending, nil
	case forgejo.StatusWarning:
		return promoterv1alpha1.CommitPhasePending, nil
	case forgejo.StatusError:
		return promoterv1alpha1.CommitPhaseFailure, nil
	case forgejo.StatusFailure:
		return promoterv1alpha1.CommitPhaseFailure, nil
	default:
		return promoterv1alpha1.CommitPhaseFailure, fmt.Errorf("unknown status state: %v", buildState)
	}
}

func mapPullRequestState(pr forgejo.PullRequest) (promoterv1alpha1.PullRequestState, error) {
	if pr.HasMerged {
		return promoterv1alpha1.PullRequestMerged, nil
	}

	switch pr.State {
	case forgejo.StateOpen:
		return promoterv1alpha1.PullRequestOpen, nil
	case forgejo.StateClosed:
		return promoterv1alpha1.PullRequestClosed, nil
	default:
		return promoterv1alpha1.PullRequestClosed, fmt.Errorf("unknown pull request state: %v", pr.State)
	}
}
