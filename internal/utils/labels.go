package utils

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// CopyInstanceIDLabel copies the promoter.argoproj.io/instance-id label from
// parent to child if the parent has it set to a non-empty value. The
// child's labels map is initialized lazily. The operation is a no-op when
// the parent is missing the label or carries an empty value, so it is safe
// to call unconditionally at child-creation sites and is idempotent across
// repeated reconciliations.
func CopyInstanceIDLabel(parent, child client.Object) {
	v, ok := parent.GetLabels()[promoterv1alpha1.InstanceIDLabel]
	if !ok || v == "" {
		return
	}
	labels := child.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[promoterv1alpha1.InstanceIDLabel] = v
	child.SetLabels(labels)
}

// CopyInstanceIDLabelToMap mirrors CopyInstanceIDLabel for child-creation
// sites that build the labels map in-place before passing it to an
// apply-configuration builder (e.g. acv1alpha1.X(...).WithLabels(...)).
// Returns the (possibly newly allocated) map so callers can fluently chain
// it through their builders. No-op semantics match CopyInstanceIDLabel.
func CopyInstanceIDLabelToMap(parent client.Object, labels map[string]string) map[string]string {
	v, ok := parent.GetLabels()[promoterv1alpha1.InstanceIDLabel]
	if !ok || v == "" {
		return labels
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[promoterv1alpha1.InstanceIDLabel] = v
	return labels
}
