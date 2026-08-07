package constants

// If you add new things here, document them in docs/monitoring/events.md.

const (
	// ResolvedConflictReason is the reason for a resolved conflict event.
	ResolvedConflictReason = "ResolvedConflict"
	// ResolvedConflictMessage is the message for a resolved conflict event.
	ResolvedConflictMessage = "Merged %s into %s with 'ours' strategy to resolve conflicts"

	// TooManyMatchingShaReason indicates that there are too many matching SHAs for the active or proposed commit status.
	TooManyMatchingShaReason = "TooManyMatchingSha"
	// TooManyMatchingShaActiveMessage is the message for too many matching SHAs for the active commit status.
	TooManyMatchingShaActiveMessage = "There are too many matching SHAs for the active commit status"
	// TooManyMatchingShaProposedMessage is the message for too many matching SHAs for the proposed commit status.
	TooManyMatchingShaProposedMessage = "There are too many matching SHAs for the proposed commit status"

	// MissingProposedHydratorMetadataReason indicates that the proposed branch has hydration output but
	// promoter could not read a dry SHA from hydrator.metadata at activePath.
	MissingProposedHydratorMetadataReason = "MissingProposedHydratorMetadata"
	// MissingProposedHydratorMetadataMessage is the message for missing proposed hydrator metadata.
	MissingProposedHydratorMetadataMessage = "Proposed branch %q has hydrated commit %s but no dry SHA from %q"

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

	// PromotionHistoryNoteFailedReason indicates that writing the promotion-history git note failed.
	PromotionHistoryNoteFailedReason = "PromotionHistoryNoteFailed"
	// PromotionHistoryNoteFailedMessage is the message for a failed promotion-history git note write.
	PromotionHistoryNoteFailedMessage = "Failed to write promotion history note for Pull Request %s: %v"

	// PullRequestClosedReason indicates that a pull request has been closed without being merged.
	PullRequestClosedReason = "PullRequestClosed"
	// PullRequestClosedMessage is the message for a closed pull request.
	PullRequestClosedMessage = "Pull Request %s closed"

	// PullRequestExternallyMergedOrClosedReason indicates that a pull request was merged or closed directly on the SCM, outside the controller.
	PullRequestExternallyMergedOrClosedReason = "PullRequestExternallyMergedOrClosed"
	// PullRequestExternallyMergedOrClosedMessage is the message for an externally merged or closed pull request.
	PullRequestExternallyMergedOrClosedMessage = "Pull Request %s (provider ID %s) was merged or closed directly on the SCM"

	// PullRequestCreateFailedReason indicates that creating a pull request on the SCM failed.
	PullRequestCreateFailedReason = "PullRequestCreateFailed"
	// PullRequestCreateFailedMessage is the message for a failed pull request creation.
	PullRequestCreateFailedMessage = "Failed to create pull request %s on SCM: %v"

	// PullRequestMergeFailedReason indicates that merging a pull request on the SCM failed.
	PullRequestMergeFailedReason = "PullRequestMergeFailed"
	// PullRequestMergeFailedMessage is the message for a failed pull request merge.
	PullRequestMergeFailedMessage = "Failed to merge pull request %s on SCM: %v"

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

	// PromotionStartedReason indicates that a new dry sha was detected and its promotion to an environment has started.
	PromotionStartedReason = "PromotionStarted"
	// PromotionStartedMessage is the message for a started promotion.
	PromotionStartedMessage = "Promotion of dry sha %s to environment branch %s started (current active dry sha %s)"

	// PromotionBlockedReason indicates that a pending promotion is blocked by a proposed commit status that is not in the success phase.
	PromotionBlockedReason = "PromotionBlocked"
	// PromotionBlockedMessage is the message for a blocked promotion.
	PromotionBlockedMessage = "Promotion of dry sha %s to environment branch %s is blocked: commit status %q is %s"

	// PromotionCompletedReason indicates that the active branch of an environment advanced to a new dry sha.
	PromotionCompletedReason = "PromotionCompleted"
	// PromotionCompletedMessage is the message for a completed promotion.
	PromotionCompletedMessage = "Environment branch %s promoted to dry sha %s (previously %s)"

	// CommitStatusPhaseChangedReason indicates that the phase computed by a commit status gate
	// (TimedCommitStatus, GitCommitStatus, WebRequestCommitStatus, ArgoCDCommitStatus) changed for an environment.
	CommitStatusPhaseChangedReason = "CommitStatusPhaseChanged"
	// CommitStatusPhaseChangedMessage is the message for a changed commit status phase.
	CommitStatusPhaseChangedMessage = "Commit status %q for environment branch %s changed from %s to %s"
	// CommitStatusNoPreviousPhase is used in CommitStatusPhaseChanged messages when the gate had no previously recorded phase.
	CommitStatusNoPreviousPhase = "<none>"

	// WebRequestFailedReason indicates that the HTTP request made by a WebRequestCommitStatus failed.
	WebRequestFailedReason = "WebRequestFailed"
	// WebRequestFailedMessage is the message for a failed web request.
	WebRequestFailedMessage = "HTTP request for commit status %q failed: %v"
)
