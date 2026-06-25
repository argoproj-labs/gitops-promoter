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

// ScmLabelsSpec configures dynamic SCM pull request labels via an expression.
type ScmLabelsSpec struct {
	// Expression is evaluated using the expr library (github.com/expr-lang/expr) against
	// ChangeTransferPolicy status and spec. It must return a list of SCM label name strings.
	//
	// Available variables:
	//   - Status: ChangeTransferPolicy status (Proposed/Active commit statuses, branch SHAs, etc.)
	//   - Spec: ChangeTransferPolicy spec (ActiveBranch, ProposedBranch, etc.)
	//   - PromotionStrategy: owning PromotionStrategy spec and status when available
	//
	// Each returned label name must satisfy the same validation as PullRequest.spec.labels
	// (non-empty, max 50 characters, no newlines, max 10 labels, unique).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	Expression string `json:"expression"`
}

// PullRequestPolicySpec configures SCM pull request behavior for a promotion policy.
type PullRequestPolicySpec struct {
	// Labels configures dynamic SCM labels applied to promotion pull requests.
	// +kubebuilder:validation:Optional
	Labels *ScmLabelsSpec `json:"labels,omitempty"`
}
