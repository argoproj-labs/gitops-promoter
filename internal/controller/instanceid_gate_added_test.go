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
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// Gate commit-status managers participate in multi-install via scheme discovery
// (kinds.All → cache.PartitionedObjects). These specs fail when a new
// Spec.PromotionStrategyRef gate is registered without that wiring.
var _ = Describe("Gate commit-status managers stay in sync with instance-id partitioning", func() {
	It("partitions every discovered gate kind", func() {
		scheme := utils.GetScheme()
		partitioned := map[string]struct{}{}
		for _, obj := range promotercache.PartitionedObjects() {
			if _, isSecret := obj.(*corev1.Secret); isSecret {
				continue
			}
			gvk, err := apiutil.GVKForObject(obj, scheme)
			Expect(err).NotTo(HaveOccurred())
			partitioned[gvk.Kind] = struct{}{}
		}
		for _, gate := range GateCommitStatusKinds() {
			gvk, err := apiutil.GVKForObject(gate, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(partitioned).To(HaveKey(gvk.Kind),
				"%s is not in the instance-id cache partition. Register it with SchemeBuilder "+
					"in api/v1alpha1 so kinds.All includes it; PartitionedObjects is derived from kinds.All",
				gvk.Kind)
		}
	})

	It("derives a parent-gate CommitStatus label key for every discovered gate kind", func() {
		for _, gate := range GateCommitStatusKinds() {
			kindName := reflect.TypeOf(gate).Elem().Name()
			gate.SetName("gate-" + kindName)
			key := utils.CommitStatusGateLabelKeyForParent(gate)
			Expect(key).NotTo(BeEmpty(),
				"%T: CommitStatusGateLabelKeyForParent returned empty. Kind must end with CommitStatus "+
					"(e.g. MyCommitStatus) and be resolvable from the scheme; see utils.CommitStatusGateLabelKeyForParent",
				gate)
			Expect(key).To(HavePrefix("promoter.argoproj.io/"),
				"%T: parent-gate label %q must use promoter.argoproj.io/ prefix", gate, key)
			Expect(key).To(HaveSuffix("-commit-status"),
				"%T: parent-gate label %q must end with -commit-status (Kind stem from %s)", gate, key, kindName)
		}
	})
})
