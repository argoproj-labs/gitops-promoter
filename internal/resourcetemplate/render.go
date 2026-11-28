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

package resourcetemplate

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// TemplateData is the data available to Go templates in template string values and in the gate's
// descriptionTemplate / urlTemplate.
type TemplateData struct {
	// Branch is the environment branch the resource is created for.
	Branch string
	// Sha is the reported hydrated SHA; ShortSha is its first 7 characters.
	Sha      string
	ShortSha string
	// ReportOn is "proposed" or "active".
	ReportOn string
	// Environment is the PromotionStrategy status entry for Branch (nil when absent).
	Environment *promoterv1alpha1.EnvironmentStatus
	// PromotionStrategy is the referenced PromotionStrategy.
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	// KubernetesResourceCommitStatus is the gate (snapshot from the previous reconcile).
	KubernetesResourceCommitStatus *promoterv1alpha1.KubernetesResourceCommitStatus
	// Template is the template object as a map.
	Template map[string]any

	// The remaining fields are set only for descriptionTemplate / urlTemplate rendering.

	// Phase is the phase computed this reconcile.
	Phase promoterv1alpha1.CommitStatusPhase
	// ResourceName and ResourceKind identify the created resource.
	ResourceName string
	ResourceKind string
	// Resource is the live resource as a map (nil when it does not exist).
	Resource map[string]any
	// ExpressionDescription is the description returned by spec.phase.expression, if any.
	ExpressionDescription string
}

// ShortSha returns the first seven characters of sha (or sha itself when shorter).
func ShortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// BuildParams are the inputs to BuildObject.
type BuildParams struct {
	// Template is the template object (wrapped kind).
	Template *unstructured.Unstructured
	// SourceGVK is the real kind to create.
	SourceGVK schema.GroupVersionKind
	// Name and Namespace of the created resource.
	Name      string
	Namespace string
	// Labels are merged over the template's metadata.labels.
	Labels map[string]string
	// Annotations are merged over the template's metadata.annotations.
	Annotations map[string]string
	// Owner is set as the controlling owner reference.
	Owner metav1.OwnerReference
	// Data is the Go-template data for string values.
	Data TemplateData
}

// BuildObject renders the real resource from a template object: the template body is copied without status
// and metadata, apiVersion/kind are replaced with the source kind, metadata is rebuilt from the params (the
// template's labels and annotations are preserved and merged under the controller's labels), and every
// string value containing "{{" is rendered as a Go template with Data. Non-string values are copied verbatim.
func BuildObject(p BuildParams) (*unstructured.Unstructured, error) {
	if p.Template == nil {
		return nil, errors.New("template is nil")
	}
	body := p.Template.DeepCopy().Object
	delete(body, "status")
	templateMeta, _ := body["metadata"].(map[string]any)
	delete(body, "metadata")
	delete(body, "apiVersion")
	delete(body, "kind")

	rendered, err := renderStrings(body, p.Data, "")
	if err != nil {
		return nil, err
	}
	renderedBody, ok := rendered.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("rendered template body is %T, expected an object", rendered)
	}
	obj := &unstructured.Unstructured{Object: renderedBody}
	obj.SetGroupVersionKind(p.SourceGVK)
	obj.SetName(p.Name)
	obj.SetNamespace(p.Namespace)

	labels := map[string]string{}
	annotations := map[string]string{}
	if templateMeta != nil {
		copyStringMap(templateMeta["labels"], labels)
		copyStringMap(templateMeta["annotations"], annotations)
	}
	// Render template labels and annotations too: they are string leaves users may want to parameterize.
	for k, v := range labels {
		s, err := renderString(v, p.Data, "metadata.labels."+k)
		if err != nil {
			return nil, err
		}
		labels[k] = s
	}
	for k, v := range annotations {
		s, err := renderString(v, p.Data, "metadata.annotations."+k)
		if err != nil {
			return nil, err
		}
		annotations[k] = s
	}
	maps.Copy(labels, p.Labels)
	obj.SetLabels(labels)
	maps.Copy(annotations, p.Annotations)
	if len(annotations) > 0 {
		obj.SetAnnotations(annotations)
	}
	obj.SetOwnerReferences([]metav1.OwnerReference{p.Owner})
	return obj, nil
}

func copyStringMap(src any, dst map[string]string) {
	m, ok := src.(map[string]any)
	if !ok {
		return
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			dst[k] = s
		}
	}
}

// renderStrings walks v and renders every string containing "{{" as a Go template.
func renderStrings(v any, data TemplateData, path string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			r, err := renderStrings(child, data, joinPath(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			r, err := renderStrings(child, data, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		return renderString(t, data, path)
	default:
		return v, nil
	}
}

func renderString(s string, data TemplateData, path string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	rendered, err := utils.RenderStringTemplate(s, data, "missingkey=error")
	if err != nil {
		return "", fmt.Errorf("failed to render template value at %s: %w", path, err)
	}
	return rendered, nil
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// ResourceLabels returns the labels stamped on every resource the gate creates: the parent-gate label,
// environment, commit-status key, SHA, and the instance-id label when the controller runs with one.
func ResourceLabels(gate *promoterv1alpha1.KubernetesResourceCommitStatus, branch, sha string) map[string]string {
	labels := utils.CommitStatusStandardLabels(gate, branch, gate.Spec.Key)
	labels[promoterv1alpha1.ShaLabel] = sha
	return labels
}

// ResourceAnnotations returns the annotations stamped on every resource the gate creates.
func ResourceAnnotations(createdAt time.Time) map[string]string {
	return map[string]string{
		promoterv1alpha1.ResourceCreatedAtAnnotation: createdAt.UTC().Format(time.RFC3339Nano),
	}
}

// ResourceCreationTime returns when the gate created obj: the created-at annotation when present and valid,
// otherwise metadata.creationTimestamp.
func ResourceCreationTime(obj *unstructured.Unstructured) time.Time {
	if raw, ok := obj.GetAnnotations()[promoterv1alpha1.ResourceCreatedAtAnnotation]; ok {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
	}
	return obj.GetCreationTimestamp().Time
}
