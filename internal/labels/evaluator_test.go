package labels

import (
	"strings"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func TestEvaluatorEvaluate(t *testing.T) {
	t.Parallel()

	e := &Evaluator{}
	ctx := ExpressionContext{
		Status: promoterv1alpha1.ChangeTransferPolicyStatus{
			Proposed: promoterv1alpha1.CommitBranchState{
				CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
					{Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				},
			},
		},
	}

	got, err := e.Evaluate(
		`len(Status.Proposed.CommitStatuses) > 0 && all(Status.Proposed.CommitStatuses, {.Phase == 'success'}) ? ['lgtm', 'approved'] : []`,
		ctx,
	)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(got) != 2 || got[0] != "lgtm" || got[1] != "approved" {
		t.Fatalf("Evaluate() = %v, want [lgtm approved]", got)
	}
}

func TestEvaluatorEvaluateNilPromotionStrategy(t *testing.T) {
	t.Parallel()

	e := &Evaluator{}
	got, err := e.Evaluate(`PromotionStrategy == nil ? ['lgtm'] : ['other']`, ExpressionContext{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(got) != 1 || got[0] != "lgtm" {
		t.Fatalf("Evaluate() = %v, want [lgtm]", got)
	}
}

func TestEvaluatorEvaluateInvalidOutput(t *testing.T) {
	t.Parallel()

	e := &Evaluator{}
	_, err := e.Evaluate(`['`+strings.Repeat("a", 51)+`']`, ExpressionContext{})
	if err == nil {
		t.Fatal("expected error for label exceeding max length")
	}
}

func TestEvaluatorEvaluateWrongType(t *testing.T) {
	t.Parallel()

	e := &Evaluator{}
	_, err := e.Evaluate(`42`, ExpressionContext{})
	if err == nil {
		t.Fatal("expected error for non-slice result")
	}
}
