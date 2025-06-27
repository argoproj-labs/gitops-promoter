package conditions

// PromotionStrategyConditionType defines conditions of a deployment.
type (
	PromotionStrategyConditionType   string
	PromotionStrategyConditionReason string
)

type (
	ChangeTrasferPolicyConditionType   string
	ChangeTrasferPolicyConditionReason string
)

type (
	CommitStatusConditionType   string
	CommitStatusConditionReason string
)

type (
	PullRequestConditionType   string
	PullRequestConditionReason string
)

// PromotionStrategyConditionType values
const (
	ReconciliationError   PromotionStrategyConditionReason = "ReconciliationError"
	ReconciliationSuccess PromotionStrategyConditionReason = "ReconciliationSuccess"

	PromotionStrategyReady PromotionStrategyConditionType = "Ready"

	ChangeTransferPolicyReady ChangeTrasferPolicyConditionType = "Ready"

	PullRequestReady PullRequestConditionType = "Ready"

	CommitStatusReady CommitStatusConditionType = "Ready"
)
