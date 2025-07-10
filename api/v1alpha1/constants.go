package v1alpha1

// CommitStatusLabel is the label used to identify commit statuses, this is used to look up commit statuses configured in the
// PromotionStrategy CR
const CommitStatusLabel = "promoter.argoproj.io/commit-status"

// PreviousEnvProposedCommitPrefixNameLabel is the prefix name for copied proposed commits
const PreviousEnvProposedCommitPrefixNameLabel = "promoter-previous-env-"

// PromotionStrategyLabel the promotion strategy which the proposed commit is associated with
const PromotionStrategyLabel = "promoter.argoproj.io/promotion-strategy"

// EnvironmentLabel the environment branch for the proposed commit
const EnvironmentLabel = "promoter.argoproj.io/environment"

// ChangeTransferPolicyLabel the change transfer policy which the proposed commit is associated with.
const ChangeTransferPolicyLabel = "promoter.argoproj.io/change-transfer-policy"

// PreviousEnvironmentCommitStatusKey the commit status key name used to indicate the previous environment health
const PreviousEnvironmentCommitStatusKey = "promoter-previous-environment"

// ReconcileAtAnnotation is the annotation used to indicate when the webhook triggered a reconcile
const ReconcileAtAnnotation = "promoter.argoproj.io/reconcile-at"

// CommitStatusPreviousEnvironmentStatusesAnnotation is the label used to identify commit statuses that make up the aggregated active commit status
const CommitStatusPreviousEnvironmentStatusesAnnotation = "promoter.argoproj.io/previous-environment-statuses"
