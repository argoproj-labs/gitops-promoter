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
		return forgejo.StatusError, fmt.Errorf("cannot map promoter commit phase '%v' to a forgejo commit status", commitPhase)
	}
}
