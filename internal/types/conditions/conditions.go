package conditions

// PromotionStrategyConditionType defines conditions of a deployment.
type (
	CommonReason string
	CommonType   string
)

// PromotionStrategyConditionType values
const (
	ReconciliationError   CommonReason = "ReconciliationError"
	ReconciliationSuccess CommonReason = "ReconciliationSuccess"
	Ready                 CommonType   = "Ready"
)
