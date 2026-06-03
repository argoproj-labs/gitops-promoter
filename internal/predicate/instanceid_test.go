package predicate_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterpredicate "github.com/argoproj-labs/gitops-promoter/internal/predicate"
)

var _ = Describe("InstanceID predicate", func() {
	newObj := func(labels map[string]string) *promoterv1alpha1.PromotionStrategy {
		return &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ps",
				Labels: labels,
			},
		}
	}

	Describe("with empty instance id (backwards-compat)", func() {
		// This is the most important invariant: an empty --instance-id must
		// allow every event through, so existing deployments keep reconciling
		// resources that have no instance-id label set.
		p := promoterpredicate.InstanceID("")

		DescribeTable("allows all events regardless of object labels",
			func(labels map[string]string) {
				obj := newObj(labels)
				Expect(p.Create(event.CreateEvent{Object: obj})).To(BeTrue())
				Expect(p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj})).To(BeTrue())
				Expect(p.Delete(event.DeleteEvent{Object: obj})).To(BeTrue())
				Expect(p.Generic(event.GenericEvent{Object: obj})).To(BeTrue())
			},
			Entry("nil labels", nil),
			Entry("empty labels", map[string]string{}),
			Entry("label set to wave-0", map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"}),
			Entry("label set to empty string", map[string]string{promoterv1alpha1.InstanceIDLabel: ""}),
			Entry("unrelated labels only", map[string]string{"foo": "bar"}),
		)
	})

	Describe("with instance id set to wave-0", func() {
		p := promoterpredicate.InstanceID("wave-0")

		It("admits objects with a matching label across all event types", func() {
			obj := newObj(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"})
			Expect(p.Create(event.CreateEvent{Object: obj})).To(BeTrue())
			Expect(p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj})).To(BeTrue())
			Expect(p.Delete(event.DeleteEvent{Object: obj})).To(BeTrue())
			Expect(p.Generic(event.GenericEvent{Object: obj})).To(BeTrue())
		})

		DescribeTable("denies objects without a matching label across all event types",
			func(labels map[string]string) {
				obj := newObj(labels)
				Expect(p.Create(event.CreateEvent{Object: obj})).To(BeFalse())
				Expect(p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj})).To(BeFalse())
				Expect(p.Delete(event.DeleteEvent{Object: obj})).To(BeFalse())
				Expect(p.Generic(event.GenericEvent{Object: obj})).To(BeFalse())
			},
			Entry("mismatching label value", map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-1"}),
			Entry("label key missing, other labels present", map[string]string{"foo": "bar"}),
			Entry("nil labels", nil),
			Entry("empty labels map", map[string]string{}),
			Entry("label key present with empty value", map[string]string{promoterv1alpha1.InstanceIDLabel: ""}),
		)
	})
})
