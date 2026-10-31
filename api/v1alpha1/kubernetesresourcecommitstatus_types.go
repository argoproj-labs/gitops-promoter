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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// KubernetesResourceLifecycleMode selects how the KubernetesResourceCommitStatus controller manages the
// real resources it creates from the template.
// +kubebuilder:validation:Enum=PerSha;InPlace
type KubernetesResourceLifecycleMode string

const (
	// KubernetesResourceLifecyclePerSha creates a new resource for every (environment, reported SHA) pair and
	// never mutates it after creation. Older resources for the same environment are pruned down to
	// spec.lifecycle.historyLimit once the current one exists. Use this for immutable kinds such as batch/v1 Job.
	KubernetesResourceLifecyclePerSha KubernetesResourceLifecycleMode = "PerSha"
	// KubernetesResourceLifecycleInPlace keeps a single resource per environment and re-applies the rendered
	// template with Server-Side Apply on every reconcile. Use this for mutable kinds.
	KubernetesResourceLifecycleInPlace KubernetesResourceLifecycleMode = "InPlace"
)

// KubernetesResourceCommitStatusSpec defines the desired state of KubernetesResourceCommitStatus.
//
// The controller creates one real Kubernetes resource per applicable PromotionStrategy environment from
// spec.template, then evaluates spec.phase.expression against the live resource to compute the phase
// reported on the child CommitStatus.
type KubernetesResourceCommitStatusSpec struct {
	// PromotionStrategyRef references the PromotionStrategy this gate applies to.
	// The controller gates every environment of the referenced PromotionStrategy where spec.key matches
	// an entry in PromotionStrategy.spec.proposedCommitStatuses / activeCommitStatuses (global) or the
	// environment's own proposedCommitStatuses / activeCommitStatuses.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the commit status key. It is matched against the PromotionStrategy's proposedCommitStatuses or
	// activeCommitStatuses to determine which environments this gate applies to, and it is used as the
	// promoter.argoproj.io/commit-status label and in the CommitStatus name shown in the SCM.
	// Must be lowercase alphanumeric with hyphens, 1–63 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// ReportOn selects which commit SHA the CommitStatus is reported on and which SHA the resource is created for.
	// - "proposed": the proposed hydrated SHA (default). Use for checks that must pass before a change is promoted.
	// - "active": the active hydrated SHA. Use for checks that run against what is currently live.
	// +optional
	// +kubebuilder:default="proposed"
	// +kubebuilder:validation:Enum=proposed;active
	ReportOn string `json:"reportOn,omitempty"`

	// PublishTo is copied to spec.publishTo on each CommitStatus this controller creates.
	// - "scm": the CommitStatus controller posts them to the SCM provider
	// The default is ["scm"]. An empty list keeps them in Kubernetes and does not post them to the SCM.
	// +optional
	// +kubebuilder:default={scm}
	// +listType=set
	// +kubebuilder:validation:items:Enum=scm
	PublishTo *[]CommitStatusPublishTo `json:"publishTo,omitempty"`

	// Template is the Kubernetes object the controller creates for every applicable environment. It must carry
	// apiVersion and kind (a namespaced kind listed in ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources);
	// metadata.name, metadata.namespace, and status are ignored because the controller sets them. metadata.labels and
	// metadata.annotations are kept and merged under the controller's own labels.
	//
	// Any string value may contain expr (https://expr-lang.org) expressions wrapped in ${ }:
	//   - A value that is exactly one expression, such as "${ Sha }" or "${ Environment.Proposed.Hydrated.Sha }", is
	//     replaced by the expression's result with its type preserved, so integers, booleans, lists, and objects
	//     can be computed. A nil result removes the field.
	//   - A string with embedded expressions, such as "smoke-${ ShortSha }", concatenates the results as text.
	//   - "$${" produces a literal "${".
	// Available variables: Branch, Sha, ShortSha, ReportOn, Environment (the PromotionStrategy status entry for the
	// branch), PromotionStrategy, and KubernetesResourceCommitStatus (this gate, as of the previous reconcile).
	// Everything else is copied verbatim. The rendered object is validated by the API server when it is created.
	// +required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Template apiextensionsv1.JSON `json:"template"`

	// Lifecycle controls whether a new resource is created per SHA (default) or a single resource per environment
	// is updated in place, and how many per-SHA resources are retained. See KubernetesResourceLifecycle.
	// +optional
	// +kubebuilder:default={}
	Lifecycle KubernetesResourceLifecycle `json:"lifecycle,omitzero"`

	// Phase holds the expr expression that computes the commit status phase from the live resource.
	// See KubernetesResourcePhaseSpec.
	// +required
	Phase KubernetesResourcePhaseSpec `json:"phase"`

	// Description is the human-readable commit status description shown in the SCM provider. Supports the same
	// ${ } expressions as Template, with these additional variables:
	//   - Phase: the phase computed this reconcile (pending/success/failure)
	//   - Resource: the live resource as a map (nil when it does not exist)
	//   - ResourceName and ResourceKind: the name and Kind of the created resource
	//   - ExpressionDescription and ExpressionUrl: the description and url returned by spec.phase.expression, if any
	// When empty, the description returned by spec.phase.expression is used, falling back to a message such as
	// "Waiting for Job my-job" or "Job my-job reports success".
	// +optional
	Description string `json:"description,omitempty"`

	// Url is the commit status target URL shown in the SCM provider. Same ${ } expressions and variables as Description.
	// When empty, the url returned by spec.phase.expression (if any) is used.
	// +optional
	Url string `json:"url,omitempty"`
}

// KubernetesResourceLifecycle controls how created resources are managed over time.
// +kubebuilder:validation:XValidation:rule="!has(self.historyLimit) || !has(self.mode) || self.mode == 'PerSha'",message="historyLimit is only valid with mode PerSha"
type KubernetesResourceLifecycle struct {
	// Mode is PerSha (default) or InPlace. See KubernetesResourceLifecycleMode.
	// +optional
	// +kubebuilder:default=PerSha
	Mode KubernetesResourceLifecycleMode `json:"mode,omitempty"`

	// HistoryLimit is the number of per-SHA resources retained per environment, including the current one.
	// Older resources are deleted once the current one exists. Defaults to 1 when omitted (no schema default,
	// so the field can stay unset for mode InPlace). Only valid with mode PerSha.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	HistoryLimit *int32 `json:"historyLimit,omitempty"`
}

// KubernetesResourcePhaseSpec holds the expression that maps the live resource to a commit status phase.
type KubernetesResourcePhaseSpec struct {
	// Expression is an expr (https://expr-lang.org) expression evaluated on every reconcile while the
	// created resource exists. Available variables:
	//   - Resource (map): the live resource, including status. Guard optional fields with ?. and ??,
	//     for example Resource.status?.conditions ?? [], because member access through nil is an error.
	//   - Branch (string), Sha (string), ReportOn (string)
	//   - Environment: the PromotionStrategy status entry for Branch
	//   - PromotionStrategy: the full PromotionStrategy spec and status
	//   - KubernetesResourceCommitStatus: this gate (snapshot from the previous reconcile)
	//
	// Return values:
	//   - a boolean: true → success, false → pending
	//   - an object { phase, description?, url? } where phase is "success", "pending", or "failure"
	//     (an omitted or empty phase means "pending")
	// Any other return type, or an unrecognized phase string, fails the reconcile.
	//
	// Example for a batch/v1 Job:
	//
	//	any(Resource.status?.conditions ?? [], {.type == 'Complete' && .status == 'True'})
	//	  ? {phase: 'success'}
	//	  : any(Resource.status?.conditions ?? [], {.type == 'Failed' && .status == 'True'})
	//	    ? {phase: 'failure', description: 'Smoke test is failing'}
	//	    : false
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	Expression string `json:"expression"`
}

// KubernetesResourceReference identifies a created resource.
type KubernetesResourceReference struct {
	// APIVersion of the created resource (the real kind, for example batch/v1).
	// +required
	APIVersion string `json:"apiVersion"`
	// Kind of the created resource.
	// +required
	Kind string `json:"kind"`
	// Name of the created resource. It lives in the gate's namespace.
	// +required
	Name string `json:"name"`
}

// KubernetesResourceCommitStatusEnvironmentStatus is the observed state for one environment branch.
type KubernetesResourceCommitStatusEnvironmentStatus struct {
	// Branch is the environment branch name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Gate report fields (phase, description, url, reportedSha) mirror the child CommitStatus spec.
	GateEnvironmentCommitStatus `json:",inline"`

	// Resource identifies the real resource created for this environment and reportedSha.
	// +optional
	Resource *KubernetesResourceReference `json:"resource,omitempty"`

	// LastEvaluationTime is when spec.phase.expression was last evaluated against the live resource.
	// +optional
	LastEvaluationTime *metav1.Time `json:"lastEvaluationTime,omitempty"`
}

// KubernetesResourceCommitStatusStatus defines the observed state of KubernetesResourceCommitStatus.
type KubernetesResourceCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Environments holds the observed state of each applicable environment.
	// +optional
	// +kubebuilder:validation:MaxItems=500
	// +listType=map
	// +listMapKey=branch
	Environments []KubernetesResourceCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#kubernetesresourcecommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// KubernetesResourceCommitStatus is the Schema for the kubernetesresourcecommitstatuses API
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.template.kind`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type KubernetesResourceCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of KubernetesResourceCommitStatus
	// +required
	Spec KubernetesResourceCommitStatusSpec `json:"spec"`

	// status defines the observed state of KubernetesResourceCommitStatus
	// +optional
	Status KubernetesResourceCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KubernetesResourceCommitStatusList contains a list of KubernetesResourceCommitStatus
type KubernetesResourceCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubernetesResourceCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the KubernetesResourceCommitStatus.
func (krcs *KubernetesResourceCommitStatus) GetConditions() *[]metav1.Condition {
	return &krcs.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (krcs *KubernetesResourceCommitStatus) SetObservedGeneration(generation int64) {
	krcs.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (krcs *KubernetesResourceCommitStatus) SetStatusInstanceID(v *string) {
	krcs.Status.InstanceID = v
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &KubernetesResourceCommitStatus{}, &KubernetesResourceCommitStatusList{})
		return nil
	})
}
