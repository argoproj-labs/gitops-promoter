package instanceid_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/instanceid"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

func TestInstanceID(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Instance ID Suite")
}

var _ = Describe("ControllerInstanceID", func() {
	AfterEach(func() {
		instanceid.ResetControllerInstanceIDForTest()
	})

	It("panics when bootstrap was not called", func() {
		Expect(func() {
			_ = instanceid.ControllerInstanceID()
		}).To(Panic())
	})

	It("returns nil when spec.instanceID is unset after test bootstrap", func() {
		instanceid.SetControllerInstanceIDForTest(nil)
		Expect(instanceid.ControllerInstanceID()).To(BeNil())
	})

	It("returns the configured value after test bootstrap", func() {
		instanceid.SetControllerInstanceIDForTest(ptr.To("wave-0"))
		got := instanceid.ControllerInstanceID()
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("wave-0"))
	})

	It("returns early when already bootstrapped", func() {
		instanceid.SetControllerInstanceIDForTest(ptr.To("wave-0"))
		Expect(instanceid.BootstrapControllerInstanceID(context.Background(), nil, "default")).To(Succeed())
		got := instanceid.ControllerInstanceID()
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("wave-0"))
	})
})

var _ = Describe("DriftMessage", func() {
	It("formats cached and live values", func() {
		msg := instanceid.DriftMessage(ptr.To("wave-0"), ptr.To("wave-1"))
		Expect(msg).To(ContainSubstring("wave-0"))
		Expect(msg).To(ContainSubstring("wave-1"))
	})
})

var _ = Describe("EnsureStable", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		instanceid.ResetControllerInstanceIDForTest()
	})

	AfterEach(func() {
		instanceid.ResetControllerInstanceIDForTest()
	})

	It("returns nil when cached and live instance IDs match", func() {
		instanceid.SetControllerInstanceIDForTest(ptr.To("wave-0"))
		cc := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      settings.ControllerConfigurationName,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{
				InstanceID: ptr.To("wave-0"),
			},
		}
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cc).Build()
		mgr := settings.NewManager(cl, cl, settings.ManagerConfig{ControllerNamespace: "default"})

		Expect(instanceid.EnsureStable(ctx, mgr)).To(Succeed())
	})

	It("returns an error when live instance ID drifts from cached", func() {
		instanceid.SetControllerInstanceIDForTest(nil)
		cc := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      settings.ControllerConfigurationName,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{
				InstanceID: ptr.To("wave-0"),
			},
		}
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cc).Build()
		mgr := settings.NewManager(cl, cl, settings.ManagerConfig{ControllerNamespace: "default"})

		err := instanceid.EnsureStable(ctx, mgr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("drifted since startup"))
	})
})
