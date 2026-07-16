package settings_test

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
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

func TestSettings(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Settings Suite")
}

var _ = Describe("ControllerInstanceID", func() {
	AfterEach(func() {
		settings.ResetControllerInstanceIDForTest()
	})

	It("panics when bootstrap was not called", func() {
		Expect(func() {
			_ = settings.ControllerInstanceID()
		}).To(Panic())
	})

	It("returns nil when spec.instanceID is unset after test bootstrap", func() {
		settings.SetControllerInstanceIDForTest(nil)
		Expect(settings.ControllerInstanceID()).To(BeNil())
	})

	It("returns the configured value after test bootstrap", func() {
		settings.SetControllerInstanceIDForTest(ptr.To("wave-0"))
		got := settings.ControllerInstanceID()
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("wave-0"))
	})

	It("returns early when already bootstrapped", func() {
		settings.SetControllerInstanceIDForTest(ptr.To("wave-0"))
		Expect(settings.BootstrapControllerInstanceID(context.Background(), nil, "default")).To(Succeed())
		got := settings.ControllerInstanceID()
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("wave-0"))
	})
})

var _ = Describe("DriftMessage", func() {
	It("formats cached and live values", func() {
		msg := settings.DriftMessage(ptr.To("wave-0"), ptr.To("wave-1"))
		Expect(msg).To(ContainSubstring("wave-0"))
		Expect(msg).To(ContainSubstring("wave-1"))
	})
})

var _ = Describe("EnsureInstanceIDStable", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		settings.ResetControllerInstanceIDForTest()
	})

	AfterEach(func() {
		settings.ResetControllerInstanceIDForTest()
	})

	It("returns nil when cached and live instance IDs match", func() {
		settings.SetControllerInstanceIDForTest(ptr.To("wave-0"))
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

		Expect(mgr.EnsureInstanceIDStable(ctx)).To(Succeed())
	})

	It("returns an error when live instance ID drifts from cached", func() {
		settings.SetControllerInstanceIDForTest(nil)
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

		err := mgr.EnsureInstanceIDStable(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("drifted since startup"))
	})
})
