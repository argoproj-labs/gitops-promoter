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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GitCommitStatusSpec defines the desired state of GitCommitStatus
type GitCommitStatusSpec struct {
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

	// Description is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// If not specified, defaults to empty string.
	// +optional
	Description string `json:"description,omitempty"`

	// Target specifies which commit SHA to validate with the expression.
	// - "active": Validates the currently active/deployed commit (default behavior)
	// - "proposed": Validates the proposed commit that will be promoted
	//
	// The validation result is always reported on the PROPOSED commit (for gating), but this field
	// controls which commit's data is used in the expression evaluation.
	//
	// Examples:
	//   target: "active" - "Don't promote if a revert commit is detected"
	//   target: "proposed" - "Don't promote unless new commit follows naming convention"
	//
	// +optional
	// +kubebuilder:default="active"
	// +kubebuilder:validation:Enum=active;proposed
	Target string `json:"target,omitempty"`

	// Expression is evaluated using the expr library (github.com/expr-lang/expr) against commit data
	// for environments in the referenced PromotionStrategy.
	// The expression must return a boolean value where true indicates the validation passed.
	//
	// The commit validated is determined by the Target field:
	// - "active" (default): Validates the ACTIVE (currently deployed) commit
	// - "proposed": Validates the PROPOSED commit (what will be promoted)
	//
	// The validation result is always reported on the PROPOSED commit to enable promotion gating.
	//
	// Use Cases by Mode:
	//   Active mode: State-based gating - validate current environment before allowing promotion
	//     - "Don't promote until active commit has required sign-offs"
	//     - "Verify current deployment meets compliance before next promotion"
	//     - "Ensure active commit is not a revert before promoting"
	//
	//   Proposed mode: Change-based gating - validate the incoming change itself
	//     - "Don't promote unless new commit follows naming convention"
	//     - "Ensure proposed commit has proper JIRA ticket reference"
	//     - "Require specific author for proposed changes"
	//
	//
	// Available variables in the expression context:
	//   - Commit.SHA (string): the commit SHA being validated (active or proposed based on Target)
	//   - Commit.Subject (string): the first line of the commit message
	//   - Commit.Body (string): the commit message body (everything after the subject line)
	//   - Commit.Author (string): commit author email address
	//   - Commit.Trailers (map[string][]string): git trailers parsed from commit message
	//
	// +required
	Expression string `json:"expression"`
}

// GitCommitStatusStatus defines the observed state of GitCommitStatus.
type GitCommitStatusStatus struct {
	// Environments holds the validation results for each environment where this validation applies.
	// Each entry corresponds to an environment from the PromotionStrategy where the Key matches
	// either global or environment-specific proposedCommitStatuses.
	//
	// The controller validates the commit specified by Target ("active" or "proposed")
	// but the CommitStatus is always reported on the PROPOSED commit for promotion gating.
	// Each environment entry tracks both the ProposedHydratedSha (where status is reported) and the
	// ActiveHydratedSha, with TargetedSha indicating which one was actually evaluated.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []GitCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of the GitCommitStatus's state.
	// Standard condition types include "Ready" which aggregates the status of all environments.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// GitCommitStatusEnvironmentStatus defines the observed validation status for a specific environment.
type GitCommitStatusEnvironmentStatus struct {
	// Branch is the environment branch name being validated.
	// +required
	Branch string `json:"branch"`

	// ProposedHydratedSha is the proposed hydrated commit SHA where the validation result is reported.
	// This comes from the PromotionStrategy's environment status.
	// The CommitStatus resource is created with this SHA, allowing the PromotionStrategy to gate
	// promotions based on the validation of the ACTIVE commit.
	// May be empty if the PromotionStrategy hasn't reconciled yet.
	// +required
	ProposedHydratedSha string `json:"proposedHydratedSha"`

	// ActiveHydratedSha is the currently active (deployed) hydrated commit SHA that was validated.
	// This comes from the PromotionStrategy's environment status.
	// The expression is evaluated against THIS commit's data, not the proposed commit.
	// May be empty if the PromotionStrategy hasn't reconciled yet.
	// +optional
	ActiveHydratedSha string `json:"activeHydratedSha,omitempty"`

	// TargetedSha is the commit SHA that was actually validated by the expression.
	// This will match either ProposedHydratedSha or ActiveHydratedSha depending on
	// the Target setting ("proposed" or "active").
	// This field clarifies which commit's data was used in the expression evaluation.
	// +optional
	TargetedSha string `json:"targetedSha,omitempty"`

	// Phase represents the current validation state of the commit.
	// - "pending": validation has not completed, commit data is not yet available, or SHAs are empty
	// - "success": expression evaluated to true, validation passed
	// - "failure": expression evaluated to false, validation failed, or expression compilation failed
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// ExpressionResult contains the boolean result of the expression evaluation.
	// Only set when the expression successfully evaluates to a boolean.
	// nil indicates the expression has not yet been evaluated, failed to compile, or failed to evaluate.
	// +optional
	ExpressionResult *bool `json:"expressionResult,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Validates",type=string,JSONPath=`.spec.target`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GitCommitStatus is the Schema for the gitcommitstatuses API.
//
// It validates commits from PromotionStrategy environments using configurable expressions
// and creates CommitStatus resources with the validation results.
//
// Use the Target field to control which commit is validated:
// - "active" (default): Validates the currently deployed commit
// - "proposed": Validates the incoming commit
//
// The validation result is always reported on the PROPOSED commit to enable promotion gating,
// regardless of which commit was validated.
//
// Workflow:
//  1. Controller reads PromotionStrategy to get ProposedHydratedSha and ActiveHydratedSha
//  2. Controller selects SHA to validate based on Target field
//  3. Controller fetches commit data (subject, body, author, trailers) for selected SHA
//  4. Controller evaluates expression against selected commit data
//  5. Controller creates/updates CommitStatus with result attached to PROPOSED SHA
//  6. PromotionStrategy checks CommitStatus on PROPOSED SHA before allowing promotion
//
// Common use cases:
//   - "Ensure active commit is not a revert before promoting"
type GitCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of GitCommitStatus
	// +required
	Spec GitCommitStatusSpec `json:"spec"`

	// status defines the observed state of GitCommitStatus
	// +optional
	Status GitCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GitCommitStatusList contains a list of GitCommitStatus
type GitCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the GitCommitStatus.
func (g *GitCommitStatus) GetConditions() *[]metav1.Condition {
	return &g.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&GitCommitStatus{}, &GitCommitStatusList{})
}
