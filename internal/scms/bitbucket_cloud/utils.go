package bitbucket_cloud

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ktrysmt/go-bitbucket"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// BitbucketBaseURL is the base URL for Bitbucket Cloud
const BitbucketBaseURL = "https://bitbucket.org"

// parseErrorStatusCode extracts the HTTP status code from a Bitbucket API error.
// The Bitbucket client doesn't return HTTP response metadata, so we parse
// the error message to determine status codes (e.g., "400 Bad Request").
// Returns the provided defaultStatusCode if the error is not a Bitbucket error.
func parseErrorStatusCode(err error, defaultStatusCode int) int {
	if err == nil {
		return defaultStatusCode
	}

	var bbErr *bitbucket.UnexpectedResponseStatusError
	if !errors.As(err, &bbErr) {
		return http.StatusInternalServerError
	}

	errMsg := bbErr.Error()
	switch {
	case strings.HasPrefix(errMsg, "400"):
		return http.StatusBadRequest
	case strings.HasPrefix(errMsg, "401"):
		return http.StatusUnauthorized
	case strings.HasPrefix(errMsg, "404"):
		return http.StatusNotFound
	case strings.HasPrefix(errMsg, "409"):
		return http.StatusConflict
	case strings.HasPrefix(errMsg, "555"):
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// phaseToBuildState converts a CommitStatusPhase to a Bitbucket Cloud build state.
// Bitbucket Cloud states: SUCCESSFUL, FAILED, INPROGRESS, STOPPED
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

// buildStateToPhase converts a Bitbucket Cloud build state to a CommitStatusPhase.
// Bitbucket Cloud states: SUCCESSFUL, FAILED, INPROGRESS, STOPPED
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

func createCommitURL(repo *v1alpha1.GitRepository, sha string) string {
	return fmt.Sprintf("%s/%s/%s/commits/%s",
		BitbucketBaseURL,
		repo.Spec.BitbucketCloud.Owner,
		repo.Spec.BitbucketCloud.Name,
		sha,
	)
}
