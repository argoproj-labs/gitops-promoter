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
	// The controller will validate commits from ALL environments defined in the referenced PromotionStrategy
	// using the same expression.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the unique identifier for this validation rule.
	// It is used as the commit status key and in status messages.
	// This becomes the key used in PromotionStrategy's activeCommitStatuses or proposedCommitStatuses.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// Description is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// If not specified, defaults to "Commit validation".
	// Keep this concise and avoid special characters that may not be supported by all SCM providers.
	// +optional
	// +kubebuilder:validation:MaxLength=140
	Description string `json:"description,omitempty"`

	// Expression is evaluated using the expr library (github.com/expr-lang/expr) against commit data
	// for ALL environments in the referenced PromotionStrategy.
	// The expression must return a boolean value where true indicates the validation passed.
	//
	// Available variables in the expression context:
	//   - Commit.SHA (string): the proposed hydrated commit SHA being validated
	//   - Commit.Subject (string): the first line of the commit message
	//   - Commit.Body (string): the commit message body (everything after the subject line)
	//   - Commit.Author (string): commit author email address
	//   - Commit.Committer (string): committer email address
	//   - Commit.Trailers (map[string][]string): git trailers parsed from commit message
	//
	// The expr library provides built-in functions and operators:
	//   - String operations: startsWith(s, prefix), endsWith(s, suffix), contains(s, substr)
	//   - Regex matching: string matches "pattern" (infix operator)
	//   - Logical operators: &&, ||, !
	//   - Comparison: ==, !=, <, >, <=, >=
	//   - Collections: "key" in map to check map key existence, len() for length
	//
	// Example expressions:
	//   'endsWith(Commit.Author, "@example.com")'
	//   '"Signed-off-by" in Commit.Trailers'
	//   'contains(Commit.Body, "JIRA-") && "Reviewed-by" in Commit.Trailers'
	//   'len(Commit.Trailers["Reviewed-by"]) >= 2'
	//   'Commit.Subject matches "^(feat|fix|docs):"'
	//   'startsWith(Commit.Subject, "Revert")'
	//   'Commit.Author == "user@example.com"'
	//
	// +required
	Expression string `json:"expression"`
}

// GitCommitStatusStatus defines the observed state of GitCommitStatus.
type GitCommitStatusStatus struct {
	// Environments holds the validation results for each configured environment.
	// Each entry corresponds to an environment defined in the spec and contains
	// the validation result for that environment's proposed hydrated commit.
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

	// ProposedHydratedSha is the proposed hydrated commit SHA that was validated.
	// This comes from the PromotionStrategy's environment status.
	// +required
	ProposedHydratedSha string `json:"proposedHydratedSha"`

	// ActiveHydratedSha is the currently active (deployed) hydrated commit SHA.
	// This comes from the PromotionStrategy's environment status.
	// +optional
	ActiveHydratedSha string `json:"activeHydratedSha,omitempty"`

	// Phase represents the current validation state of the commit.
	// - "pending": validation has not completed or commit data is not yet available
	// - "success": expression evaluated to true, validation passed
	// - "failure": expression evaluated to false, validation failed
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// Message provides a human-readable description of the validation result.
	// This includes details about why validation passed or failed.
	// +optional
	Message string `json:"message,omitempty"`

	// ExpressionResult contains the boolean result of the expression evaluation.
	// Only set when the expression successfully evaluates to a boolean.
	// nil indicates the expression has not yet been evaluated or failed to evaluate.
	// +optional
	ExpressionResult *bool `json:"expressionResult,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GitCommitStatus is the Schema for the gitcommitstatuses API.
// It validates commits from PromotionStrategy environments using configurable expressions
// and creates CommitStatus resources with the validation results.
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
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the GitCommitStatus.
func (g *GitCommitStatus) GetConditions() *[]metav1.Condition {
	return &g.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&GitCommitStatus{}, &GitCommitStatusList{})
}
