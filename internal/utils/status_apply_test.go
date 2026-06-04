package utils

import (
	"encoding/json"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"k8s.io/utils/ptr"
)

func TestJSONRoundTripPreservesEmptyPullRequestState(t *testing.T) {
	t.Parallel()

	src := promoterv1alpha1.ChangeTransferPolicyStatus{
		PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
			ID:                       "42",
			State:                    "",
			ExternallyMergedOrClosed: ptr.To(true),
		},
	}
	dst := acv1alpha1.ChangeTransferPolicyStatus()
	if err := jsonRoundTrip(&src, dst); err != nil {
		t.Fatalf("jsonRoundTrip: %v", err)
	}
	if dst.PullRequest == nil || dst.PullRequest.State == nil {
		t.Fatal("expected pullRequest.state to be set in apply configuration")
	}
	if *dst.PullRequest.State != "" {
		t.Fatalf("expected empty pullRequest.state, got %q", *dst.PullRequest.State)
	}

	data, err := json.Marshal(dst.PullRequest)
	if err != nil {
		t.Fatalf("marshal apply configuration: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid JSON: %s", data)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	state, ok := parsed["state"]
	if !ok {
		t.Fatalf("expected state key in JSON %s", data)
	}
	if state != "" {
		t.Fatalf("expected state=\"\" in JSON, got %#v", state)
	}
}
