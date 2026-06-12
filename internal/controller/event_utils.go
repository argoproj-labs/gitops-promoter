package controller

import (
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// emitCommitStatusPhaseChangedEvent emits a CommitStatusPhaseChanged event on a commit status gate
// resource (TimedCommitStatus, GitCommitStatus, WebRequestCommitStatus, ArgoCDCommitStatus) when the
// phase it computed for an environment differs from the phase recorded by the previous reconcile.
// It is a no-op when the phase is unchanged, keeping the event transition-only. A failure phase is
// emitted as a Warning so it stands out in `kubectl get events`.
func emitCommitStatusPhaseChangedEvent(recorder events.EventRecorder, obj client.Object, key, branch, previousPhase, phase string) {
	if previousPhase == phase {
		return
	}
	if previousPhase == "" {
		previousPhase = constants.CommitStatusNoPreviousPhase
	}
	eventType := "Normal"
	if phase == string(promoterv1alpha1.CommitPhaseFailure) {
		eventType = "Warning"
	}
	recorder.Eventf(obj, nil, eventType, constants.CommitStatusPhaseChangedReason, "EvaluatingCommitStatus",
		constants.CommitStatusPhaseChangedMessage, key, branch, previousPhase, phase)
}
