package v1alpha1

// CommitStatusLabel is the label used to identify commit statuses, this is used to look up commit statuses configured in the
// PromotionStrategy CR
const CommitStatusLabel = "promoter.argoproj.io/commit-status"

// CommitStatusLabelCopy is the label used to identify copied commit statuses
const CommitStatusLabelCopy = "promoter.argoproj.io/commit-status-copy"

// CopiedProposedCommitPrefixName is the prefix name for copied proposed commits
const CopiedProposedCommitPrefixName = "proposed-"
