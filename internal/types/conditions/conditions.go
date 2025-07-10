package conditions

// PromotionStrategyConditionType defines conditions of a deployment.
type (
	// CommonReason is a reason for a condition.
	CommonReason string
	// CommonType is a type of condition.
	CommonType string
)

// PromotionStrategyConditionType values
const (
	// ReconciliationError is the condition type for an error during reconciliation.
	ReconciliationError CommonReason = "ReconciliationError"
	// ReconciliationSuccess is the condition type for a successful reconciliation.
	ReconciliationSuccess CommonReason = "ReconciliationSuccess"
	// Ready is the condition type for a resource that is ready.
	Ready CommonType = "Ready"
)
