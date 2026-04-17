package utils

import (
	"encoding/json"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConditionsToApply converts a slice of metav1.Condition to its apply-config equivalent.
// Returned entries are suitable for passing to the generated WithConditions methods on
// status apply configurations.
func ConditionsToApply(conds []metav1.Condition) []*acmetav1.ConditionApplyConfiguration {
	if len(conds) == 0 {
		return nil
	}
	out := make([]*acmetav1.ConditionApplyConfiguration, 0, len(conds))
	for i := range conds {
		c := conds[i]
		cfg := acmetav1.Condition().
			WithType(c.Type).
			WithStatus(c.Status).
			WithObservedGeneration(c.ObservedGeneration).
			WithReason(c.Reason).
			WithMessage(c.Message)
		if !c.LastTransitionTime.IsZero() {
			cfg = cfg.WithLastTransitionTime(c.LastTransitionTime)
		}
		out = append(out, cfg)
	}
	return out
}

// statusApplyConfig returns an apply configuration for the object's status subresource.
//
// When conditionsOnly is false, every field of the in-memory status is populated via a
// JSON round-trip into the typed apply configuration. This is used for the normal path
// where the controller owns the entire status.
//
// When conditionsOnly is true, only status.conditions is populated. This is used as a
// fallback when the full-status apply is rejected by server-side schema/CEL validation:
// SSA on /status is atomic, so a rejection loses the Ready condition that describes the
// failure. Re-applying under the same field owner with only conditions ensures the
// Ready=False condition reaches users, and a subsequent successful full apply naturally
// re-owns conditions along with every other status field (no managedFields drift).
//
// Dispatch is via type switch because api/v1alpha1 cannot import
// applyconfiguration/api/v1alpha1 (that package already imports api/v1alpha1).
func statusApplyConfig(obj client.Object, conditionsOnly bool) (any, error) {
	switch o := obj.(type) {
	case *promoterv1alpha1.ChangeTransferPolicy:
		return changeTransferPolicyStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.PromotionStrategy:
		return promotionStrategyStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.CommitStatus:
		return commitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.WebRequestCommitStatus:
		return webRequestCommitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.TimedCommitStatus:
		return timedCommitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.GitCommitStatus:
		return gitCommitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.ArgoCDCommitStatus:
		return argoCDCommitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.PullRequest:
		return pullRequestStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.GitRepository:
		return gitRepositoryStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.ScmProvider:
		return scmProviderStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.ClusterScmProvider:
		return clusterScmProviderStatusApply(o, conditionsOnly)
	default:
		return nil, fmt.Errorf("unsupported object type for status SSA: %T", obj)
	}
}

func changeTransferPolicyStatusApply(o *promoterv1alpha1.ChangeTransferPolicy, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ChangeTransferPolicyStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ChangeTransferPolicy(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func promotionStrategyStatusApply(o *promoterv1alpha1.PromotionStrategy, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.PromotionStrategyStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.PromotionStrategy(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func commitStatusStatusApply(o *promoterv1alpha1.CommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.CommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.CommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func webRequestCommitStatusStatusApply(o *promoterv1alpha1.WebRequestCommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.WebRequestCommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.WebRequestCommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func timedCommitStatusStatusApply(o *promoterv1alpha1.TimedCommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.TimedCommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.TimedCommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func gitCommitStatusStatusApply(o *promoterv1alpha1.GitCommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.GitCommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.GitCommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func argoCDCommitStatusStatusApply(o *promoterv1alpha1.ArgoCDCommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ArgoCDCommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ArgoCDCommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func pullRequestStatusApply(o *promoterv1alpha1.PullRequest, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.PullRequestStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.PullRequest(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func gitRepositoryStatusApply(o *promoterv1alpha1.GitRepository, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.GitRepositoryStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.GitRepository(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func scmProviderStatusApply(o *promoterv1alpha1.ScmProvider, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ScmProviderStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ScmProvider(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func clusterScmProviderStatusApply(o *promoterv1alpha1.ClusterScmProvider, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ScmProviderStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ClusterScmProvider(o.Name, "").WithStatus(statusAC), nil
}

// jsonRoundTrip copies all JSON-tagged fields from src into dst by marshaling src and
// unmarshaling into dst. This works for status types whose apply configuration mirrors
// the original type's JSON shape (which is true for all generated apply configs).
func jsonRoundTrip(src, dst any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal into apply configuration: %w", err)
	}
	return nil
}
