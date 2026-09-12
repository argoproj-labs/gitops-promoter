package v1alpha1

import "k8s.io/apimachinery/pkg/runtime/schema"

// WithDefaults returns a copy of the ref with empty group/kind filled from API defaults.
func (r OrderCommitStatusRef) WithDefaults() OrderCommitStatusRef {
	out := r
	if out.Group == "" {
		out.Group = DefaultOrderCommitStatusGroup
	}
	if out.Kind == "" {
		out.Kind = DefaultOrderCommitStatusKind
	}
	return out
}

// GroupKind returns the normalized group/kind pair for this reference.
func (r OrderCommitStatusRef) GroupKind() schema.GroupKind {
	normalized := r.WithDefaults()
	return schema.GroupKind{Group: normalized.Group, Kind: normalized.Kind}
}
