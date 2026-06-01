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

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PromotionStrategyDetails is a read-only, server-computed bundle that joins a
// PromotionStrategy with all of its related resources. It is served by the
// dashboard aggregation layer (an extension apiserver) and is not persisted in
// etcd. The name of a PromotionStrategyDetails always matches the name of the
// PromotionStrategy it describes (1:1 mapping within a namespace).
//
// Secrets are never included in the bundle.
type PromotionStrategyDetails struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// PromotionStrategy is the source PromotionStrategy this bundle describes.
	PromotionStrategy promoterv1alpha1.PromotionStrategy `json:"promotionStrategy"`

	// ChangeTransferPolicies are the CTPs owned by the PromotionStrategy
	// (selected by the promoter.argoproj.io/promotion-strategy label).
	ChangeTransferPolicies []promoterv1alpha1.ChangeTransferPolicy `json:"changeTransferPolicies,omitempty"`

	// PullRequests are the PullRequests associated with the PromotionStrategy
	// (selected by the promoter.argoproj.io/promotion-strategy label).
	PullRequests []promoterv1alpha1.PullRequest `json:"pullRequests,omitempty"`

	// CommitStatuses are the base CommitStatus resources associated with the
	// PromotionStrategy (selected by the promoter.argoproj.io/promotion-strategy label).
	CommitStatuses []promoterv1alpha1.CommitStatus `json:"commitStatuses,omitempty"`

	// ArgoCDCommitStatuses are the ArgoCDCommitStatus managers that reference the PromotionStrategy.
	ArgoCDCommitStatuses []promoterv1alpha1.ArgoCDCommitStatus `json:"argoCDCommitStatuses,omitempty"`

	// GitCommitStatuses are the GitCommitStatus managers that reference the PromotionStrategy.
	GitCommitStatuses []promoterv1alpha1.GitCommitStatus `json:"gitCommitStatuses,omitempty"`

	// TimedCommitStatuses are the TimedCommitStatus managers that reference the PromotionStrategy.
	TimedCommitStatuses []promoterv1alpha1.TimedCommitStatus `json:"timedCommitStatuses,omitempty"`

	// WebRequestCommitStatuses are the WebRequestCommitStatus managers that reference the PromotionStrategy.
	WebRequestCommitStatuses []promoterv1alpha1.WebRequestCommitStatus `json:"webRequestCommitStatuses,omitempty"`

	// GitRepository is the GitRepository referenced by the PromotionStrategy, if resolvable.
	GitRepository *promoterv1alpha1.GitRepository `json:"gitRepository,omitempty"`

	// ScmProvider is the namespaced ScmProvider referenced by the GitRepository, if applicable.
	// The credentials Secret referenced by the provider is never resolved or included.
	ScmProvider *promoterv1alpha1.ScmProvider `json:"scmProvider,omitempty"`

	// ClusterScmProvider is the cluster-scoped ScmProvider referenced by the GitRepository, if applicable.
	// The credentials Secret referenced by the provider is never resolved or included.
	ClusterScmProvider *promoterv1alpha1.ClusterScmProvider `json:"clusterScmProvider,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PromotionStrategyDetailsList contains a list of PromotionStrategyDetails.
type PromotionStrategyDetailsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PromotionStrategyDetails `json:"items"`
}
