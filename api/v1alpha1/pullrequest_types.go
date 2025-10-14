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

// PullRequestSpec defines the desired state of PullRequest
type PullRequestSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RepositoryReference indicates what repository to open the PR on.
	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`
	// Title is the title of the pull request.
	// +kubebuilder:validation:Required
	Title string `json:"title"`
	// TargetBranch is the head the git reference we are merging from Head ---> Base
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	TargetBranch string `json:"targetBranch"`
	// SourceBranch is the base the git reference that we are merging into Head ---> Base
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	SourceBranch string `json:"sourceBranch"`
	// Description is the description body of the pull/merge request
	Description string `json:"description,omitempty"`
	// Commit contains configuration for how we will merge/squash/etc the pull request.
	Commit CommitConfiguration `json:"commit,omitempty"`
	// MergeSha is the commit SHA that the head branch must match before the PR can be merged.
	// This prevents a race condition where a PR is merged with a different commit than intended.
	// +kubebuilder:validation:Required
	MergeSha string `json:"mergeSha"`
	// State of the merge request closed/merged/open
	// +kubebuilder:default:=open
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=closed;merged;open
	State PullRequestState `json:"state"`
}

// CommitConfiguration defines the commit configuration for how we will merge/squash/etc the pull request.
type CommitConfiguration struct {
	// Message is the commit message that will be written for the commit that's made when merging the PR.
	Message string `json:"message"`
}

// PullRequestStatus defines the observed state of PullRequest
type PullRequestStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ID the id of the pull request
	ID string `json:"id,omitempty"`
	// State of the merge request closed/merged/open
	// +kubebuilder:validation:Enum="";closed;merged;open
	State PullRequestState `json:"state,omitempty"`
	// PRCreationTime the time the PR was created
	PRCreationTime metav1.Time `json:"prCreationTime,omitempty"`
	// Url is the URL of the pull request.
	Url string `json:"url,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// GetConditions returns the conditions of the PullRequest.
func (ps *PullRequest) GetConditions() *[]metav1.Condition {
	return &ps.Status.Conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PullRequest is the Schema for the pullrequests API
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.status.id`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
type PullRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PullRequestSpec   `json:"spec,omitempty"`
	Status PullRequestStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PullRequestList contains a list of PullRequest
type PullRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PullRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PullRequest{}, &PullRequestList{})
}

// PullRequestState represents the state of a pull request.
type PullRequestState string

const (
	// PullRequestClosed indicates that the pull request is closed.
	PullRequestClosed PullRequestState = "closed"
	// PullRequestOpen indicates that the pull request is open.
	PullRequestOpen PullRequestState = "open"
	// PullRequestMerged indicates that the pull request has been merged.
	PullRequestMerged PullRequestState = "merged"
)
