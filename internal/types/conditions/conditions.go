package conditions

// PromotionStrategyConditionType defines conditions of a deployment.
type (
	// CommonType is a type of condition.
	CommonType string

	// CommonReason is a reason for a condition.
	CommonReason string
)

// PromotionStrategyConditionType values
const (
	// Ready is the condition type for a resource that is ready.
	Ready CommonType = "Ready"
)

// Reasons that apply to all CRDs.
const (
	// ReconciliationError is the condition type for an error during reconciliation.
	ReconciliationError CommonReason = "ReconciliationError"
	// ReconciliationSuccess is the condition type for a successful reconciliation.
	ReconciliationSuccess CommonReason = "ReconciliationSuccess"
)

// Reasons that apply to ArgoCDCommitStatus.
const (
	// CommitStatusesNotReady is the condition type for commit statuses not being ready.
	CommitStatusesNotReady CommonReason = "CommitStatusesNotReady"
)

// Reasons that apply to ChangeTransferPolicy.
const (
	// PullRequestNotReady is the condition type for a pull request not being ready.
	PullRequestNotReady CommonReason = "PullRequestNotReady"
)

// Reasons that apply to PromotionStrategy.
const (
	// ChangeTransferPolicyNotReady is the condition type for a change transfer policy not being ready.
	ChangeTransferPolicyNotReady CommonReason = "ChangeTransferPolicyNotReady"
	// PreviousEnvironmentCommitStatusNotReady is the condition type for a previous environment commit status not being ready.
	PreviousEnvironmentCommitStatusNotReady CommonReason = "PreviousEnvironmentCommitStatusNotReady"
)
