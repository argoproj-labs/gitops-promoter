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

// keyA / keyB are package-level so both the DescribeTable Entries (which are
// evaluated at file-init time, before any BeforeEach) and the other specs share
// the same fixtures without redeclaring them inline.
var (
	keyA = client.ObjectKey{Namespace: "ns", Name: "a"}
	keyB = client.ObjectKey{Namespace: "ns", Name: "b"}
)

var _ = Describe("ResourceVersionTracker", func() {
	var tracker *utils.ResourceVersionTracker

	BeforeEach(func() {
		tracker = utils.NewResourceVersionTracker()
	})

	DescribeTable("IsCacheStale comparison semantics",
		func(recorded map[client.ObjectKey]string, queryKey client.ObjectKey, cachedRV string, wantStale bool) {
			for k, v := range recorded {
				tracker.Record(k, v)
			}
			Expect(tracker.IsCacheStale(queryKey, cachedRV)).To(Equal(wantStale))
		},
		Entry("empty tracker => never stale (controller's first reconcile for this key)",
			map[client.ObjectKey]string(nil), keyA, "100", false),
		Entry("cache RV == last written RV => not stale (cache fully caught up, steady state)",
			map[client.ObjectKey]string{keyA: "100"}, keyA, "100", false),
		Entry("cache RV > last written RV => not stale (third party updated the object after us)",
			map[client.ObjectKey]string{keyA: "100"}, keyA, "101", false),
		Entry("cache RV < last written RV => stale (informer hasn't observed our last write yet)",
			map[client.ObjectKey]string{keyA: "200"}, keyA, "199", true),
		Entry("a record for one key does not bleed into checks for a different key",
			map[client.ObjectKey]string{keyA: "999"}, keyB, "1", false),
		Entry("empty cached RV => not stale (treat unknown as not-yet-known, not as infinitely old)",
			map[client.ObjectKey]string{keyA: "200"}, keyA, "", false),
		Entry("stored RV is malformed => not stale (fail open: bad data should not wedge reconciles)",
			map[client.ObjectKey]string{keyA: "abc"}, keyA, "100", false),
		Entry("cached RV is malformed => not stale (same fail-open rationale as above)",
			map[client.ObjectKey]string{keyA: "100"}, keyA, "abc", false),
		Entry("9 vs 10 => stale (RVs compare as integers, not lexically)",
			map[client.ObjectKey]string{keyA: "10"}, keyA, "9", true),
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
