package utils

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

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
// When conditionsOnly is true, ONLY status.conditions is populated. Notably,
// status.observedGeneration is deliberately left out: the caller uses a distinct
// FieldOwner for the fallback apply so the other status fields (proposed, active,
// history, pullRequest, …) owned by the main manager are not wiped, and keeping
// status.observedGeneration pinned to the last successful reconcile serves as the
// canonical "status is stale" signal. The Ready condition's own observedGeneration
// field records the generation that was attempted, so consumers can see both: the
// stored status is from generation N, but reconciliation of generation N+k failed
// with <message>.
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
	case *promoterv1alpha1.ScheduledCommitStatus:
		return scheduledCommitStatusStatusApply(o, conditionsOnly)
	case *promoterv1alpha1.ControllerConfiguration:
		return controllerConfigurationStatusApply(o, conditionsOnly)
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
	return acv1alpha1.ClusterScmProvider(o.Name).WithStatus(statusAC), nil
}

func scheduledCommitStatusStatusApply(o *promoterv1alpha1.ScheduledCommitStatus, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ScheduledCommitStatusStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ScheduledCommitStatus(o.Name, o.Namespace).WithStatus(statusAC), nil
}

func controllerConfigurationStatusApply(o *promoterv1alpha1.ControllerConfiguration, conditionsOnly bool) (any, error) {
	statusAC := acv1alpha1.ControllerConfigurationStatus()
	if conditionsOnly {
		statusAC = statusAC.WithConditions(ConditionsToApply(o.Status.Conditions)...)
	} else if err := jsonRoundTrip(&o.Status, statusAC); err != nil {
		return nil, err
	}
	return acv1alpha1.ControllerConfiguration(o.Name, o.Namespace).WithStatus(statusAC), nil
}

// jsonRoundTrip copies all JSON-tagged fields from src into dst by marshaling src and
// unmarshaling into dst. This works for status types whose apply configuration mirrors
// the original type's JSON shape (which is true for all generated apply configs).
//
// Optional sub-objects that marshal empty are dropped from the intermediate JSON before
// it is loaded into the apply configuration. Status types embed value-typed sub-objects
// (for example CommitBranchState.Dry) whose zero value marshals as "{}", or as an object
// whose only members are null (a zero metav1.Time marshals as null). Applying "{}" for a
// field whose children this manager owned on the previous apply makes the apiserver
// remove those children and store the now-empty field as null (structured-merge-diff
// RemoveItems semantics, restored in v6.3.3/v6.4.2 and shipped in Kubernetes 1.36.3+ and
// 1.37+), which the CRD's non-nullable object schema then rejects. Omitting the field
// from the apply configuration lets server-side apply unset it cleanly instead, and the
// Go zero value round-trips identically on read.
//
// Only fields tagged omitempty are dropped. controller-gen marks exactly those fields
// optional in the CRD schema, so a required object (for example
// PromotionStrategy status.environments[].active) keeps applying as "{}" when empty.
func jsonRoundTrip(src, dst any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return fmt.Errorf("unmarshal status: %w", err)
	}
	data, err = json.Marshal(pruneEmptyOptionalObjects(reflect.ValueOf(src), generic))
	if err != nil {
		return fmt.Errorf("marshal pruned status: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal into apply configuration: %w", err)
	}
	return nil
}

// pruneEmptyOptionalObjects walks the Go value rv alongside its generic JSON encoding j
// and removes object members that correspond to struct fields tagged omitempty whose
// encoded value is null or an object with no remaining members. Required fields (no
// omitempty), lists, and map entries are never removed; they are only traversed so that
// nested optional sub-objects inside them are pruned. It returns the pruned j.
func pruneEmptyOptionalObjects(rv reflect.Value, j any) any {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return j
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Struct:
		m, ok := j.(map[string]any)
		if !ok {
			// Custom marshalers (metav1.Time and friends) do not encode as objects.
			return j
		}
		return pruneStructMembers(rv, m)
	case reflect.Slice, reflect.Array:
		items, ok := j.([]any)
		if !ok {
			return j
		}
		for i := range items {
			if i < rv.Len() {
				items[i] = pruneEmptyOptionalObjects(rv.Index(i), items[i])
			}
		}
		return items
	case reflect.Map:
		m, ok := j.(map[string]any)
		if !ok || rv.Type().Key().Kind() != reflect.String {
			return j
		}
		for _, key := range rv.MapKeys() {
			k := key.String()
			if child, present := m[k]; present {
				m[k] = pruneEmptyOptionalObjects(rv.MapIndex(key), child)
			}
		}
		return m
	default:
		return j
	}
}

func pruneStructMembers(rv reflect.Value, m map[string]any) map[string]any {
	t := rv.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported
		}
		name, omitEmpty, inline := parseJSONTag(f)
		if name == "-" {
			continue
		}
		fv := rv.Field(i)
		if inline {
			// Embedded struct without a name: its members are flattened into m.
			pruneEmptyOptionalObjects(fv, m)
			continue
		}
		child, present := m[name]
		if !present {
			continue
		}
		pruned := pruneEmptyOptionalObjects(fv, child)
		if omitEmpty {
			if pruned == nil {
				delete(m, name)
				continue
			}
			if cm, ok := pruned.(map[string]any); ok && len(cm) == 0 {
				delete(m, name)
				continue
			}
		}
		m[name] = pruned
	}
	return m
}

// parseJSONTag returns the JSON member name for f, whether it carries omitempty, and
// whether it is an embedded struct that encoding/json flattens into its parent.
func parseJSONTag(f reflect.StructField) (name string, omitEmpty bool, inline bool) {
	name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "-" && opts == "" {
		return "-", false, false
	}
	omitEmpty = slices.Contains(strings.Split(opts, ","), "omitempty")
	if name != "" {
		return name, omitEmpty, false
	}
	ft := f.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if f.Anonymous && ft.Kind() == reflect.Struct {
		return "", omitEmpty, true
	}
	return f.Name, omitEmpty, false
}
