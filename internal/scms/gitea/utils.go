package gitea

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func commitPhaseToGiteaStatusState(commitPhase promoterv1alpha1.CommitStatusPhase) (gitea.StatusState, error) {
	switch commitPhase {
	case promoterv1alpha1.CommitPhaseFailure:
		return gitea.StatusFailure, nil
	case promoterv1alpha1.CommitPhasePending:
		return gitea.StatusPending, nil
	case promoterv1alpha1.CommitPhaseSuccess:
		return gitea.StatusSuccess, nil
	default:
		return gitea.StatusError, fmt.Errorf("cannot map promoter commit phase '%v' to a gitea commit status", commitPhase)
	}
}
