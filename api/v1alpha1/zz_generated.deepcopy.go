//go:build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ApplicationsSelected) DeepCopyInto(out *ApplicationsSelected) {
	*out = *in
	if in.LastTransitionTime != nil {
		in, out := &in.LastTransitionTime, &out.LastTransitionTime
		*out = (*in).DeepCopy()
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ApplicationsSelected.
func (in *ApplicationsSelected) DeepCopy() *ApplicationsSelected {
	if in == nil {
		return nil
	}
	out := new(ApplicationsSelected)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ArgoCDCommitStatus) DeepCopyInto(out *ArgoCDCommitStatus) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ArgoCDCommitStatus.
func (in *ArgoCDCommitStatus) DeepCopy() *ArgoCDCommitStatus {
	if in == nil {
		return nil
	}
	out := new(ArgoCDCommitStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ArgoCDCommitStatus) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ArgoCDCommitStatusList) DeepCopyInto(out *ArgoCDCommitStatusList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ArgoCDCommitStatus, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ArgoCDCommitStatusList.
func (in *ArgoCDCommitStatusList) DeepCopy() *ArgoCDCommitStatusList {
	if in == nil {
		return nil
	}
	out := new(ArgoCDCommitStatusList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ArgoCDCommitStatusList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ArgoCDCommitStatusSpec) DeepCopyInto(out *ArgoCDCommitStatusSpec) {
	*out = *in
	out.PromotionStrategyRef = in.PromotionStrategyRef
	if in.ApplicationSelector != nil {
		in, out := &in.ApplicationSelector, &out.ApplicationSelector
		*out = new(v1.LabelSelector)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ArgoCDCommitStatusSpec.
func (in *ArgoCDCommitStatusSpec) DeepCopy() *ArgoCDCommitStatusSpec {
	if in == nil {
		return nil
	}
	out := new(ArgoCDCommitStatusSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ArgoCDCommitStatusStatus) DeepCopyInto(out *ArgoCDCommitStatusStatus) {
	*out = *in
	if in.ApplicationsSelected != nil {
		in, out := &in.ApplicationsSelected, &out.ApplicationsSelected
		*out = make([]ApplicationsSelected, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ArgoCDCommitStatusStatus.
func (in *ArgoCDCommitStatusStatus) DeepCopy() *ArgoCDCommitStatusStatus {
	if in == nil {
		return nil
	}
	out := new(ArgoCDCommitStatusStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeRequestPolicyCommitStatusPhase) DeepCopyInto(out *ChangeRequestPolicyCommitStatusPhase) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeRequestPolicyCommitStatusPhase.
func (in *ChangeRequestPolicyCommitStatusPhase) DeepCopy() *ChangeRequestPolicyCommitStatusPhase {
	if in == nil {
		return nil
	}
	out := new(ChangeRequestPolicyCommitStatusPhase)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeTransferPolicy) DeepCopyInto(out *ChangeTransferPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeTransferPolicy.
func (in *ChangeTransferPolicy) DeepCopy() *ChangeTransferPolicy {
	if in == nil {
		return nil
	}
	out := new(ChangeTransferPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ChangeTransferPolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeTransferPolicyList) DeepCopyInto(out *ChangeTransferPolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ChangeTransferPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeTransferPolicyList.
func (in *ChangeTransferPolicyList) DeepCopy() *ChangeTransferPolicyList {
	if in == nil {
		return nil
	}
	out := new(ChangeTransferPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ChangeTransferPolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeTransferPolicySpec) DeepCopyInto(out *ChangeTransferPolicySpec) {
	*out = *in
	out.RepositoryReference = in.RepositoryReference
	if in.AutoMerge != nil {
		in, out := &in.AutoMerge, &out.AutoMerge
		*out = new(bool)
		**out = **in
	}
	if in.ActiveCommitStatuses != nil {
		in, out := &in.ActiveCommitStatuses, &out.ActiveCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
	if in.ProposedCommitStatuses != nil {
		in, out := &in.ProposedCommitStatuses, &out.ProposedCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
	if in.OpenPullerRequestFilter != nil {
		in, out := &in.OpenPullerRequestFilter, &out.OpenPullerRequestFilter
		*out = new(OpenPullRequestFilter)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeTransferPolicySpec.
func (in *ChangeTransferPolicySpec) DeepCopy() *ChangeTransferPolicySpec {
	if in == nil {
		return nil
	}
	out := new(ChangeTransferPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ChangeTransferPolicyStatus) DeepCopyInto(out *ChangeTransferPolicyStatus) {
	*out = *in
	in.Proposed.DeepCopyInto(&out.Proposed)
	in.Active.DeepCopyInto(&out.Active)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ChangeTransferPolicyStatus.
func (in *ChangeTransferPolicyStatus) DeepCopy() *ChangeTransferPolicyStatus {
	if in == nil {
		return nil
	}
	out := new(ChangeTransferPolicyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitBranchState) DeepCopyInto(out *CommitBranchState) {
	*out = *in
	in.Dry.DeepCopyInto(&out.Dry)
	in.Hydrated.DeepCopyInto(&out.Hydrated)
	if in.CommitStatuses != nil {
		in, out := &in.CommitStatuses, &out.CommitStatuses
		*out = make([]ChangeRequestPolicyCommitStatusPhase, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitBranchState.
func (in *CommitBranchState) DeepCopy() *CommitBranchState {
	if in == nil {
		return nil
	}
	out := new(CommitBranchState)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitShaState) DeepCopyInto(out *CommitShaState) {
	*out = *in
	in.CommitTime.DeepCopyInto(&out.CommitTime)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitShaState.
func (in *CommitShaState) DeepCopy() *CommitShaState {
	if in == nil {
		return nil
	}
	out := new(CommitShaState)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitStatus) DeepCopyInto(out *CommitStatus) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitStatus.
func (in *CommitStatus) DeepCopy() *CommitStatus {
	if in == nil {
		return nil
	}
	out := new(CommitStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CommitStatus) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitStatusList) DeepCopyInto(out *CommitStatusList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]CommitStatus, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitStatusList.
func (in *CommitStatusList) DeepCopy() *CommitStatusList {
	if in == nil {
		return nil
	}
	out := new(CommitStatusList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CommitStatusList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitStatusSelector) DeepCopyInto(out *CommitStatusSelector) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitStatusSelector.
func (in *CommitStatusSelector) DeepCopy() *CommitStatusSelector {
	if in == nil {
		return nil
	}
	out := new(CommitStatusSelector)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitStatusSpec) DeepCopyInto(out *CommitStatusSpec) {
	*out = *in
	out.RepositoryReference = in.RepositoryReference
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitStatusSpec.
func (in *CommitStatusSpec) DeepCopy() *CommitStatusSpec {
	if in == nil {
		return nil
	}
	out := new(CommitStatusSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommitStatusStatus) DeepCopyInto(out *CommitStatusStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommitStatusStatus.
func (in *CommitStatusStatus) DeepCopy() *CommitStatusStatus {
	if in == nil {
		return nil
	}
	out := new(CommitStatusStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Environment) DeepCopyInto(out *Environment) {
	*out = *in
	if in.AutoMerge != nil {
		in, out := &in.AutoMerge, &out.AutoMerge
		*out = new(bool)
		**out = **in
	}
	if in.ActiveCommitStatuses != nil {
		in, out := &in.ActiveCommitStatuses, &out.ActiveCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
	if in.ProposedCommitStatuses != nil {
		in, out := &in.ProposedCommitStatuses, &out.ProposedCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Environment.
func (in *Environment) DeepCopy() *Environment {
	if in == nil {
		return nil
	}
	out := new(Environment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EnvironmentStatus) DeepCopyInto(out *EnvironmentStatus) {
	*out = *in
	in.Active.DeepCopyInto(&out.Active)
	in.Proposed.DeepCopyInto(&out.Proposed)
	if in.LastHealthyDryShas != nil {
		in, out := &in.LastHealthyDryShas, &out.LastHealthyDryShas
		*out = make([]HealthyDryShas, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EnvironmentStatus.
func (in *EnvironmentStatus) DeepCopy() *EnvironmentStatus {
	if in == nil {
		return nil
	}
	out := new(EnvironmentStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Fake) DeepCopyInto(out *Fake) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Fake.
func (in *Fake) DeepCopy() *Fake {
	if in == nil {
		return nil
	}
	out := new(Fake)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitHub) DeepCopyInto(out *GitHub) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitHub.
func (in *GitHub) DeepCopy() *GitHub {
	if in == nil {
		return nil
	}
	out := new(GitHub)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitRepository) DeepCopyInto(out *GitRepository) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitRepository.
func (in *GitRepository) DeepCopy() *GitRepository {
	if in == nil {
		return nil
	}
	out := new(GitRepository)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GitRepository) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitRepositoryList) DeepCopyInto(out *GitRepositoryList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]GitRepository, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitRepositoryList.
func (in *GitRepositoryList) DeepCopy() *GitRepositoryList {
	if in == nil {
		return nil
	}
	out := new(GitRepositoryList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GitRepositoryList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitRepositorySpec) DeepCopyInto(out *GitRepositorySpec) {
	*out = *in
	out.ScmProviderRef = in.ScmProviderRef
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitRepositorySpec.
func (in *GitRepositorySpec) DeepCopy() *GitRepositorySpec {
	if in == nil {
		return nil
	}
	out := new(GitRepositorySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitRepositoryStatus) DeepCopyInto(out *GitRepositoryStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitRepositoryStatus.
func (in *GitRepositoryStatus) DeepCopy() *GitRepositoryStatus {
	if in == nil {
		return nil
	}
	out := new(GitRepositoryStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HealthyDryShas) DeepCopyInto(out *HealthyDryShas) {
	*out = *in
	in.Time.DeepCopyInto(&out.Time)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HealthyDryShas.
func (in *HealthyDryShas) DeepCopy() *HealthyDryShas {
	if in == nil {
		return nil
	}
	out := new(HealthyDryShas)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ObjectReference) DeepCopyInto(out *ObjectReference) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ObjectReference.
func (in *ObjectReference) DeepCopy() *ObjectReference {
	if in == nil {
		return nil
	}
	out := new(ObjectReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *OpenPullRequestFilter) DeepCopyInto(out *OpenPullRequestFilter) {
	*out = *in
	if in.Paths != nil {
		in, out := &in.Paths, &out.Paths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new OpenPullRequestFilter.
func (in *OpenPullRequestFilter) DeepCopy() *OpenPullRequestFilter {
	if in == nil {
		return nil
	}
	out := new(OpenPullRequestFilter)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategy) DeepCopyInto(out *PromotionStrategy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategy.
func (in *PromotionStrategy) DeepCopy() *PromotionStrategy {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PromotionStrategy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategyBranchStateStatus) DeepCopyInto(out *PromotionStrategyBranchStateStatus) {
	*out = *in
	in.Dry.DeepCopyInto(&out.Dry)
	in.Hydrated.DeepCopyInto(&out.Hydrated)
	out.CommitStatus = in.CommitStatus
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategyBranchStateStatus.
func (in *PromotionStrategyBranchStateStatus) DeepCopy() *PromotionStrategyBranchStateStatus {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategyBranchStateStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategyCommitStatus) DeepCopyInto(out *PromotionStrategyCommitStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategyCommitStatus.
func (in *PromotionStrategyCommitStatus) DeepCopy() *PromotionStrategyCommitStatus {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategyCommitStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategyList) DeepCopyInto(out *PromotionStrategyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PromotionStrategy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategyList.
func (in *PromotionStrategyList) DeepCopy() *PromotionStrategyList {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PromotionStrategyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategySpec) DeepCopyInto(out *PromotionStrategySpec) {
	*out = *in
	out.RepositoryReference = in.RepositoryReference
	if in.ActiveCommitStatuses != nil {
		in, out := &in.ActiveCommitStatuses, &out.ActiveCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
	if in.ProposedCommitStatuses != nil {
		in, out := &in.ProposedCommitStatuses, &out.ProposedCommitStatuses
		*out = make([]CommitStatusSelector, len(*in))
		copy(*out, *in)
	}
	if in.Environments != nil {
		in, out := &in.Environments, &out.Environments
		*out = make([]Environment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.OpenPullRequestFilter != nil {
		in, out := &in.OpenPullRequestFilter, &out.OpenPullRequestFilter
		*out = new(OpenPullRequestFilter)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategySpec.
func (in *PromotionStrategySpec) DeepCopy() *PromotionStrategySpec {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PromotionStrategyStatus) DeepCopyInto(out *PromotionStrategyStatus) {
	*out = *in
	if in.Environments != nil {
		in, out := &in.Environments, &out.Environments
		*out = make([]EnvironmentStatus, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PromotionStrategyStatus.
func (in *PromotionStrategyStatus) DeepCopy() *PromotionStrategyStatus {
	if in == nil {
		return nil
	}
	out := new(PromotionStrategyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PullRequest) DeepCopyInto(out *PullRequest) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PullRequest.
func (in *PullRequest) DeepCopy() *PullRequest {
	if in == nil {
		return nil
	}
	out := new(PullRequest)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PullRequest) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PullRequestList) DeepCopyInto(out *PullRequestList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PullRequest, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PullRequestList.
func (in *PullRequestList) DeepCopy() *PullRequestList {
	if in == nil {
		return nil
	}
	out := new(PullRequestList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PullRequestList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PullRequestSpec) DeepCopyInto(out *PullRequestSpec) {
	*out = *in
	out.RepositoryReference = in.RepositoryReference
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PullRequestSpec.
func (in *PullRequestSpec) DeepCopy() *PullRequestSpec {
	if in == nil {
		return nil
	}
	out := new(PullRequestSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PullRequestStatus) DeepCopyInto(out *PullRequestStatus) {
	*out = *in
	in.PRCreationTime.DeepCopyInto(&out.PRCreationTime)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PullRequestStatus.
func (in *PullRequestStatus) DeepCopy() *PullRequestStatus {
	if in == nil {
		return nil
	}
	out := new(PullRequestStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RevertCommit) DeepCopyInto(out *RevertCommit) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RevertCommit.
func (in *RevertCommit) DeepCopy() *RevertCommit {
	if in == nil {
		return nil
	}
	out := new(RevertCommit)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RevertCommit) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RevertCommitList) DeepCopyInto(out *RevertCommitList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RevertCommit, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RevertCommitList.
func (in *RevertCommitList) DeepCopy() *RevertCommitList {
	if in == nil {
		return nil
	}
	out := new(RevertCommitList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RevertCommitList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RevertCommitSpec) DeepCopyInto(out *RevertCommitSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RevertCommitSpec.
func (in *RevertCommitSpec) DeepCopy() *RevertCommitSpec {
	if in == nil {
		return nil
	}
	out := new(RevertCommitSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RevertCommitStatus) DeepCopyInto(out *RevertCommitStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RevertCommitStatus.
func (in *RevertCommitStatus) DeepCopy() *RevertCommitStatus {
	if in == nil {
		return nil
	}
	out := new(RevertCommitStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScmProvider) DeepCopyInto(out *ScmProvider) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScmProvider.
func (in *ScmProvider) DeepCopy() *ScmProvider {
	if in == nil {
		return nil
	}
	out := new(ScmProvider)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ScmProvider) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScmProviderList) DeepCopyInto(out *ScmProviderList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ScmProvider, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScmProviderList.
func (in *ScmProviderList) DeepCopy() *ScmProviderList {
	if in == nil {
		return nil
	}
	out := new(ScmProviderList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ScmProviderList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScmProviderSpec) DeepCopyInto(out *ScmProviderSpec) {
	*out = *in
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(corev1.LocalObjectReference)
		**out = **in
	}
	if in.GitHub != nil {
		in, out := &in.GitHub, &out.GitHub
		*out = new(GitHub)
		**out = **in
	}
	if in.Fake != nil {
		in, out := &in.Fake, &out.Fake
		*out = new(Fake)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScmProviderSpec.
func (in *ScmProviderSpec) DeepCopy() *ScmProviderSpec {
	if in == nil {
		return nil
	}
	out := new(ScmProviderSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScmProviderStatus) DeepCopyInto(out *ScmProviderStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScmProviderStatus.
func (in *ScmProviderStatus) DeepCopy() *ScmProviderStatus {
	if in == nil {
		return nil
	}
	out := new(ScmProviderStatus)
	in.DeepCopyInto(out)
	return out
}
