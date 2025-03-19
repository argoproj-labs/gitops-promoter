package v1alpha1

// CommitStatusLabel is the label used to identify commit statuses, this is used to look up commit statuses configured in the
// PromotionStrategy CR
const CommitStatusLabel = "promoter.argoproj.io/commit-status"

// CommitStatusCopyLabel is the label used to identify copied commit statuses (true or false)
const CommitStatusCopyLabel = "promoter.argoproj.io/commit-status-copy"

// PreviousEnvProposedCommitPrefixNameLabel is the prefix name for copied proposed commits
const PreviousEnvProposedCommitPrefixNameLabel = "promoter-previous-env-"

// CopiedCommitStatusFromLabel is the commit status that we were copied from
const CopiedCommitStatusFromLabel = "promoter.argoproj.io/commit-status-copy-from"

// CommmitStatusFromShaLabel is the commit status hydrated sha that we were copied from
const CommmitStatusFromShaLabel = "promoter.argoproj.io/commit-status-copy-from-sha"

// CommitStatusFromBranchLabel the branch/environment that we were copied from
const CommitStatusFromBranchLabel = "promoter.argoproj.io/commit-status-copy-from-branch"

// PromotionStrategyLabel the promotion strategy which the proposed commit is associated with
const PromotionStrategyLabel = "promoter.argoproj.io/promotion-strategy"

// EnvironmentLabel the environment branch for the proposed commit
const EnvironmentLabel = "promoter.argoproj.io/environment"

const ChangeTransferPolicyLabel = "promoter.argoproj.io/change-transfer-policy"

// PreviousEnvironmentCommitStatusKey the commit status key name used to indicate the previous environment health
const PreviousEnvironmentCommitStatusKey = "promoter-previous-environment"

// ReconcileAtAnnotation is the annotation used to indicate when the webhook triggered a reconcile
const ReconcileAtAnnotation = "promoter.argoproj.io/reconcile-at"

// CommitStatusPreviousEnvironmentStatusesAnnotation is the label used to identify commit statuses that make up the aggregated active commit status
const CommitStatusPreviousEnvironmentStatusesAnnotation = "promoter.argoproj.io/previous-environment-statuses"
