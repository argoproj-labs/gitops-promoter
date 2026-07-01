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
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, testInstanceID))
	})

	It("copies the instance-id label without clobbering existing child labels", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID})
		child.SetLabels(map[string]string{
			promoterv1alpha1.EnvironmentLabel:       testEnvBranch,
			promoterv1alpha1.PromotionStrategyLabel: "ps",
		})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, testInstanceID))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, testEnvBranch))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.PromotionStrategyLabel, "ps"))
	})

	It("overwrites the child's existing instance-id label with the parent's value", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: testInstanceID})
		child.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-stale"})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, testInstanceID))
	})

	It("is a no-op when the parent has no labels", func() {
		// nil labels on parent
		utils.CopyInstanceIDLabel(parent, child)
		Expect(child.GetLabels()).To(BeNil())
	})

	It("is a no-op when the parent has labels but no instance-id key", func() {
		parent.SetLabels(map[string]string{promoterv1alpha1.EnvironmentLabel: testEnvBranch})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).To(BeNil())
	})

	It("is a no-op when the parent's instance-id value is empty", func() {
		// An empty parent value carries no useful identity and must not be propagated.
		parent.SetLabels(map[string]string{promoterv1alpha1.InstanceIDLabel: ""})
		child.SetLabels(map[string]string{promoterv1alpha1.EnvironmentLabel: testEnvBranch})

		utils.CopyInstanceIDLabel(parent, child)

		Expect(child.GetLabels()).NotTo(HaveKey(promoterv1alpha1.InstanceIDLabel))
		Expect(child.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.EnvironmentLabel, testEnvBranch))
	})
})
