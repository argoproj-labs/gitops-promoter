package gitlab

import (
	"errors"
	"net/http"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/go-logr/logr"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	corev1 "k8s.io/api/core/v1"
)

func logGitLabRateLimitsIfAvailable(
	logger logr.Logger,
	scmProvider string,
	resp *gitlab.Response,
) {
	limit := resp.Header.Get("Ratelimit-Limit")
	remaining := resp.Header.Get("Ratelimit-Remaining")
	reset := resp.Header.Get("Ratelimit-Reset")

	if limit != "" || remaining != "" || reset != "" {
		logger.Info("GitLab rate limits",
			"scmProvider", scmProvider,
			"limit", limit,
			"remaining", remaining,
			"reset", reset,
			"url", resp.Request.URL.String(),
		)
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

// ApplyHTTPAuth applies GitLab authentication to the HTTP request using a PRIVATE-TOKEN header.
func ApplyHTTPAuth(secret corev1.Secret, req *http.Request) error {
	token := string(secret.Data["token"])
	if token == "" {
		return errors.New("non-empty token required in secret for GitLab SCM auth")
	}
	req.Header.Set("Private-Token", token)
	return nil
}
