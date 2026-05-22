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

package utils_test

import (
	"sync"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ResourceVersionTracker", func() {
	var (
		tracker *utils.ResourceVersionTracker
		keyA    client.ObjectKey
		keyB    client.ObjectKey
	)

	BeforeEach(func() {
		tracker = utils.NewResourceVersionTracker()
		keyA = client.ObjectKey{Namespace: "ns", Name: "a"}
		keyB = client.ObjectKey{Namespace: "ns", Name: "b"}
	})

	DescribeTable("IsCacheStale comparison semantics",
		func(recorded map[client.ObjectKey]string, queryKey client.ObjectKey, cachedRV string, wantStale bool) {
			for k, v := range recorded {
				tracker.Record(k, v)
			}
			Expect(tracker.IsCacheStale(queryKey, cachedRV)).To(Equal(wantStale))
		},
		Entry("no record for key returns not stale (first reconcile)",
			map[client.ObjectKey]string(nil),
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"100", false),
		Entry("cache equals last write is not stale (steady state)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "100"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"100", false),
		Entry("cache ahead of last write is not stale (someone else updated)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "100"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"101", false),
		Entry("cache strictly behind last write is stale (the race we guard against)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "200"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"199", true),
		Entry("different key is independent",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "999"},
			client.ObjectKey{Namespace: "ns", Name: "b"},
			"1", false),
		Entry("empty cached RV is treated as unknown, not infinitely old",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "200"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"", false),
		Entry("non-decimal stored RV fails open (not stale)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "abc"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"100", false),
		Entry("non-decimal cached RV fails open (not stale)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "100"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"abc", false),
		// "9" vs "10": lexically "9" > "10", but as integers 9 < 10.
		// CompareResourceVersion must use bigint semantics, not strcmp.
		Entry("longer-length RV is greater (bigint compare, not lexical)",
			map[client.ObjectKey]string{{Namespace: "ns", Name: "a"}: "10"},
			client.ObjectKey{Namespace: "ns", Name: "a"},
			"9", true),
	)

	Describe("Record", func() {
		It("treats an empty RV as a no-op so a NotFound-during-patch doesn't clobber an existing entry", func() {
			tracker.Record(keyA, "500")
			tracker.Record(keyA, "") // simulating a status patch that was skipped (e.g. NotFound)
			Expect(tracker.IsCacheStale(keyA, "499")).To(BeTrue(),
				"a no-op Record(key, \"\") must not erase the previously recorded RV; "+
					"otherwise a transient patch skip would cause the next reconcile to "+
					"forget the staleness baseline and proceed against an actually-stale cache")
		})

		It("overwrites with a newer RV so subsequent reconciles see the updated baseline", func() {
			tracker.Record(keyA, "500")
			tracker.Record(keyA, "501")
			Expect(tracker.IsCacheStale(keyA, "500")).To(BeTrue())
			Expect(tracker.IsCacheStale(keyA, "501")).To(BeFalse())
		})
	})

	It("is safe under concurrent Record + IsCacheStale across keys (-race must pass)", func() {
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				key := keyA
				if i%2 == 0 {
					key = keyB
				}
				tracker.Record(key, "1000")
				_ = tracker.IsCacheStale(key, "999")
			}(i)
		}
		wg.Wait()
		Expect(tracker.IsCacheStale(keyA, "999")).To(BeTrue())
		Expect(tracker.IsCacheStale(keyB, "999")).To(BeTrue())
	})
})
