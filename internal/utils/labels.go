package utils

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

// StampInstanceIDLabel sets promoter.argoproj.io/instance-id when settings.ControllerInstanceID() is non-nil.
// Returns labels (never nil).
func StampInstanceIDLabel(labels map[string]string) map[string]string {
	if labels == nil {
		labels = make(map[string]string)
	}
	instanceID := settings.ControllerInstanceID()
	if instanceID == nil {
		return labels
	}
	labels[promoterv1alpha1.InstanceIDLabel] = *instanceID
	return labels
}
