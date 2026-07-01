package v1alpha1

import "testing"

// Changing these constants is a breaking change for users who reference them
// in branch protection rules, rulesets, or automation. If a test below fails
// after your change, update documentation and migration guides before merging.

func TestPreviousEnvironmentCommitStatusKey(t *testing.T) {
	if PreviousEnvironmentCommitStatusKey != "promoter-previous-environment" {
		t.Errorf("PreviousEnvironmentCommitStatusKey changed to %q — this is a public API used as the SCM commit status context (e.g. GitHub check run name). Users may reference this value in branch protection rules. Update documentation and migration guides before merging.", PreviousEnvironmentCommitStatusKey)
	}
}
