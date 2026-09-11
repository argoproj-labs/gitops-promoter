package scms

import (
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GetPullRequestResult holds the outcome of fetching a pull request by status.id on the SCM.
// Authoritative merge metadata (state, merged-at time, merged target SHA) comes from the provider Get response.
type GetPullRequestResult struct {
	MergedAt time.Time
	State    v1alpha1.PullRequestState
	// MergedTargetSHA is the SHA the target branch points at after the merge. It is a merge commit
	// only when the SCM created one; squash and fast-forward merges report the resulting commit.
	MergedTargetSHA string
	Found           bool
}
