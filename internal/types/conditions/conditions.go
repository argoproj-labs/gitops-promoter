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
	PullReqeustConditionType   string
	PullReqeustConditionReason string
)

// PromotionStrategyConditionType values
const (
	PromotionStrategyReady      PromotionStrategyConditionType   = "Ready"
	ReconciliationError         PromotionStrategyConditionReason = "ReconciliationError"
	ReconciliationSuccess       PromotionStrategyConditionReason = "ReconciliationSuccess"
	PsChangeTransferPolicyReady PromotionStrategyConditionType   = "ChangeTransferPoliciesReady"

	ChangeTransferPolicyReady                 ChangeTrasferPolicyConditionType   = "Ready"
	ChangeTransferPolicyReconciliationError   ChangeTrasferPolicyConditionReason = "ReconciliationError"
	ChangeTransferPolicyReconciliationSuccess ChangeTrasferPolicyConditionReason = "ReconciliationSuccess"
)
