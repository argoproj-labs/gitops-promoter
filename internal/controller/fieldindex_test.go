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

var _ = Describe("GateCommitStatusKinds", func() {
	It("discovers PromotionStrategyRef gate types from the scheme", func() {
		got := map[string]struct{}{}
		for _, obj := range GateCommitStatusKinds() {
			got[reflect.TypeOf(obj).Elem().Name()] = struct{}{}
		}

		for _, want := range []string{
			"ArgoCDCommitStatus",
			"GitCommitStatus",
			"TimedCommitStatus",
			"WebRequestCommitStatus",
			"ScheduledCommitStatus",
		} {
			Expect(got).To(HaveKey(want),
				"%s missing from GateCommitStatusKinds. Ensure Spec has PromotionStrategyRef and the type is "+
					"registered with SchemeBuilder in api/v1alpha1",
				want)
		}

		for _, notWant := range []string{
			"PromotionStrategy",
			"ChangeTransferPolicy",
			"CommitStatus",
			"PullRequest",
			"GitRepository",
		} {
			Expect(got).NotTo(HaveKey(notWant),
				"%s must not be treated as a PromotionStrategyRef gate; it lacks Spec.PromotionStrategyRef",
				notWant)
		}
	})
})

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
