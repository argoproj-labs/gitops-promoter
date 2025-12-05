package constants

// Keep sorted alphabetically
const (
	// TrailerCommitStatusActivePrefix is the prefix for trailers indicating active commit statuses.
	TrailerCommitStatusActivePrefix = "Commit-status-active-"
	// TrailerCommitStatusProposedPrefix is the prefix for trailers indicating proposed commit statuses.
	TrailerCommitStatusProposedPrefix = "Commit-status-proposed-"
	// TrailerPullRequestCreationTime is the trailer key used to store the creation time of the pull request.
	TrailerPullRequestCreationTime = "Pull-request-creation-time"
	// TrailerPullRequestMergeTime is the trailer key used to store the merge time of the pull request.
	TrailerPullRequestMergeTime = "Pull-request-merge-time"
	// TrailerPullRequestID is the trailer key used to store the pull request ID.
	TrailerPullRequestID = "Pull-request-id"
	// TrailerPullRequestSourceBranch is the trailer key used to store the source branch of the pull request.
	TrailerPullRequestSourceBranch = "Pull-request-source-branch"
	// TrailerPullRequestTargetBranch is the trailer key used to store the target branch of the pull request.
	TrailerPullRequestTargetBranch = "Pull-request-target-branch"
	// TrailerPullRequestUrl is the trailer key used to store the URL of the pull request.
	TrailerPullRequestUrl = "Pull-request-url"
	// TrailerShaDryActive is the trailer key used to store the SHA of the active dry commit.
	TrailerShaDryActive = "Sha-dry-active"
	// TrailerShaDryProposed is the trailer key used to store the SHA of the proposed dry commit.
	TrailerShaDryProposed = "Sha-dry-proposed"
	// TrailerShaHydratedActive is the trailer key used to store the SHA of the active hydrated commit.
	TrailerShaHydratedActive = "Sha-hydrated-active"
	// TrailerShaHydratedProposed is the trailer key used to store the SHA of the proposed hydrated commit.
	TrailerShaHydratedProposed = "Sha-hydrated-proposed"
)
