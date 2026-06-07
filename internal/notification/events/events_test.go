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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// newBaseEvent returns a fully-populated Event used as the canonical fixture for
// EventID tests. Each test mutates exactly one field of a fresh copy so we can assert
// which fields participate in the stable ID and which (OccurredAt) do not.
func newBaseEvent() Event {
	return Event{
		Type: TypeCTPActive,
		Object: ObjectRef{
			APIVersion: "promoter.argoproj.io/v1alpha1",
			Kind:       "ChangeTransferPolicy",
			Namespace:  "team-a",
			Name:       "ctp-1",
			UID:        "uid-123",
		},
		Labels:        map[string]string{"app": "web", "team": "a"},
		Environment:   "environment/production",
		GateName:      "gate-x",
		PreviousSha:   "aaaa1111",
		NewSha:        "bbbb2222",
		PreviousState: "pending",
		NewState:      "success",
		ActiveURL:     "https://example.com/active",
		ProposedURL:   "https://example.com/proposed",
		OccurredAt:    time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
	}
}

var _ = Describe("Event", func() {
	Describe("EventID", func() {
		It("is deterministic across two freshly-built identical events", func() {
			e1 := newBaseEvent()
			e2 := newBaseEvent()
			Expect(e1.EventID()).To(Equal(e2.EventID()))
		})

		It("returns a 64-char lowercase hex sha256 digest", func() {
			e := newBaseEvent()
			id := e.EventID()
			Expect(id).To(HaveLen(64))
			Expect(id).To(MatchRegexp("^[0-9a-f]{64}$"))
		})

		It("is idempotent: repeated calls on the same value return the same cached ID", func() {
			e := newBaseEvent()
			first := e.EventID()
			second := e.EventID()
			Expect(second).To(Equal(first))
		})

		It("EXCLUDES OccurredAt so a re-emitted identical event collapses to one ID", func() {
			e1 := newBaseEvent()
			e2 := newBaseEvent()
			e2.OccurredAt = e1.OccurredAt.Add(48 * time.Hour)
			Expect(e2.EventID()).To(Equal(e1.EventID()),
				"OccurredAt must not affect the stable event ID")
		})

		It("EXCLUDES the originating resource's labels, URLs (non-identity fields)", func() {
			base := newBaseEvent()
			baseID := base.EventID()

			withDiffLabels := newBaseEvent()
			withDiffLabels.Labels = map[string]string{"completely": "different"}
			Expect(withDiffLabels.EventID()).To(Equal(baseID), "Labels must not affect the ID")

			withDiffURLs := newBaseEvent()
			withDiffURLs.ActiveURL = "https://changed/active"
			withDiffURLs.ProposedURL = "https://changed/proposed"
			Expect(withDiffURLs.EventID()).To(Equal(baseID), "URLs must not affect the ID")
		})

		DescribeTable("changes when any identity-defining field changes",
			func(mutate func(*Event)) {
				base := newBaseEvent()
				baseID := base.EventID()

				mutated := newBaseEvent()
				mutate(&mutated)
				Expect(mutated.EventID()).NotTo(Equal(baseID))
			},
			Entry("Type", func(e *Event) { e.Type = TypeCTPProposed }),
			Entry("Object.Namespace", func(e *Event) { e.Object.Namespace = "team-b" }),
			Entry("Object.Kind", func(e *Event) { e.Object.Kind = "PromotionStrategy" }),
			Entry("Object.Name", func(e *Event) { e.Object.Name = "ctp-2" }),
			Entry("Object.UID", func(e *Event) { e.Object.UID = "uid-999" }),
			Entry("Environment", func(e *Event) { e.Environment = "environment/staging" }),
			Entry("GateName", func(e *Event) { e.GateName = "gate-y" }),
			Entry("PreviousSha", func(e *Event) { e.PreviousSha = "ffff0000" }),
			Entry("NewSha", func(e *Event) { e.NewSha = "cccc3333" }),
			Entry("PreviousState", func(e *Event) { e.PreviousState = "stale" }),
			Entry("NewState", func(e *Event) { e.NewState = "failed" }),
		)

		It("does not collide across the field separator (distinct field layouts map to distinct IDs)", func() {
			// Guards against a naive concatenation where moving content across a field
			// boundary would produce the same joined string. The \x1f separator should
			// prevent ("a","b") and ("ab","") from colliding.
			e1 := Event{Object: ObjectRef{Namespace: "a", Kind: "b"}}
			e2 := Event{Object: ObjectRef{Namespace: "ab", Kind: ""}}
			Expect(e1.EventID()).NotTo(Equal(e2.EventID()))
		})
	})

	Describe("SortedLabelKeys", func() {
		It("returns keys in sorted order", func() {
			e := Event{Labels: map[string]string{"z": "1", "a": "2", "m": "3"}}
			Expect(e.SortedLabelKeys()).To(Equal([]string{"a", "m", "z"}))
		})

		It("returns an empty slice for no labels", func() {
			e := Event{}
			Expect(e.SortedLabelKeys()).To(BeEmpty())
		})
	})

	Describe("ObjectRef.String", func() {
		It("returns a stable namespace/kind/name identity", func() {
			ref := ObjectRef{Namespace: "ns", Kind: "ChangeTransferPolicy", Name: "ctp-1"}
			Expect(ref.String()).To(Equal("ns/ChangeTransferPolicy/ctp-1"))
		})
	})

	Describe("EventType aliasing", func() {
		It("re-exported constants equal the v1alpha1 enum values so spec.eventTypes match without conversion", func() {
			Expect(TypePromotionComplete).To(Equal(v1alpha1.NotificationEventPromotionComplete))
			Expect(TypeGateFailed).To(Equal(v1alpha1.NotificationEventGateFailed))
			Expect(TypeGateStale).To(Equal(v1alpha1.NotificationEventGateStale))
			Expect(TypeCTPProposed).To(Equal(v1alpha1.NotificationEventCTPProposed))
			Expect(TypeCTPActive).To(Equal(v1alpha1.NotificationEventCTPActive))
		})
	})
})

var _ = Describe("HandlerFunc", func() {
	It("adapts a function to the Handler interface", func() {
		called := false
		var h Handler = HandlerFunc(func(_ context.Context, _ Event) error {
			called = true
			return nil
		})
		Expect(h.Handle(context.Background(), Event{})).To(Succeed())
		Expect(called).To(BeTrue())
	})
})
