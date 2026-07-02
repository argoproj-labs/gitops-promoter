package utils_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	testInstanceID = "wave-0"
	testEnvBranch  = "main"
)

var _ = Describe("CopyInstanceIDLabelToMap", func() {
	It("returns an empty map when labels is nil and parent has no instance-id", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "tcs", Namespace: "ns"},
		}

		labels := utils.CopyInstanceIDLabelToMap(parent, nil)

		Expect(labels).NotTo(BeNil())
		Expect(labels).To(BeEmpty())
	})

	It("no-ops when parent has no instance-id label", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "tcs", Namespace: "ns"},
		}
		labels := utils.CopyInstanceIDLabelToMap(parent, map[string]string{"k": "v"})
		Expect(labels).To(Equal(map[string]string{"k": "v"}))
	})

	It("adds instance-id to the labels map", func() {
		parent := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wrcs",
				Namespace: "ns",
				Labels:    map[string]string{promoterv1alpha1.InstanceIDLabel: "a"},
			},
		}
		labels := utils.CopyInstanceIDLabelToMap(parent, map[string]string{"k": "v"})
		Expect(labels[promoterv1alpha1.InstanceIDLabel]).To(Equal("a"))
		Expect(labels["k"]).To(Equal("v"))
	})
})

var _ = Describe("InstanceIDStatusValue", func() {
	It("returns a pointer to the parent's instance-id label value", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "parent",
				Labels: map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID},
			},
		}

		got := utils.InstanceIDStatusValue(parent)
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal(testInstanceID))
	})

	It("returns nil when the parent has no instance-id label", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		}

		Expect(utils.InstanceIDStatusValue(parent)).To(BeNil())
	})

	It("returns nil when the parent's instance-id label is empty", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "parent",
				Labels: map[string]string{promoterv1alpha1.InstanceIDLabel: ""},
			},
		}

		Expect(utils.InstanceIDStatusValue(parent)).To(BeNil())
	})
})

var _ = Describe("status.instanceID stamping", func() {
	It("sets status.instanceID on Promoter CRs from metadata labels", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID},
			},
		}
		cs := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID},
			},
		}

		ps.SetStatusInstanceID(utils.InstanceIDStatusValue(ps))
		cs.SetStatusInstanceID(utils.InstanceIDStatusValue(cs))

		Expect(ps.Status.InstanceID).NotTo(BeNil())
		Expect(*ps.Status.InstanceID).To(Equal(testInstanceID))
		Expect(cs.Status.InstanceID).NotTo(BeNil())
		Expect(*cs.Status.InstanceID).To(Equal(testInstanceID))
	})

	It("clears status.instanceID when the parent has no instance-id label", func() {
		stale := testInstanceID
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "ps"},
			Status: promoterv1alpha1.PromotionStrategyStatus{
				InstanceID: &stale,
			},
		}

		ps.SetStatusInstanceID(utils.InstanceIDStatusValue(ps))

		Expect(ps.Status.InstanceID).To(BeNil())
	})
})
