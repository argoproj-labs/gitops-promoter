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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PromotionStrategyDetails is a read-only, server-computed bundle that joins a
// PromotionStrategy with all of its related resources. It is served by the
// dashboard aggregation layer (an extension apiserver) and is not persisted in
// etcd. The name of a PromotionStrategyDetails always matches the name of the
// PromotionStrategy it describes (1:1 mapping within a namespace).
//
// Secrets are never included in the bundle.
//
// Embedded objects use runtime.RawExtension so the served OpenAPI schema is opaque
// (type: object, no $refs into the promoter/core type universe). This keeps the
// aggregated API from injecting a large, cross-referencing schema into the
// cluster-wide OpenAPI document (which breaks strict consumers like Argo CD). The
// wire JSON is unchanged: each RawExtension marshals its underlying object inline.
type PromotionStrategyDetails struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// PromotionStrategy is the source PromotionStrategy this bundle describes.
	PromotionStrategy runtime.RawExtension `json:"promotionStrategy"`

	// ChangeTransferPolicies are the CTPs owned by the PromotionStrategy
	// (selected by the promoter.argoproj.io/promotion-strategy label).
	ChangeTransferPolicies []runtime.RawExtension `json:"changeTransferPolicies,omitempty"`

	// PullRequests are the PullRequests associated with the PromotionStrategy
	// (selected by the promoter.argoproj.io/promotion-strategy label).
	PullRequests []runtime.RawExtension `json:"pullRequests,omitempty"`

	// CommitStatuses are the base CommitStatus resources associated with the
	// PromotionStrategy (selected by the promoter.argoproj.io/promotion-strategy label).
	CommitStatuses []runtime.RawExtension `json:"commitStatuses,omitempty"`

	// ArgoCDCommitStatuses are the ArgoCDCommitStatus managers that reference the PromotionStrategy.
	ArgoCDCommitStatuses []runtime.RawExtension `json:"argoCDCommitStatuses,omitempty"`

	// GitCommitStatuses are the GitCommitStatus managers that reference the PromotionStrategy.
	GitCommitStatuses []runtime.RawExtension `json:"gitCommitStatuses,omitempty"`

	// TimedCommitStatuses are the TimedCommitStatus managers that reference the PromotionStrategy.
	TimedCommitStatuses []runtime.RawExtension `json:"timedCommitStatuses,omitempty"`

	// WebRequestCommitStatuses are the WebRequestCommitStatus managers that reference the PromotionStrategy.
	WebRequestCommitStatuses []runtime.RawExtension `json:"webRequestCommitStatuses,omitempty"`

	// GitRepository is the GitRepository referenced by the PromotionStrategy, if resolvable.
	GitRepository *runtime.RawExtension `json:"gitRepository,omitempty"`

	// ScmProvider is the namespaced ScmProvider referenced by the GitRepository, if applicable.
	// The credentials Secret referenced by the provider is never resolved or included.
	ScmProvider *runtime.RawExtension `json:"scmProvider,omitempty"`

	// ClusterScmProvider is the cluster-scoped ScmProvider referenced by the GitRepository, if applicable.
	// The credentials Secret referenced by the provider is never resolved or included.
	ClusterScmProvider *runtime.RawExtension `json:"clusterScmProvider,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PromotionStrategyDetailsList contains a list of PromotionStrategyDetails.
type PromotionStrategyDetailsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PromotionStrategyDetails `json:"items"`
}
