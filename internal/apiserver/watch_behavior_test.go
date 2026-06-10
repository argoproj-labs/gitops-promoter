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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// mustDetailsList asserts the runtime.Object returned by List is the typed list.
func mustDetailsList(obj runtime.Object) *viewv1alpha1.PromotionStrategyDetailsList {
	GinkgoHelper()
	list, ok := obj.(*viewv1alpha1.PromotionStrategyDetailsList)
	Expect(ok).To(BeTrue(), "List must return a *PromotionStrategyDetailsList")
	return list
}

// watchTestNamespace isolates makePS fixtures from the shared seedObjects namespace (test-ns).
const watchTestNamespace = "watch-test-ns"

// makePS returns a minimal PromotionStrategy in watchTestNamespace (no repository
// reference, so buildBundle resolves no git config) with the given labels.
func makePS(name string, lbls map[string]string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: watchTestNamespace, Labels: lbls},
	}
}

var _ = Describe("Watch fan-out behavior", func() {
	nsContext := func(ns string) context.Context {
		return genericapirequest.WithNamespace(context.Background(), ns)
	}

	Describe("watch-list initial snapshot", func() {
		It("delivers every bundle plus the terminating bookmark even past the delta buffer size", func() {
			// More PromotionStrategies than the per-watcher delta buffer (64). The
			// initial watch-list snapshot must never be truncated by that buffer,
			// and the initial-events-end bookmark must always arrive — a reflector
			// blocks forever without it.
			const total = 70
			objs := make([]client.Object, 0, total)
			for i := range total {
				objs = append(objs, makePS(fmt.Sprintf("ps-%03d", i), nil))
			}
			store := NewREST(newProviderWithReader(newFakeReader(objs...)))

			sendInitialEvents := true
			w, err := store.Watch(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				SendInitialEvents:    &sendInitialEvents,
				AllowWatchBookmarks:  true,
				ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			added := 0
			deadline := time.After(10 * time.Second)
		recv:
			for {
				select {
				case ev, ok := <-w.ResultChan():
					Expect(ok).To(BeTrue(), "watch ended before the initial snapshot completed")
					switch ev.Type {
					case watch.Added:
						added++
					case watch.Bookmark:
						break recv
					default:
						Fail(fmt.Sprintf("unexpected event type %q during initial snapshot", ev.Type))
					}
				case <-deadline:
					Fail(fmt.Sprintf("timed out after %d ADDED events without the initial-events-end bookmark", added))
				}
			}
			Expect(added).To(Equal(total), "initial snapshot must not be truncated")
		})
	})

	Describe("slow watcher", func() {
		It("is terminated on buffer overflow instead of silently losing deltas", func() {
			provider := newProviderWithReader(newFakeReader(seedObjects()...))
			store := NewREST(provider)

			// A watch at a specific resourceVersion gets no initial snapshot; every
			// event it sees is a delta.
			w, err := store.Watch(nsContext(testNamespace), &metainternalversion.ListOptions{ResourceVersion: "5"})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			// Broadcast far more deltas than the watcher buffer can hold while the
			// consumer reads nothing. Dropping deltas silently would leave the client
			// stale forever with no signal; the watcher must be closed instead so the
			// client re-lists.
			key := types.NamespacedName{Namespace: testNamespace, Name: testPSName}
			for range 200 {
				Expect(provider.reconcileKey(context.Background(), key)).To(Succeed())
			}

			Eventually(func() bool {
				for {
					select {
					case _, ok := <-w.ResultChan():
						if !ok {
							return true
						}
					default:
						return false
					}
				}
			}, 10*time.Second, 50*time.Millisecond).Should(BeTrue(),
				"overflowing watcher must have its result channel closed to force a client re-list")
		})
	})

	Describe("label selectors", func() {
		var store *REST

		BeforeEach(func() {
			store = NewREST(newProviderWithReader(newFakeReader(
				makePS("prod-ps", map[string]string{"env": "prod"}),
				makePS("dev-ps", map[string]string{"env": "dev"}),
			)))
		})

		It("filters List results", func() {
			obj, err := store.List(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				LabelSelector: labels.SelectorFromSet(labels.Set{"env": "prod"}),
			})
			Expect(err).NotTo(HaveOccurred())
			list := mustDetailsList(obj)
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Name).To(Equal("prod-ps"))
		})

		It("suppresses non-matching initial watch events", func() {
			w, err := store.Watch(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				LabelSelector: labels.SelectorFromSet(labels.Set{"env": "prod"}),
			})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			var ev watch.Event
			Eventually(w.ResultChan()).Should(Receive(&ev))
			Expect(ev.Type).To(Equal(watch.Added))
			obj, err := meta.Accessor(ev.Object)
			Expect(err).NotTo(HaveOccurred())
			Expect(obj.GetName()).To(Equal("prod-ps"))

			Consistently(w.ResultChan(), 200*time.Millisecond).ShouldNot(Receive(),
				"the non-matching bundle must not be delivered")
		})

		It("sends DELETED to a selector watcher when the object stops matching", func() {
			cl := fake.NewClientBuilder().WithScheme(utils.GetScheme()).
				WithObjects(makePS("prod-ps", map[string]string{"env": "prod"})).
				Build()
			provider := newProviderWithReader(cl)
			store := NewREST(provider)

			w, err := store.Watch(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				LabelSelector: labels.SelectorFromSet(labels.Set{"env": "prod"}),
			})
			Expect(err).NotTo(HaveOccurred())
			defer w.Stop()

			var added watch.Event
			Eventually(w.ResultChan()).Should(Receive(&added))
			Expect(added.Type).To(Equal(watch.Added))

			// Make the broadcast path aware of the object's current labels.
			key := types.NamespacedName{Namespace: watchTestNamespace, Name: "prod-ps"}
			Expect(provider.reconcileKey(context.Background(), key)).To(Succeed())

			By("relabeling the PromotionStrategy so it no longer matches the selector")
			ps := &promoterv1alpha1.PromotionStrategy{}
			Expect(cl.Get(context.Background(), key, ps)).To(Succeed())
			ps.Labels = map[string]string{"env": "dev"}
			Expect(cl.Update(context.Background(), ps)).To(Succeed())
			Expect(provider.reconcileKey(context.Background(), key)).To(Succeed())

			Eventually(func(g Gomega) {
				var ev watch.Event
				g.Expect(w.ResultChan()).To(Receive(&ev))
				g.Expect(ev.Type).To(Equal(watch.Deleted))
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed(),
				"a selector watcher must see DELETED when the object stops matching")
		})
	})

	Describe("field selectors", func() {
		var store *REST

		BeforeEach(func() {
			store = NewREST(newProviderWithReader(newFakeReader(
				makePS("prod-ps", nil),
				makePS("dev-ps", nil),
			)))
		})

		It("supports metadata.name equality on List", func() {
			obj, err := store.List(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				FieldSelector: fields.OneTermEqualSelector("metadata.name", "dev-ps"),
			})
			Expect(err).NotTo(HaveOccurred())
			list := mustDetailsList(obj)
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Name).To(Equal("dev-ps"))
		})

		It("rejects unsupported field selectors instead of silently ignoring them", func() {
			_, err := store.List(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				FieldSelector: fields.OneTermEqualSelector("spec.unsupported", "x"),
			})
			Expect(err).To(HaveOccurred())

			_, err = store.Watch(nsContext(watchTestNamespace), &metainternalversion.ListOptions{
				FieldSelector: fields.OneTermEqualSelector("spec.unsupported", "x"),
			})
			Expect(err).To(HaveOccurred())
		})
	})
})
