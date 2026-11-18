/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package resourcetemplate implements the template machinery behind KubernetesResourceCommitStatus:
// naming of wrapped template kinds, generation of their CustomResourceDefinitions from the source
// kind's schema, rendering of a real resource from a template object, and evaluation of the phase
// expression against the live resource.
package resourcetemplate

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// TemplateGroupFor returns the API group of the wrapped template kind for a source API group:
// "<group>.templates.promoter.argoproj.io", or "core.templates.promoter.argoproj.io" for the core group.
func TemplateGroupFor(sourceGroup string) string {
	if sourceGroup == "" {
		sourceGroup = promoterv1alpha1.TemplateCoreGroup
	}
	return sourceGroup + "." + promoterv1alpha1.TemplateGroupSuffix
}

// SourceGroupFor inverts TemplateGroupFor. ok is false when templateGroup is not a wrapped template group.
func SourceGroupFor(templateGroup string) (group string, ok bool) {
	suffix := "." + promoterv1alpha1.TemplateGroupSuffix
	if !strings.HasSuffix(templateGroup, suffix) {
		return "", false
	}
	group = strings.TrimSuffix(templateGroup, suffix)
	if group == "" {
		return "", false
	}
	if group == promoterv1alpha1.TemplateCoreGroup {
		return "", true
	}
	return group, true
}

// TemplateGVKFor returns the wrapped template GVK for a source GVK (same Kind and version, template group).
func TemplateGVKFor(source schema.GroupVersionKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: TemplateGroupFor(source.Group), Version: source.Version, Kind: source.Kind}
}

// SourceGVKFor returns the real GVK a wrapped template GVK stands for.
func SourceGVKFor(template schema.GroupVersionKind) (schema.GroupVersionKind, error) {
	group, ok := SourceGroupFor(template.Group)
	if !ok {
		return schema.GroupVersionKind{}, fmt.Errorf("%s is not a wrapped template API group (expected suffix .%s)", template.Group, promoterv1alpha1.TemplateGroupSuffix)
	}
	return schema.GroupVersionKind{Group: group, Version: template.Version, Kind: template.Kind}, nil
}

// SourceGVKForTemplateRef resolves a KubernetesResourceTemplateReference to the template GVK and the real GVK.
func SourceGVKForTemplateRef(ref promoterv1alpha1.KubernetesResourceTemplateReference) (templateGVK, sourceGVK schema.GroupVersionKind, err error) {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return schema.GroupVersionKind{}, schema.GroupVersionKind{}, fmt.Errorf("invalid templateRef.apiVersion %q: %w", ref.APIVersion, err)
	}
	templateGVK = gv.WithKind(ref.Kind)
	sourceGVK, err = SourceGVKFor(templateGVK)
	if err != nil {
		return schema.GroupVersionKind{}, schema.GroupVersionKind{}, err
	}
	return templateGVK, sourceGVK, nil
}

// TemplateCRDName returns the name of the wrapped template CustomResourceDefinition for a source plural and group.
func TemplateCRDName(plural, sourceGroup string) string {
	return plural + "." + TemplateGroupFor(sourceGroup)
}

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
