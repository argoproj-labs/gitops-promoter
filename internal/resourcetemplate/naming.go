// Package resourcetemplate implements the template machinery behind KubernetesResourceCommitStatus:
// parsing of spec.template, evaluation of its ${ } expr expressions, naming of the created resources, and
// evaluation of the phase expression against the live resource.
package resourcetemplate

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// GVKOf converts a configured ResourceGroupVersionKind to a schema.GroupVersionKind.
func GVKOf(r promoterv1alpha1.ResourceGroupVersionKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: r.Group, Version: r.Version, Kind: r.Kind}
}

// ResourceNameMaxLength bounds the names of created resources. Kinds such as batch/v1 Job copy their name into
// a label value (batch.kubernetes.io/job-name), so names must fit a DNS-1123 label.
const ResourceNameMaxLength = validation.DNS1123LabelMaxLength

// PerShaResourceName returns the name of the resource created for gate, branch, and sha in PerSha mode.
// The full SHA participates in the hash so names for different SHAs never collide after truncation.
func PerShaResourceName(gateName, branch, sha string) string {
	return utils.KubeSafeUniqueNameWithMaxLength(gateName+"-"+branch+"-"+sha, ResourceNameMaxLength)
}

// InPlaceResourceName returns the name of the single resource maintained for gate and branch in InPlace mode.
func InPlaceResourceName(gateName, branch string) string {
	return utils.KubeSafeUniqueNameWithMaxLength(gateName+"-"+branch, ResourceNameMaxLength)
}
