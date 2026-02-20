package constants

// If you add new things here, document them in docs/monitoring/events.md.

const (
	// ResolvedConflictReason is the reason for a resolved conflict event.
	ResolvedConflictReason = "ResolvedConflict"
	// ResolvedConflictMessage is the message for a resolved conflict event.
	ResolvedConflictMessage = "Merged %s into %s with 'ours' strategy to resolve conflicts"

	// ResolvedDivergenceReason is the reason for a resolved branch divergence event (e.g., after a squash merge on the SCM).
	ResolvedDivergenceReason = "ResolvedDivergence"
	// ResolvedDivergenceMessage is the message for a resolved branch divergence event.
	ResolvedDivergenceMessage = "Merged %s into %s with 'ours' strategy to resolve branch divergence (possible squash merge)"

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

	// PullRequestUpdatedReason indicates that a pull request has been updated.
	PullRequestUpdatedReason = "PullRequestUpdated"

	// CommitStatusSetReason indicates that a commit status has been set.
	CommitStatusSetReason = "CommitStatusSet"

	// OrphanedChangeTransferPolicyDeletedReason indicates that an orphaned ChangeTransferPolicy has been deleted.
	OrphanedChangeTransferPolicyDeletedReason = "OrphanedChangeTransferPolicyDeleted"
	// OrphanedChangeTransferPolicyDeletedMessage is the message for a deleted orphaned ChangeTransferPolicy.
	OrphanedChangeTransferPolicyDeletedMessage = "Deleted orphaned ChangeTransferPolicy %s"

	// OrphanedCommitStatusDeletedReason indicates that an orphaned CommitStatus has been deleted.
	OrphanedCommitStatusDeletedReason = "OrphanedCommitStatusDeleted"
	// OrphanedCommitStatusDeletedMessage is the message for a deleted orphaned CommitStatus.
	OrphanedCommitStatusDeletedMessage = "Deleted orphaned CommitStatus %s"
)
