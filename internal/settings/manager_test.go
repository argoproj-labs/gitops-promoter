package settings

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("GetChangeTransferPolicyCommitMessageTemplate", func() {
	It("returns configured template", func() {
		cfg := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ControllerConfigurationName,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{
				ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
					CommitMessageTemplate: "{{ .ChangeTransferPolicy.Status.Proposed.Dry.Subject }}",
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(utils.GetScheme()).WithObjects(cfg).Build()
		mgr := NewManager(cl, cl, ManagerConfig{ControllerNamespace: "default"})

		got, err := mgr.GetChangeTransferPolicyCommitMessageTemplate(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(cfg.Spec.ChangeTransferPolicy.CommitMessageTemplate))
	})

	It("returns upgrade guidance error for empty template", func() {
		cfg := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ControllerConfigurationName,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{
				ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
					CommitMessageTemplate: "   ",
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(utils.GetScheme()).WithObjects(cfg).Build()
		mgr := NewManager(cl, cl, ManagerConfig{ControllerNamespace: "default"})

		_, err := mgr.GetChangeTransferPolicyCommitMessageTemplate(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("update the ControllerConfiguration CRD"))
		Expect(err.Error()).To(ContainSubstring("latest default ControllerConfiguration custom resource"))
	})
})
