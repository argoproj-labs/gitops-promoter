package utils_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("CopyInstanceIDLabel", func() {
	var (
		parent *promoterv1alpha1.PromotionStrategy
		child  *promoterv1alpha1.ChangeTransferPolicy
	)

	BeforeEach(func() {
		parent = &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "parent"},
		}
		child = &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "child"},
		}
	})

	It("copies the instance-id label from parent to a child with nil labels", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, "wave-0"))
	})

	It("copies the instance-id label without clobbering existing child labels", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"})
		child.SetLabels(map[string]string{
			promoterv1alpha1.EnvironmentLabel:       "main",
			promoterv1alpha1.PromotionStrategyLabel: "ps",
		})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, "wave-0"))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, "main"))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.PromotionStrategyLabel, "ps"))
	})

	It("overwrites the child's existing instance-id label with the parent's value", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"})
		child.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-stale"})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, "wave-0"))
	})

	It("is a no-op when the parent has no labels", func() {
		// nil labels on parent
		utils.CopyInstanceIDLabel(parent, child)
		Expect(child.GetLabels()).To(BeNil())
	})

	It("is a no-op when the parent has labels but no instance-id key", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.EnvironmentLabel: "main"})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(BeNil())
	})

	It("is a no-op when the parent's instance-id value is empty", func() {
		// An empty parent value carries no useful identity and must not be
		// propagated, otherwise children would acquire an "empty" instance-id
		// label that conflicts with the empty-id allow-all predicate.
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: ""})
		child.SetLabels(map[string]string{promoterv1alpha1.EnvironmentLabel: "main"})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).NotTo(HaveKey(promoterv1alpha1.InstanceIDLabel))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, "main"))
	})
})
