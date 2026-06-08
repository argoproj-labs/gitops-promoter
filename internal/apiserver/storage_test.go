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

package apiserver

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"

	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

func newTestStore() *REST {
	return NewREST(newProviderWithReader(newFakeReader(seedObjects()...)))
}

var _ = Describe("REST storage", func() {
	var store *REST

	// nsContext returns a request context scoped to the test namespace. Built per
	// spec (not stored on the closure) to keep contexts request-scoped.
	nsContext := func() context.Context {
		return genericapirequest.WithNamespace(context.Background(), testNamespace)
	}

	BeforeEach(func() {
		store = newTestStore()
	})

	Describe("Get", func() {
		It("returns the bundle for the named PromotionStrategy", func() {
			ctx := nsContext()
			obj, err := store.Get(ctx, testPSName, &metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			bundle, ok := obj.(*viewv1alpha1.PromotionStrategyDetails)
			Expect(ok).To(BeTrue())
			Expect(bundle.Name).To(Equal(testPSName))
		})

		It("returns NotFound for a missing PromotionStrategy", func() {
			ctx := nsContext()
			_, err := store.Get(ctx, "missing", &metav1.GetOptions{})
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Describe("List", func() {
		It("returns bundles for the request namespace", func() {
			ctx := nsContext()
			obj, err := store.List(ctx, &metainternalversion.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			list, ok := obj.(*viewv1alpha1.PromotionStrategyDetailsList)
			Expect(ok).To(BeTrue())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Name).To(Equal(testPSName))
		})

		It("is namespace scoped", func() {
			otherCtx := genericapirequest.WithNamespace(context.Background(), "different-namespace")
			obj, err := store.List(otherCtx, &metainternalversion.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			list, ok := obj.(*viewv1alpha1.PromotionStrategyDetailsList)
			Expect(ok).To(BeTrue())
			Expect(list.Items).To(BeEmpty())
		})
	})

	Describe("Watch", func() {
		It("sends an initial ADDED snapshot then a delta", func() {
			ctx := nsContext()
			w, err := store.Watch(ctx, &metainternalversion.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			var ev watch.Event
			Eventually(w.ResultChan()).Should(Receive(&ev))
			Expect(ev.Type).To(Equal(watch.Added))
			snap, ok := ev.Object.(*viewv1alpha1.PromotionStrategyDetails)
			Expect(ok).To(BeTrue())
			Expect(snap.Name).To(Equal(testPSName))

			By("broadcasting a delta when the key is rebuilt")
			store.provider.reconcileKey(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testPSName})

			var delta watch.Event
			Eventually(w.ResultChan()).Should(Receive(&delta))
			Expect([]watch.EventType{watch.Added, watch.Modified}).To(ContainElement(delta.Type))
			deltaObj, ok := delta.Object.(*viewv1alpha1.PromotionStrategyDetails)
			Expect(ok).To(BeTrue())
			Expect(deltaObj.Name).To(Equal(testPSName))
		})

		It("does not emit the initial-events bookmark without AllowWatchBookmarks", func() {
			ctx := nsContext()
			sendInitialEvents := true
			// SendInitialEvents without AllowWatchBookmarks is NOT a watch-list request
			// (apiserver isListWatchRequest), so no terminating bookmark must be sent.
			w, err := store.Watch(ctx, &metainternalversion.ListOptions{SendInitialEvents: &sendInitialEvents})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			var added watch.Event
			Eventually(w.ResultChan()).Should(Receive(&added))
			Expect(added.Type).To(Equal(watch.Added))

			// No bookmark (or anything else) should follow the snapshot.
			Consistently(w.ResultChan(), 200*time.Millisecond).ShouldNot(Receive())
		})

		It("skips the snapshot when a resourceVersion is supplied", func() {
			ctx := nsContext()
			w, err := store.Watch(ctx, &metainternalversion.ListOptions{ResourceVersion: "5"})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			Consistently(w.ResultChan(), 200*time.Millisecond).ShouldNot(Receive())
		})

		It("ends the initial events with an annotated bookmark for the watch-list protocol", func() {
			ctx := nsContext()
			sendInitialEvents := true
			w, err := store.Watch(ctx, &metainternalversion.ListOptions{
				SendInitialEvents:    &sendInitialEvents,
				AllowWatchBookmarks:  true,
				ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			// First the ADDED snapshot for the seeded PromotionStrategy.
			var added watch.Event
			Eventually(w.ResultChan()).Should(Receive(&added))
			Expect(added.Type).To(Equal(watch.Added))

			// Then the terminating bookmark marking the end of the initial events.
			var bookmark watch.Event
			Eventually(w.ResultChan()).Should(Receive(&bookmark))
			Expect(bookmark.Type).To(Equal(watch.Bookmark))
			obj, err := meta.Accessor(bookmark.Object)
			Expect(err).NotTo(HaveOccurred())
			Expect(obj.GetAnnotations()).To(HaveKeyWithValue(metav1.InitialEventsAnnotationKey, "true"))
			Expect(obj.GetResourceVersion()).NotTo(BeEmpty())
		})
	})
})
