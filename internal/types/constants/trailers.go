package constants

// Keep sorted alphabetically
const (
	// TrailerCommitStatusActivePrefix is the prefix for trailers indicating active commit statuses.
	TrailerCommitStatusActivePrefix = "CommitStatus-Active-"
	// TrailerCommitStatusProposedPrefix is the prefix for trailers indicating proposed commit statuses.
	TrailerCommitStatusProposedPrefix = "CommitStatus-Proposed-"
	// TrailerPullRequestID is the trailer key used to store the pull request ID.
	TrailerPullRequestID = "PullRequest-ID"
	// TrailerPullRequestSourceBranch is the trailer key used to store the source branch of the pull request.
	TrailerPullRequestSourceBranch = "PullRequest-SourceBranch"
	// TrailerPullRequestTargetBranch is the trailer key used to store the target branch of the pull request.
	TrailerPullRequestTargetBranch = "PullRequest-TargetBranch"
	// TrailerPullRequestCreationTime is the trailer key used to store the creation time of the pull request.
	TrailerPullRequestCreationTime = "PullRequest-CreationTime"
	// TrailerPullRequestUrl is the trailer key used to store the URL of the pull request.
	TrailerPullRequestUrl = "PullRequest-Url"
	// TrailerNoOp is the trailer key use to store the No-Op marker.
	TrailerNoOp = "No-Op"
	// TrailerShaDryActive is the trailer key used to store the SHA of the active dry commit.
	TrailerShaDryActive = "Sha-Dry-Active"
	// TrailerShaDryProposed is the trailer key used to store the SHA of the proposed dry commit.
	TrailerShaDryProposed = "Sha-Dry-Proposed"
	// TrailerShaHydratedActive is the trailer key used to store the SHA of the active hydrated commit.
	TrailerShaHydratedActive = "Sha-Hydrated-Active"
	// TrailerShaHydratedProposed is the trailer key used to store the SHA of the proposed hydrated commit.
	TrailerShaHydratedProposed = "Sha-Hydrated-Proposed"
)
