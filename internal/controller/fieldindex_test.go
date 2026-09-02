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

package controller

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// registeredGateCommitStatusTypes lists every gate manager type in api/v1alpha1.
// When you add a new Spec.PromotionStrategyRef gate, append its struct type here,
// register it with SchemeBuilder, and add a matching DescribeTable Entry below.
// Discovery tests assert exact bidirectional sync with GateCommitStatusKinds().
var registeredGateCommitStatusTypes = []reflect.Type{
	reflect.TypeFor[promoterv1alpha1.ArgoCDCommitStatus](),
	reflect.TypeFor[promoterv1alpha1.GitCommitStatus](),
	reflect.TypeFor[promoterv1alpha1.TimedCommitStatus](),
	reflect.TypeFor[promoterv1alpha1.WebRequestCommitStatus](),
	reflect.TypeFor[promoterv1alpha1.ScheduledCommitStatus](),
}

var _ = Describe("IsPromotionStrategyRefGateType", func() {
	DescribeTable("identifies gate struct types independently of scheme discovery",
		func(typ reflect.Type, want bool) {
			Expect(IsPromotionStrategyRefGateType(typ)).To(Equal(want))
		},
		Entry("ArgoCDCommitStatus", reflect.TypeFor[promoterv1alpha1.ArgoCDCommitStatus](), true),
		Entry("GitCommitStatus", reflect.TypeFor[promoterv1alpha1.GitCommitStatus](), true),
		Entry("TimedCommitStatus", reflect.TypeFor[promoterv1alpha1.TimedCommitStatus](), true),
		Entry("WebRequestCommitStatus", reflect.TypeFor[promoterv1alpha1.WebRequestCommitStatus](), true),
		Entry("ScheduledCommitStatus", reflect.TypeFor[promoterv1alpha1.ScheduledCommitStatus](), true),
		Entry("PromotionStrategy", reflect.TypeFor[promoterv1alpha1.PromotionStrategy](), false),
		Entry("ChangeTransferPolicy", reflect.TypeFor[promoterv1alpha1.ChangeTransferPolicy](), false),
		Entry("CommitStatus", reflect.TypeFor[promoterv1alpha1.CommitStatus](), false),
		Entry("PullRequest", reflect.TypeFor[promoterv1alpha1.PullRequest](), false),
		Entry("GitRepository", reflect.TypeFor[promoterv1alpha1.GitRepository](), false),
	)
})

var _ = Describe("GateCommitStatusKinds", func() {
	It("discovers every registered PromotionStrategyRef gate type from the scheme", func() {
		gates := GateCommitStatusKinds()
		Expect(gates).NotTo(BeEmpty(),
			"GateCommitStatusKinds returned nothing; check SchemeBuilder registration in api/v1alpha1")

		discovered := make(map[reflect.Type]struct{}, len(gates))
		for _, obj := range gates {
			discovered[reflect.TypeOf(obj).Elem()] = struct{}{}
		}

		registered := registeredGateCommitStatusTypeSet()

		for _, want := range registeredGateCommitStatusTypes {
			Expect(discovered).To(HaveKey(want),
				"%s missing from GateCommitStatusKinds. Register the type with SchemeBuilder in api/v1alpha1",
				want.Name())
		}

		for got := range discovered {
			Expect(registered).To(HaveKey(got),
				"%s is discovered by GateCommitStatusKinds but missing from registeredGateCommitStatusTypes. "+
					"Add the type to registeredGateCommitStatusTypes and the IsPromotionStrategyRefGateType table in fieldindex_test.go",
				got.Name())
		}
		Expect(discovered).To(HaveLen(len(registered)),
			"GateCommitStatusKinds discovered %d types but registeredGateCommitStatusTypes lists %d",
			len(discovered), len(registered))

		for _, notWant := range []reflect.Type{
			reflect.TypeFor[promoterv1alpha1.PromotionStrategy](),
			reflect.TypeFor[promoterv1alpha1.ChangeTransferPolicy](),
			reflect.TypeFor[promoterv1alpha1.CommitStatus](),
			reflect.TypeFor[promoterv1alpha1.PullRequest](),
			reflect.TypeFor[promoterv1alpha1.GitRepository](),
		} {
			Expect(discovered).NotTo(HaveKey(notWant),
				"%s must not be treated as a PromotionStrategyRef gate; it lacks Spec.PromotionStrategyRef",
				notWant.Name())
		}
	})
})

func registeredGateCommitStatusTypeSet() map[reflect.Type]struct{} {
	set := make(map[reflect.Type]struct{}, len(registeredGateCommitStatusTypes))
	for _, typ := range registeredGateCommitStatusTypes {
		set[typ] = struct{}{}
	}
	return set
}

var _ = Describe("PromotionStrategyRefName", func() {
	It("returns the referenced PromotionStrategy name for a gate", func() {
		scs := &promoterv1alpha1.ScheduledCommitStatus{}
		scs.Spec.PromotionStrategyRef.Name = "my-ps"
		Expect(PromotionStrategyRefName(scs)).To(Equal("my-ps"))
	})

	It("returns empty for non-gate types", func() {
		Expect(PromotionStrategyRefName(&promoterv1alpha1.PromotionStrategy{})).To(BeEmpty())
	})
})
