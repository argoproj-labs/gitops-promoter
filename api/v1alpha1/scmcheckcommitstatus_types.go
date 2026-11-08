/*
Copyright 2024.

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
)

type SCMCheckStatus string

// The following are constants for a SCM check's status
const (
	// SCMCheckStatusPending is set when a check has not started yet
	SCMCheckStatusPending SCMCheckStatus = "pending"
	// SCMCheckStatusInProgress is set when a check is running
	SCMCheckStatusInProgress SCMCheckStatus = "in-progress"
	// SCMCheckStatusSucceeded is set wehn a check has finished successfully
	SCMCheckStatusSucceeded SCMCheckStatus = "succeeded"
	// SCMCheckStatusFailed is set when a check has finished and failed a reason.
	SCMCheckStatusFailed SCMCheckStatus = "failed"
	// SCMCheckStatusCancelled is set when a check was running but was cancelled.
	SCMCheckStatusCancelled SCMCheckStatus = "cancelled"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SCMCheckCommitStatusSpec defines the desired state of SCMCheckCommitStatus
type SCMCheckCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this commit status applies to.
	// The controller will validate commits from ALL environments in the referenced PromotionStrategy
	// where this GitCommitStatus.Spec.Key matches an entry in either:
	//   - PromotionStrategy.Spec.ProposedCommitStatuses (applies to all environments), OR
	//   - Environment.ProposedCommitStatuses (applies to specific environment)
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the unique identifier for this validation rule.
	// It is used as the commit status key and in status messages.
	// This key is matched against PromotionStrategy's proposedCommitStatuses or activeCommitStatuses
	// to determine which environments this validation applies to.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// PublishTo is copied to spec.publishTo on each CommitStatus this controller creates.
	// - "scm": the CommitStatus controller posts them to the SCM provider
	// The default is empty as posting SCM checks would be a duplicate. An empty list keeps them
	// in Kubernetes and does not post them to the SCM. Omitting the field uses the default.
	// Promotion gates use the phase in every case.
	// +optional
	// +kubebuilder:default={}
	// +listType=set
	// +kubebuilder:validation:items:Enum=scm
	PublishTo *[]CommitStatusPublishTo `json:"publishTo,omitempty"`

	// Filters is a list of expressions evaluated using the expr library (github.com/expr-lang/expr). These expressions
	// can be used to filter which SCM checks will be used to create CommitStatuses. If the name of an SCM check matches
	// one of the expressions it will not be included. The name can be accessed via the variable "Check.Name" that is passed
	// into the expression. Do note that some SCMs might not support a name per checks and if they don't then this will not work
	// with it. Check the docs for this CommitStatus for which SCMs support this.
	Filters []string `json:"filters"`
}

// SCMCheckCommitStatusStatus defines the observed state of SCMCheckCommitStatus.
type SCMCheckCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Environments holds the validation results for each environment where this validation applies.
	// Each entry corresponds to an environment from the PromotionStrategy where the Key matches
	// either global or environment-specific proposedCommitStatuses.
	//
	// The controller always validates the proposed commit and is always reported to the proposed commit for gating.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []SCMCheckCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the SCMCheckCommitStatus resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
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

// SCMCheckCommitStatusEnvironmentStatus defines the observed validation status for a specific environment.
type SCMCheckCommitStatusEnvironmentStatus struct {
	// Branch is the environment branch name being validated.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// ProposedHydratedSha is the proposed hydrated commit SHA where the validation result is reported.
	// This comes from the PromotionStrategy's environment status.
	// The CommitStatus resource is created with this SHA, allowing the PromotionStrategy to gate
	// promotions based on the validation of the ACTIVE commit.
	// May be empty if the PromotionStrategy hasn't reconciled yet.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +required
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	ProposedHydratedSha string `json:"proposedHydratedSha"`

	// Phase represents the current validation state of the commit.
	// - "pending": validation has not completed, commit data is not yet available, or SHAs are empty
	// - "success": expression evaluated to true, validation passed
	// - "failure": expression evaluated to false, validation failed, or expression compilation failed
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// SCMChecks holds the results of each SCM check.
	// +required
	SCMChecks []SCMCheckResults `json:"scmCheckResults"`
}

type SCMCheckResults struct {
	// Name represents the name of the check in the SCM.
	Name string `json:"name"`
	// Status represents the status of the check.
	Status SCMCheckStatus `json:"status"`
	// URL is a link to where details on the check can be found in the SCM.
	URL string `json:"url"`
	// Filtered is whether the check was filtered by the expression provided in the spec.
	Filtered bool `json:"filtered"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SCMCheckCommitStatus is the Schema for the scmcheckcommitstatuses API
type SCMCheckCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of SCMCheckCommitStatus
	// +required
	Spec SCMCheckCommitStatusSpec `json:"spec"`

	// status defines the observed state of SCMCheckCommitStatus
	// +optional
	Status SCMCheckCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SCMCheckCommitStatusList contains a list of SCMCheckCommitStatus
type SCMCheckCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SCMCheckCommitStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &SCMCheckCommitStatus{}, &SCMCheckCommitStatusList{})
		return nil
	})
}
