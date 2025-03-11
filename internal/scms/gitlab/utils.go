package gitlab

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/go-logr/logr"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func logGitLabRatelimits(
	logger logr.Logger,
	scmProvider string,
	resp *gitlab.Response,
) {
	logger.Info("GitLab rate limits",
		"scmProvider", scmProvider,
		"limit", resp.Header.Get("Ratelimit-Limit"),
		"remaining", resp.Header.Get("Ratelimit-Remaining"),
		"reset", resp.Header.Get("Ratelimit-Reset"),
		"url", resp.Request.URL,
	)
}

func mapMergeRequestState(gl string) v1alpha1.PullRequestState {
	switch gl {
	case "opened":
		return v1alpha1.PullRequestOpen
	case "closed":
		return v1alpha1.PullRequestClosed
	default:
		return v1alpha1.PullRequestMerged
	}
}

func phaseToBuildState(phase v1alpha1.CommitStatusPhase) gitlab.BuildStateValue {
	switch phase {
	case v1alpha1.CommitPhaseSuccess:
		return gitlab.Success
	case v1alpha1.CommitPhasePending:
		return gitlab.Pending
	default:
		return gitlab.Failed
	}
}

func buildStateToPhase(buildState gitlab.BuildStateValue) v1alpha1.CommitStatusPhase {
	switch buildState {
	case gitlab.Success:
		return v1alpha1.CommitPhaseSuccess
	case gitlab.Pending:
		return v1alpha1.CommitPhasePending
	default:
		return v1alpha1.CommitPhaseFailure
	}
}
