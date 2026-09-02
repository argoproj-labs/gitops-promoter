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

package kinds_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

func TestKinds(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kinds Suite")
}

var _ = Describe("All", func() {
	It("returns every promoter root kind and stable pointer identity", func() {
		scheme := utils.GetScheme()
		first := kinds.All(scheme)
		second := kinds.All(scheme)
		Expect(first).NotTo(BeEmpty())
		Expect(second).To(HaveLen(len(first)))

		seen := map[string]struct{}{}
		for i, obj := range first {
			gvk, err := apiutil.GVKForObject(obj, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(gvk.GroupVersion()).To(Equal(schema.GroupVersion{Group: "promoter.argoproj.io", Version: "v1alpha1"}))
			Expect(seen).NotTo(HaveKey(gvk.Kind), "duplicate kind %s", gvk.Kind)
			seen[gvk.Kind] = struct{}{}
			Expect(second[i]).To(BeIdenticalTo(obj), "All must return stable ByObject keys")
		}

		Expect(seen).To(HaveKey(kinds.ControllerConfigurationKind),
			"kinds.All did not find ControllerConfiguration; ensure it is registered with SchemeBuilder")
		Expect(seen).To(HaveKey("PromotionStrategy"),
			"kinds.All did not find PromotionStrategy; ensure it is registered with SchemeBuilder")
		Expect(seen).To(HaveKey("ScheduledCommitStatus"),
			"kinds.All did not find ScheduledCommitStatus. Register the type with SchemeBuilder in api/v1alpha1 "+
				"so instance-id cache partitioning and metrics pick it up automatically")
	})
})
