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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("commitStatusPhaseChangedPredicate", func() {
	p := commitStatusPhaseChangedPredicate()

	commitStatus := func(sha string, phase promoterv1alpha1.CommitStatusPhase, key, description string) *promoterv1alpha1.CommitStatus {
		return &promoterv1alpha1.CommitStatus{
			Labels: map[string]string{promoterv1alpha1.CommitStatusLabel: key},
			Spec: promoterv1alpha1.CommitStatusSpec{
				Sha:         sha,
				Phase:       phase,
				Description: description,
			},
		}
	}

	It("passes creates except those from the informer's initial list", func() {
		cs := commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", "")
		Expect(p.Create(event.CreateEvent{Object: cs})).To(BeTrue())
		Expect(p.Create(event.CreateEvent{Object: cs, IsInInitialList: true})).To(BeFalse())
	})

	DescribeTable("filters updates to the changes a ChangeTransferPolicy acts on",
		func(oldCS, newCS *promoterv1alpha1.CommitStatus, want bool) {
			Expect(p.Update(event.UpdateEvent{ObjectOld: oldCS, ObjectNew: newCS})).To(Equal(want))
		},
		Entry("pending to success",
			commitStatus("abc", promoterv1alpha1.CommitPhasePending, "k", ""),
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""), true),
		Entry("success to failure",
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""),
			commitStatus("abc", promoterv1alpha1.CommitPhaseFailure, "k", ""), true),
		Entry("success to pending",
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""),
			commitStatus("abc", promoterv1alpha1.CommitPhasePending, "k", ""), true),
		Entry("same phase on a new SHA",
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""),
			commitStatus("def", promoterv1alpha1.CommitPhaseSuccess, "k", ""), true),
		Entry("commit-status key label change",
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""),
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "other", ""), true),
		Entry("description-only change",
			commitStatus("abc", promoterv1alpha1.CommitPhasePending, "k", "1/3 healthy"),
			commitStatus("abc", promoterv1alpha1.CommitPhasePending, "k", "2/3 healthy"), false),
		Entry("no change (for example a status-only write)",
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""),
			commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", ""), false),
	)

	It("passes deletes and drops generic events", func() {
		cs := commitStatus("abc", promoterv1alpha1.CommitPhaseSuccess, "k", "")
		Expect(p.Delete(event.DeleteEvent{Object: cs})).To(BeTrue())
		Expect(p.Generic(event.GenericEvent{Object: cs})).To(BeFalse())
	})
})

var _ = Describe("changeTransferPoliciesForShas", func() {
	var (
		ctx context.Context
		c   client.Client
	)

	ctp := func(namespace, name, activeSha, proposedSha string) *promoterv1alpha1.ChangeTransferPolicy {
		policy := &promoterv1alpha1.ChangeTransferPolicy{}
		policy.Namespace = namespace
		policy.Name = name
		policy.Status.Active.Hydrated.Sha = activeSha
		policy.Status.Proposed.Hydrated.Sha = proposedSha
		return policy
	}

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		c = fake.NewClientBuilder().WithScheme(scheme).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, ctpActiveHydratedShaIndex, func(o client.Object) []string {
				return []string{o.(*promoterv1alpha1.ChangeTransferPolicy).Status.Active.Hydrated.Sha} //nolint:forcetypeassert // test index
			}).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, ctpProposedHydratedShaIndex, func(o client.Object) []string {
				return []string{o.(*promoterv1alpha1.ChangeTransferPolicy).Status.Proposed.Hydrated.Sha} //nolint:forcetypeassert // test index
			}).
			WithObjects(
				ctp("ns1", "active-abc", "abc", "zzz"),
				ctp("ns2", "proposed-abc", "yyy", "abc"),
				ctp("ns1", "both-abc", "abc", "abc"),
				ctp("ns1", "on-def", "def", "xxx"),
				ctp("ns1", "unstarted", "", ""),
			).Build()
	})

	It("returns each policy whose active or proposed SHA matches, once, across namespaces", func() {
		Expect(changeTransferPoliciesForShas(ctx, c, "abc")).To(ConsistOf(
			types.NamespacedName{Namespace: "ns1", Name: "active-abc"},
			types.NamespacedName{Namespace: "ns2", Name: "proposed-abc"},
			types.NamespacedName{Namespace: "ns1", Name: "both-abc"},
		))
	})

	It("covers both the previous and the current SHA of a moved CommitStatus", func() {
		Expect(changeTransferPoliciesForShas(ctx, c, "abc", "def")).To(ConsistOf(
			types.NamespacedName{Namespace: "ns1", Name: "active-abc"},
			types.NamespacedName{Namespace: "ns2", Name: "proposed-abc"},
			types.NamespacedName{Namespace: "ns1", Name: "both-abc"},
			types.NamespacedName{Namespace: "ns1", Name: "on-def"},
		))
	})

	It("never looks up an empty SHA", func() {
		Expect(changeTransferPoliciesForShas(ctx, c, "")).To(BeEmpty())
		Expect(changeTransferPoliciesForShas(ctx, c, "", "def")).To(ConsistOf(
			types.NamespacedName{Namespace: "ns1", Name: "on-def"},
		))
	})
})
