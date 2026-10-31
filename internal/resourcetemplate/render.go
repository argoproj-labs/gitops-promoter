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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sjson "k8s.io/apimachinery/pkg/util/json"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// TemplateData is the variable set available to ${ } expressions in spec.template and, with the report fields
// filled in, to spec.description and spec.url.
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

	// The remaining fields are set only for spec.description / spec.url rendering.

	// Phase is the phase computed this reconcile.
	Phase promoterv1alpha1.CommitStatusPhase
	// ResourceName and ResourceKind identify the created resource.
	ResourceName string
	ResourceKind string
	// Resource is the live resource as a map (nil when it does not exist).
	Resource map[string]any
	// ExpressionDescription and ExpressionURL are the description and url returned by spec.phase.expression, if any.
	ExpressionDescription string
	ExpressionURL         string
}

// Env returns the variables available to spec.template expressions, plus the keyBy helper function.
func (d TemplateData) Env() map[string]any {
	return map[string]any{
		"keyBy":                          keyBy,
		"Branch":                         d.Branch,
		"Sha":                            d.Sha,
		"ShortSha":                       d.ShortSha,
		"ReportOn":                       d.ReportOn,
		"Environment":                    d.Environment,
		"PromotionStrategy":              d.PromotionStrategy,
		"KubernetesResourceCommitStatus": d.KubernetesResourceCommitStatus,
	}
}

// ReportEnv returns the variables available to spec.description and spec.url expressions: Env plus the
// evaluation results.
func (d TemplateData) ReportEnv() map[string]any {
	env := d.Env()
	env["Phase"] = string(d.Phase)
	env["ResourceName"] = d.ResourceName
	env["ResourceKind"] = d.ResourceKind
	env["Resource"] = d.Resource
	env["ExpressionDescription"] = d.ExpressionDescription
	env["ExpressionUrl"] = d.ExpressionURL
	return env
}

// keyBy turns a list of objects into a map: each item's keyField value becomes a key whose value is the item's
// valueField (or the whole item when valueField is empty). Items that are not objects, lack a string keyField, or
// lack valueField are skipped. It exists because expr cannot build a map from a list, and name/value lists such as
// Tekton results or container env are common in Kubernetes status.
func keyBy(list []any, keyField, valueField string) map[string]any {
	out := make(map[string]any, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, ok := obj[keyField].(string)
		if !ok {
			continue
		}
		if valueField == "" {
			out[key] = obj
			continue
		}
		if v, ok := obj[valueField]; ok {
			out[key] = v
		}
	}
	return out
}

// ShortSha returns the first seven characters of sha (or sha itself when shorter).
func ShortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ParseTemplate decodes spec.template and returns the object together with the GVK it declares. apiVersion and
// kind must be literal strings: they identify the kind before any expression is evaluated.
func ParseTemplate(raw apiextensionsv1.JSON) (map[string]any, schema.GroupVersionKind, error) {
	if len(raw.Raw) == 0 {
		return nil, schema.GroupVersionKind{}, errors.New("template is empty")
	}
	var object map[string]any
	if err := k8sjson.Unmarshal(raw.Raw, &object); err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("template is not an object: %w", err)
	}
	apiVersion, err := literalString(object, "apiVersion")
	if err != nil {
		return nil, schema.GroupVersionKind{}, err
	}
	kind, err := literalString(object, "kind")
	if err != nil {
		return nil, schema.GroupVersionKind{}, err
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("template apiVersion %q is invalid: %w", apiVersion, err)
	}
	return object, gv.WithKind(kind), nil
}

func literalString(object map[string]any, key string) (string, error) {
	raw, ok := object[key]
	if !ok {
		return "", fmt.Errorf("template is missing %s", key)
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("template %s must be a non-empty string", key)
	}
	if strings.Contains(s, "${") {
		return "", fmt.Errorf("template %s must be a literal, not an expression", key)
	}
	return s, nil
}

// BuildParams are the inputs to BuildObject.
type BuildParams struct {
	// Template is the parsed spec.template object.
	Template map[string]any
	// Cache compiles the template's expressions.
	Cache *exprcache.Cache
	// SourceGVK is the kind to create.
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
	// Data supplies the expression variables.
	Data TemplateData
}

// BuildObject renders the resource to create from a template: the template body is copied without status
// and metadata, apiVersion/kind are set from SourceGVK, metadata is rebuilt from the params (the template's
// labels and annotations are rendered and merged under the controller's), and every string value is
// interpolated (see Interpolator.Value). A standalone expression that evaluates to nil removes its field.
func BuildObject(p BuildParams) (*unstructured.Unstructured, error) {
	if p.Template == nil {
		return nil, errors.New("template is nil")
	}
	body := runtime.DeepCopyJSON(p.Template)
	delete(body, "status")
	templateMeta, _ := body["metadata"].(map[string]any)
	delete(body, "metadata")
	delete(body, "apiVersion")
	delete(body, "kind")

	in := NewInterpolator(p.Cache, TemplateExpressionCachePrefix, p.Data.Env())
	rendered, _, err := interpolateValue(in, body, "")
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

	labels, err := renderStringMap(in, templateMeta, "labels")
	if err != nil {
		return nil, err
	}
	annotations, err := renderStringMap(in, templateMeta, "annotations")
	if err != nil {
		return nil, err
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

// renderStringMap renders metadata.<key> (labels or annotations) into a string map. Values must render to text.
func renderStringMap(in *Interpolator, meta map[string]any, key string) (map[string]string, error) {
	out := map[string]string{}
	raw, ok := meta[key].(map[string]any)
	if !ok {
		return out, nil
	}
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("metadata.%s.%s must be a string, got %T", key, k, v)
		}
		rendered, err := in.String(s)
		if err != nil {
			return nil, fmt.Errorf("failed to render metadata.%s.%s: %w", key, k, err)
		}
		out[k] = rendered
	}
	return out, nil
}

// interpolateValue walks v and renders every string through in. omit is true when a standalone expression
// evaluated to nil, telling the caller to drop the field.
func interpolateValue(in *Interpolator, v any, path string) (out any, omit bool, err error) {
	switch t := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(t))
		for k, child := range t {
			r, omitChild, err := interpolateValue(in, child, joinPath(path, k))
			if err != nil {
				return nil, false, err
			}
			if omitChild {
				continue
			}
			result[k] = r
		}
		return result, false, nil
	case []any:
		result := make([]any, 0, len(t))
		for i, child := range t {
			r, omitChild, err := interpolateValue(in, child, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, false, err
			}
			if omitChild {
				continue
			}
			result = append(result, r)
		}
		return result, false, nil
	case string:
		r, err := in.Value(t)
		if err != nil {
			return nil, false, fmt.Errorf("failed to render template value at %s: %w", path, err)
		}
		return r, r == nil, nil
	default:
		return v, false, nil
	}
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
