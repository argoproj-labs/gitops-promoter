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

package events

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// kindCTP is the originating-resource kind used in these broker tests. Declared as a const so
// the literal is not repeated (goconst).
const kindCTP = "ChangeTransferPolicy"

var _ = Describe("MemoryBroker", func() {
	var (
		ctx    context.Context
		broker *MemoryBroker
	)

	validEvent := func() Event {
		return Event{
			Type: TypeCTPActive,
			Object: ObjectRef{
				APIVersion: "promoter.argoproj.io/v1alpha1",
				Kind:       kindCTP,
				Namespace:  "ns",
				Name:       "ctp",
			},
			NewSha: "abc123",
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		broker = NewMemoryBroker()
	})

	It("fans a published event out to all subscribed handlers", func() {
		var mu sync.Mutex
		var got []Event
		record := HandlerFunc(func(_ context.Context, e Event) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, e)
			return nil
		})
		broker.Subscribe(record)
		broker.Subscribe(record)

		Expect(broker.Publish(ctx, validEvent())).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		Expect(got).To(HaveLen(2), "both subscribed handlers should receive the event")
		Expect(got[0].Type).To(Equal(TypeCTPActive))
	})

	It("is a no-op (no error) when there are no subscribers", func() {
		Expect(broker.Publish(ctx, validEvent())).To(Succeed())
	})

	It("does not propagate handler errors to the publisher", func() {
		broker.Subscribe(HandlerFunc(func(_ context.Context, _ Event) error {
			return errors.New("boom")
		}))
		// Publish must never fail a reconcile for handler/transport reasons.
		Expect(broker.Publish(ctx, validEvent())).To(Succeed())
	})

	It("still invokes later handlers even if an earlier one errors", func() {
		called := false
		broker.Subscribe(HandlerFunc(func(_ context.Context, _ Event) error {
			return errors.New("boom")
		}))
		broker.Subscribe(HandlerFunc(func(_ context.Context, _ Event) error {
			called = true
			return nil
		}))
		Expect(broker.Publish(ctx, validEvent())).To(Succeed())
		Expect(called).To(BeTrue())
	})

	It("rejects programmer-error events (empty type)", func() {
		e := validEvent()
		e.Type = ""
		Expect(broker.Publish(ctx, e)).ToNot(Succeed())
	})

	It("rejects events with no object identity", func() {
		e := validEvent()
		e.Object.Namespace = ""
		Expect(broker.Publish(ctx, e)).ToNot(Succeed())
	})

	It("ignores a nil handler on Subscribe", func() {
		broker.Subscribe(nil)
		Expect(broker.Publish(ctx, validEvent())).To(Succeed())
	})
})
