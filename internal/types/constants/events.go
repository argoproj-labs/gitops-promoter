package constants

const (
	// ResolvedConflictReason is the reason for a resolved conflict event.
	ResolvedConflictReason = "ResolvedConflict"
	// ResolvedConflictMessage is the message for a resolved conflict event.
	ResolvedConflictMessage = "Merged %s into %s with 'ours' strategy to resolve conflicts"

	// TooManyMatchingShaReason indicates that there are too many matching SHAs for the active or proposed commit status.
	TooManyMatchingShaReason = "TooManyMatchingSha"
	// TooManyMatchingShaActiveMessage is the message for too many matching SHAs for the active commit status.
	TooManyMatchingShaActiveMessage = "There are to many matching SHAs for the active commit status"
	// TooManyMatchingShaProposedMessage is the message for too many matching SHAs for the proposed commit status.
	TooManyMatchingShaProposedMessage = "There are to many matching SHAs for the proposed commit status"

	// PullRequestCreatedReason indicates that a pull request has been created.
	PullRequestCreatedReason = "PullRequestCreated"
	// PullRequestCreatedMessage is the message for a created pull request.
	PullRequestCreatedMessage = "Pull Request %s created"

	// PullRequestMergedReason indicates that a pull request has been merged.
	PullRequestMergedReason = "PullRequestMerged"
	// PullRequestMergedMessage is the message for a merged pull request.
	PullRequestMergedMessage = "Pull Request %s merged"
)
