package webhooks_test

// Common test constants used across multiple test files
const (
	invalidJSON       = `{invalid`
	simplePushPayload = `{
				"ref": "refs/heads/main",
				"before": "abc123",
				"after": "def456"
			}`
)
