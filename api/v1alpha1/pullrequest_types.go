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
	"crypto/sha1"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PullRequestSpec defines the desired state of PullRequest
type PullRequestSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RepositoryReference what repository to open the PR on.
	// +kubebuilder:validation:Required
	RepositoryReference *Repository `json:"repository"`
	// Title is the title of the pull request.
	// +kubebuilder:validation:Required
	Title string `json:"title"`
	// Head the git reference we are merging from Head ---> Base
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	TargetBranch string `json:"targetBranch"`
	// Base the git reference that we are merging into Head ---> Base
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	SourceBranch string `json:"sourceBranch"`
	// Body the description body of the pull/merge request
	Description string `json:"description,omitempty"`
	// State of the merge request closed/merged/open
	// +kubebuilder:default:=open
	// +kubebuilder:validation:Required
	State PullRequestState `json:"state"`
}

// PullRequestStatus defines the observed state of PullRequest
type PullRequestStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ID the id of the pull request
	ID string `json:"id,omitempty"`
	// State of the merge request closed/merged/open
	State PullRequestState `json:"state,omitempty"`
	// SpecHash used to track if we need to update, should maybe use observedGeneration pattern
	SpecHash string `json:"specHash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PullRequest is the Schema for the pullrequests API
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

type PullRequestState string

const (
	PullRequestClosed PullRequestState = "closed"
	PullRequestOpen   PullRequestState = "open"
	PullRequestMerged PullRequestState = "merged"
)

func (pr PullRequest) Hash() (string, error) {
	jsonSpec, err := json.Marshal(pr.Spec)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(jsonSpec)
	return fmt.Sprintf("%x", sum), nil
}
