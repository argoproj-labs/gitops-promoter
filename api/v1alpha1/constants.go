package v1alpha1

// CommitStatusLabel is the label used to identify commit statuses, this is used to look up commit statuses configured in the
// PromotionStrategy CR
const CommitStatusLabel = "promoter.argoproj.io/commit-status"

// CommitStatusLabelCopy is the label used to identify copied commit statuses
const CommitStatusLabelCopy = "promoter.argoproj.io/commit-status-copy"

// CopiedProposedCommitPrefixName is the prefix name for copied proposed commits
const CopiedProposedCommitPrefixName = "proposed-"

// CopiedCommitStatusFrom is the commit status that we where copied from
const CopiedCommitStatusFrom = "promoter.argoproj.io/commit-status-copy-from"

// CopiedCommmitStatusFromSha is the commit status hydrated sha that we were copied from
const CopiedCommmitStatusFromSha = "promoter.argoproj.io/commit-status-copy-from-sha"

// CopiedCommitStatusBranch the branch/environment that we were copied from
const CopiedCommitStatusBranch = "promoter.argoproj.io/commit-status-copy-from-branch"

// ProposedCommitPromotionStrategy the promotion strategy which the proposed commit is associated with
const ProposedCommitPromotionStrategy = "promoter.argoproj.io/promotion-strategy"

// ProposedCommitEnvironment the environment branch for the proposed commit
const ProposedCommitEnvironment = "promoter.argoproj.io/environment"

const ProposedCommitLabel = "promoter.argoproj.io/proposed-commit"
