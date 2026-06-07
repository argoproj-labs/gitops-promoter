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

package matching_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/matching"
)

const (
	matchNamespace = "team-a"

	// Common label keys/values reused across the selector test cases.
	labelKeyApp   = "app"
	labelKeyTier  = "tier"
	labelValWeb   = "web"
	labelValFront = "frontend"
)

// newNotification builds a PromoterNotification in matchNamespace subscribed to the given
// event types with the given selector.
func newNotification(eventTypes []promoterv1alpha1.NotificationEventType, selector *metav1.LabelSelector) *promoterv1alpha1.PromoterNotification {
	return &promoterv1alpha1.PromoterNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: matchNamespace},
		Spec: promoterv1alpha1.PromoterNotificationSpec{
			EventTypes: eventTypes,
			Selector:   selector,
		},
	}
}

// newEvent builds a CTPActive Event originating from the given namespace with the given labels.
// The event type is fixed (CTPActive); the event-type membership tests vary the notification's
// subscribed types, not the event's type.
func newEvent(ns string, labels map[string]string) events.Event {
	return events.Event{
		Type: events.TypeCTPActive,
		Object: events.ObjectRef{
			APIVersion: "promoter.argoproj.io/v1alpha1",
			Kind:       "ChangeTransferPolicy",
			Namespace:  ns,
			Name:       "ctp-1",
		},
		Labels: labels,
	}
}

var _ = Describe("Matches", func() {
	Describe("namespace scope", func() {
		It("matches when the notification and event share a namespace", func() {
			n := newNotification([]promoterv1alpha1.NotificationEventType{events.TypeCTPActive}, nil)
			ev := newEvent(matchNamespace, nil)
			ok, err := matching.Matches(n, ev)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		It("does NOT match an event from a different namespace", func() {
			n := newNotification([]promoterv1alpha1.NotificationEventType{events.TypeCTPActive}, nil)
			ev := newEvent("team-b", nil)
			ok, err := matching.Matches(n, ev)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})
	})

	Describe("event type membership", func() {
		It("matches when the event type is one of spec.eventTypes", func() {
			n := newNotification([]promoterv1alpha1.NotificationEventType{
				events.TypeGateFailed, events.TypeCTPActive,
			}, nil)
			ok, err := matching.Matches(n, newEvent(matchNamespace, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		It("does NOT match when the event type is not subscribed", func() {
			n := newNotification([]promoterv1alpha1.NotificationEventType{events.TypeGateFailed}, nil)
			ok, err := matching.Matches(n, newEvent(matchNamespace, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("fails closed when eventTypes is empty (matches nothing)", func() {
			n := newNotification(nil, nil)
			ok, err := matching.Matches(n, newEvent(matchNamespace, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})
	})

	Describe("label selector", func() {
		allTypes := []promoterv1alpha1.NotificationEventType{events.TypeCTPActive}

		It("a nil selector matches any labels (including none)", func() {
			n := newNotification(allTypes, nil)
			ok, err := matching.Matches(n, newEvent(matchNamespace, map[string]string{labelKeyApp: labelValWeb}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			ok, err = matching.Matches(n, newEvent(matchNamespace, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		It("an empty (non-nil) selector matches everything", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{})
			ok, err := matching.Matches(n, newEvent(matchNamespace, map[string]string{labelKeyApp: labelValWeb}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		It("matchLabels matches when all key/values are present", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{
				MatchLabels: map[string]string{labelKeyApp: labelValWeb, "team": "a"},
			})
			ok, err := matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyApp: labelValWeb, "team": "a", "extra": "ok"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		It("matchLabels does NOT match when a required label value differs", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{
				MatchLabels: map[string]string{labelKeyApp: labelValWeb},
			})
			ok, err := matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyApp: "api"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("matchLabels does NOT match when a required label is absent", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{
				MatchLabels: map[string]string{labelKeyApp: labelValWeb},
			})
			ok, err := matching.Matches(n, newEvent(matchNamespace, nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("supports matchExpressions (In)", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      labelKeyTier,
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{labelValFront, "backend"},
				}},
			})
			ok, err := matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyTier: labelValFront}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			ok, err = matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyTier: "db"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("supports matchExpressions (Exists / DoesNotExist)", func() {
			exists := newNotification(allTypes, &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key: "canary", Operator: metav1.LabelSelectorOpExists,
				}},
			})
			ok, err := matching.Matches(exists, newEvent(matchNamespace,
				map[string]string{"canary": "true"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			ok, err = matching.Matches(exists, newEvent(matchNamespace,
				map[string]string{"other": "x"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("returns an error for a structurally invalid selector and reports non-match", func() {
			n := newNotification(allTypes, &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      labelKeyApp,
					Operator: metav1.LabelSelectorOpIn,
					Values:   nil, // In requires at least one value
				}},
			})
			ok, err := matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyApp: labelValWeb}))
			Expect(err).To(HaveOccurred())
			Expect(ok).To(BeFalse())
		})
	})

	Describe("combined / critical negative case", func() {
		It("requires ALL of namespace + type + selector to match", func() {
			n := newNotification([]promoterv1alpha1.NotificationEventType{events.TypeCTPActive},
				&metav1.LabelSelector{MatchLabels: map[string]string{labelKeyApp: labelValWeb}})

			// All three satisfied -> match.
			ok, err := matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyApp: labelValWeb}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			// Right ns + type, wrong labels -> the framework must deliver NOTHING.
			ok, err = matching.Matches(n, newEvent(matchNamespace,
				map[string]string{labelKeyApp: "api"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("nil notification is a programmer error", func() {
			_, err := matching.Matches(nil, newEvent(matchNamespace, nil))
			Expect(err).To(HaveOccurred())
		})
	})
})
