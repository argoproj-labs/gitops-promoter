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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/argoproj-labs/gitops-promoter/internal/types/tekton"
)

// TektonCommitStatusSpec defines the desired state of TektonCommitStatus
type TektonCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this commit status applies to.
	// +kubebuilder:validation:Required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the gate name referenced in the PromotionStrategy's activeCommitStatuses or proposedCommitStatuses.
	// +optional
	// +kubebuilder:default=tekton-result
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key,omitempty"`

	// TektonResource is a reference to the TektonResource to run for the checks
	// +kubebuilder:validation:Required
	TektonResource TektonResourceRef `json:"tektonResourceRef"`

	// ExpectedResults are individual checks to make on results from the Tekton resource
	// If left empty along with the general expression also being empty, whether this passes will be determined by the completion status of
	// the Tekton run.
	// +optional
	ExpectedResults []ExpectedResult `json:"expectedResults,omitempty"`

	// Expression is an expr expression that can be used for evaluation that requires multiple results values. It must return a boolean.
	// The results values can be accessed through the map variable Results.
	// If left empty along with the expected results array being empty, whether this passes will be determined by the completion status of
	// the Tekton run.
	// +optional
	Expression string `json:"expression,omitempty"`

	// PruneRun determines if the run of the Tekton Resource will be deleted after running. Defaults to false.
	// +kubebuilder:default:=false
	PruneRun bool `json:"pruneRun,omitempty"`

	// URL generates the URL to use in the CommitStatus so something like a link to the Tekton Dashboard UI can be used
	// +optional
	URL URLConfig `json:"url,omitempty"`

	// Target specifies which commit SHA to run the Tekton Pipeline/Task on.
	// - "proposed": runs the pipeline on the proposed commit (default)
	// - "active": runs the pipeline on the currently active/deployed commit
	//
	// The commit SHA to report the validation on is determined by where the key is configured in the referenced PromotionStrategy:
	//   - Case key is in proposedCommitStatuses - Will report on the proposed commit (gate an incoming promotion)
	//   - Case key is in activeCommitStatuses - Will report on the active commit (gate downstream promotions)
	// If configuration for CommitStatuses is done via individual environments that will also be respected.
	//
	// Examples:
	//   Target = "proposed", Report On = "proposed" - Run the pipeline on the proposed commit and don't promote if it fails.
	//                                                 An example pipeline that would be good here is a security scan.
	//   Target = "active", Report On = "proposed" - Run the pipeline on the current commit in the environment and block promotion if it fails.
	//                                               An example pipeline to use in this setup would be running a health check on the active environment
	//                                               and block if it is not healthy.
	//                                               If there is no active environment then this would be skipped.
	//  Target = "active", Report On = "active" - Run a pipeline on the active commit and then block future promotions if it fails
	//                                            A pipeline that would be useful here is running tests on an active environment and blocking further
	//                                            promotions if any of them fail.
	//  Target = "proposed", Report on = "active" - This case is not supported as it does not make logical sense. If used will give an error.
	//
	// +optional
	// +kubebuilder:default="proposed"
	// +kubebuilder:validation:Enum=active;proposed
	Target string `json:"target,omitempty"`
}

// TektonResourceRef defines a reference to a Tekton resource. It does not assume the same namespace but the ServiceAccount
// provided must have permissions in the desired namespace.
type TektonResourceRef struct {
	// Kind is the kind of Tekton resource to target.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Pipeline;Task
	Kind string `json:"kind"`

	// Name is the name of the Tekton resource to target. It must be in the same namespace as the commit status.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// ServiceAccount is a ServiceAccount in the same namespace as the Tekton resource and is passed to the resource to be used by it.
	// on the targeted Tekton resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ServiceAccount string `json:"serviceAccount"`

	// Parameters are the parameters that get passed to the Tekton resource run that is created.
	// A paramter's value can use the following Go template values to inject information into a paramter.
	//
	// .TargetedSha - The SHA that the pipeline is being run on
	// .Branch - The name of the branch for the environment
	//
	// So for example you could have parameters like:
	// params:
	//   - githubUrl: "https://github.com/someuser/somerepo/tree/{{ .Branch }}"
	//   - shortSha: "{{ .TargetedSha | trunc 8 }}"
	//
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`
}

type ExpectedResult struct {
	// Key is the name of the result from the Tekton resource to use
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Expression is an expr expression to be used with the result. Must return a boolean.
	// The result can be accessed through the string variable Result.
	// +kubebuilder:validation:Required
	Expression string `json:"expression"`
}

type TektonCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`

	// Results holds the results of each evaluation of expressions from the Tekton run.
	Environments []TektonCommitStatusEnvironmentStatus `json:"environments,omitempty"`
}

// TektonCommitStatusEnvironmentStatus defines the observed status for a specific environment based on specified result keys.
type TektonCommitStatusEnvironmentStatus struct {
	// Branch is the name of the branch/environment.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// FIXME: MAKE BETTER
	// ProposedHydratedSha is the proposed hydrated commit SHA where the Tekton resource was run on.
	// This comes from the PromotionStrategy's environment status.
	// The CommitStatus resource is created with this SHA, allowing the PromotionStrategy to gate
	// promotions based on the validation of the ACTIVE commit.
	// May be empty if the PromotionStrategy hasn't reconciled yet.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	ProposedHydratedSha string `json:"proposedHydratedSha"`

	// FIXME: MAKE BETTER
	// ActiveHydratedSha is the currently active (deployed) hydrated commit SHA that the Tekton resource was run on.
	// This comes from the PromotionStrategy's environment status.
	// The expression is evaluated against THIS commit's data, not the proposed commit.
	// May be empty if the PromotionStrategy hasn't reconciled yet.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	ActiveHydratedSha string `json:"activeHydratedSha,omitempty"`

	// TargetedSha is the commit SHA that the Tekton resource was last run on.
	// This will match either ProposedHydratedSha or ActiveHydratedSha depending on
	// the Target setting ("proposed" or "active").
	// This field clarifies which commit's data was used in the expression evaluation.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	TargetedSha string `json:"targetedSha,omitempty"`

	// Phase is the current phase of the commit status.
	// Calculated by aggregating the expression results.
	// If no expressions are provided it is determined via whether the run succeeded or failed.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase CommitStatusPhase `json:"phase"`

	// TektonRunName is the name of the run that was created
	// +optional
	TektonRunName string `json:"tektonRunName,omitempty"`

	// LastRunTime is when the Tekton run was started/created
	// +optional
	LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`

	// TektonRunPhase is the phase that the Tekton run is in
	// +optional
	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Cancelled
	TektonRunPhase string `json:"tektonRunPhase,omitempty"`

	// ExpressionResult holds the result of the general expression used on all results.
	ExpressionResult bool `json:"expressionResult,omitempty"`

	// IndividualExpressionResults holds all of the results of the expressions used on individual keys.
	IndividualExpressionResults []IndividualExpressionResult `json:"individualExpressionResults,omitempty"`
}

// IndividualExpressionResult holds the results of expressions run on an individual result
type IndividualExpressionResult struct {
	// Key is the key of the result.
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// ExpressionResult is the result of the expression evaluated at the key
	// +kubebuilder:validation:Required
	ExpressionResult bool `json:"expressionResult"`
}

// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#tektoncommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TektonCommitStatus is the schema for the tektoncommitstatus API.
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type TektonCommitStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TektonCommitStatusSpec   `json:"spec,omitempty"`
	Status TektonCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TektonCommitStatusList contains a list of TektonCommitStatus.
type TektonCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TektonCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the TektonCommitStatus.
func (tcs *TektonCommitStatus) GetConditions() *[]metav1.Condition {
	return &tcs.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (tcs *TektonCommitStatus) SetObservedGeneration(generation int64) {
	tcs.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (tcs *TektonCommitStatus) SetStatusInstanceID(v *string) {
	tcs.Status.InstanceID = v
}

// UseRunStatusEvaluation determines if the TektonCommitStatus should be evaluated based on whether the pipeline
// succeeded or failed. This happens when there is no expression to check and no expected results.
func (tcs *TektonCommitStatus) UseRunStatusEvaluation() bool {
	return tcs.Spec.Expression == "" && len(tcs.Spec.ExpectedResults) == 0
}

func (env *TektonCommitStatusEnvironmentStatus) IsTektonResourceFinished() bool {
	return env.TektonRunPhase == tekton.TektonRunPhaseSucceeded || env.TektonRunPhase == tekton.TektonRunPhaseFailed || env.TektonRunPhase == tekton.TektonRunPhaseCancelled
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TektonCommitStatus{}, &TektonCommitStatusList{})
		return nil
	})
}
