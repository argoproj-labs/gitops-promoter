package utils

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CopyInstanceIDLabel copies promoter.argoproj.io/instance-id from parent to child when the
// parent carries a non-empty value. No-op when the parent label is missing or empty.
func CopyInstanceIDLabel(parent, child client.Object) {
	v, ok := parent.GetLabels()[promoterv1alpha1.InstanceIDLabel]
	if !ok || v == "" {
		return
	}
	labels := child.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[promoterv1alpha1.InstanceIDLabel] = v
	child.SetLabels(labels)
}

// CopyInstanceIDLabelToMap mirrors CopyInstanceIDLabel for SSA apply-config call sites that
// build a labels map locally. Returns labels (never nil).
func CopyInstanceIDLabelToMap(parent client.Object, labels map[string]string) map[string]string {
	if labels == nil {
		labels = make(map[string]string)
	}
	v, ok := parent.GetLabels()[promoterv1alpha1.InstanceIDLabel]
	if !ok || v == "" {
		return labels
	}
	labels[promoterv1alpha1.InstanceIDLabel] = v
	return labels
}

// InstanceIDStatusValue returns a pointer for status.instanceID from parent metadata labels.
// Returns nil when the parent has no non-empty instance-id label (default install).
func InstanceIDStatusValue(parent client.Object) *string {
	v, ok := parent.GetLabels()[promoterv1alpha1.InstanceIDLabel]
	if !ok || v == "" {
		return nil
	}
	return &v
}
