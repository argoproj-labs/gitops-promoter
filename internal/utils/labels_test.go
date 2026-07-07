package utils_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/instanceid"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	testInstanceID = "wave-0"
)

var _ = Describe("StampInstanceIDLabel", func() {
	BeforeEach(func() {
		instanceid.ResetControllerInstanceIDForTest()
	})

	AfterEach(func() {
		instanceid.ResetControllerInstanceIDForTest()
	})

	It("returns an empty map when labels is nil and install is default", func() {
		instanceid.SetControllerInstanceIDForTest(nil)
		labels := utils.StampInstanceIDLabel(nil)
		Expect(labels).NotTo(BeNil())
		Expect(labels).To(BeEmpty())
	})

	It("preserves existing labels on default install", func() {
		instanceid.SetControllerInstanceIDForTest(nil)
		labels := utils.StampInstanceIDLabel(map[string]string{"k": "v"})
		Expect(labels).To(Equal(map[string]string{"k": "v"}))
	})

	It("stamps instance-id from instanceid.ControllerInstanceID", func() {
		instanceid.SetControllerInstanceIDForTest(ptr.To(testInstanceID))
		labels := utils.StampInstanceIDLabel(map[string]string{"k": "v"})
		Expect(labels[promoterv1alpha1.InstanceIDLabel]).To(Equal(testInstanceID))
		Expect(labels["k"]).To(Equal("v"))
	})
})
