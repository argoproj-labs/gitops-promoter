package settings

import (
	"context"
	"strings"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetChangeTransferPolicyCommitMessageTemplate(t *testing.T) {
	t.Run("returns configured template", func(t *testing.T) {
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
		if err != nil {
			t.Fatalf("GetChangeTransferPolicyCommitMessageTemplate() unexpected error = %v", err)
		}
		if got != cfg.Spec.ChangeTransferPolicy.CommitMessageTemplate {
			t.Fatalf("GetChangeTransferPolicyCommitMessageTemplate() = %q, want %q", got, cfg.Spec.ChangeTransferPolicy.CommitMessageTemplate)
		}
	})

	t.Run("returns upgrade guidance error for empty template", func(t *testing.T) {
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
		if err == nil {
			t.Fatal("GetChangeTransferPolicyCommitMessageTemplate() expected error, got nil")
		}

		msg := err.Error()
		if !strings.Contains(msg, "update the ControllerConfiguration CRD") {
			t.Fatalf("GetChangeTransferPolicyCommitMessageTemplate() error = %q, want CRD upgrade guidance", msg)
		}
		if !strings.Contains(msg, "latest default ControllerConfiguration custom resource") {
			t.Fatalf("GetChangeTransferPolicyCommitMessageTemplate() error = %q, want default ControllerConfiguration guidance", msg)
		}
	})
}
