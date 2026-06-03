// Package predicate contains controller-runtime event predicates used by the
// GitOps Promoter reconcilers.
package predicate

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// InstanceID returns a predicate that filters objects by the
// promoter.argoproj.io/instance-id label.
//
// When id is empty, the predicate passes every object. This preserves the
// "reconcile everything" semantics for controllers started without
// --instance-id, which is a hard backwards-compatibility constraint.
//
// When id is non-empty, only objects whose instance-id label equals id are
// admitted. Missing label or mismatching value are both filtered out.
func InstanceID(id string) predicate.Predicate {
	if id == "" {
		return predicate.NewPredicateFuncs(func(client.Object) bool { return true })
	}
	return predicate.NewPredicateFuncs(func(o client.Object) bool {
		return o.GetLabels()[promoterv1alpha1.InstanceIDLabel] == id
	})
}
