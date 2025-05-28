package constants

const (
	ResolvedConflictReason  = "ResolvedConflict"
	ResolvedConflictMessage = "Merged %s into %s with 'ours' strategy to resolve conflicts"

	TooManyMatchingShaReason          = "TooManyMatchingSha"
	TooManyMatchingShaActiveMessage   = "There are to many matching SHAs for the active commit status"
	TooManyMatchingShaProposedMessage = "There are to many matching SHAs for the proposed commit status"

	PullRequestCreatedReason  = "PullRequestCreated"
	PullRequestCreatedMessage = "Pull Request %s created"

	PullRequestMergedReason  = "PullRequestMerged"
	PullRequestMergedMessage = "Pull Request %s merged"
)
