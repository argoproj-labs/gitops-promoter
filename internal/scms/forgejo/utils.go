package forgejo

import (
	"fmt"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func commitPhaseToForgejoStatusState(commitPhase promoterv1alpha1.CommitStatusPhase) (forgejo.StatusState, error) {
	switch commitPhase {
	case promoterv1alpha1.CommitPhaseFailure:
		return forgejo.StatusFailure, nil
	case promoterv1alpha1.CommitPhasePending:
		return forgejo.StatusPending, nil
	case promoterv1alpha1.CommitPhaseSuccess:
		return forgejo.StatusSuccess, nil
	default:
		return forgejo.StatusError, fmt.Errorf("unknown commit phase: %v", commitPhase)
	}
}

func forgejoPullRequestStateToPullRequestState(pr forgejo.PullRequest) (promoterv1alpha1.PullRequestState, error) {
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
