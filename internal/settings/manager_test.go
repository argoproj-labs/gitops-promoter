package settings

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

func TestIsArgoCDLocalClusterMonitoringDisabled(t *testing.T) {
	scheme := utils.GetScheme()

	tests := []struct {
		name                         string
		config                       *promoterv1alpha1.ControllerConfiguration
		expectedDisabled             bool
		expectedError                bool
	}{
		{
			name: "monitoring disabled",
			config: &promoterv1alpha1.ControllerConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ControllerConfigurationName,
					Namespace: "test-namespace",
				},
				Spec: promoterv1alpha1.ControllerConfigurationSpec{
					DisableArgoCDLocalClusterMonitoring: true,
					PullRequest: promoterv1alpha1.PullRequestConfiguration{
						Template: promoterv1alpha1.PullRequestTemplate{
							Title:       "test",
							Description: "test",
						},
					},
					PromotionStrategyRequeueDuration:    metav1.Duration{Duration: 5 * time.Minute},
					ChangeTransferPolicyRequeueDuration: metav1.Duration{Duration: 5 * time.Minute},
					ArgoCDCommitStatusRequeueDuration:   metav1.Duration{Duration: 15 * time.Second},
					PullRequestRequeueDuration:          metav1.Duration{Duration: 5 * time.Minute},
				},
			},
			expectedDisabled: true,
			expectedError:    false,
		},
		{
			name: "monitoring enabled (default)",
			config: &promoterv1alpha1.ControllerConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ControllerConfigurationName,
					Namespace: "test-namespace",
				},
				Spec: promoterv1alpha1.ControllerConfigurationSpec{
					DisableArgoCDLocalClusterMonitoring: false,
					PullRequest: promoterv1alpha1.PullRequestConfiguration{
						Template: promoterv1alpha1.PullRequestTemplate{
							Title:       "test",
							Description: "test",
						},
					},
					PromotionStrategyRequeueDuration:    metav1.Duration{Duration: 5 * time.Minute},
					ChangeTransferPolicyRequeueDuration: metav1.Duration{Duration: 5 * time.Minute},
					ArgoCDCommitStatusRequeueDuration:   metav1.Duration{Duration: 15 * time.Second},
					PullRequestRequeueDuration:          metav1.Duration{Duration: 5 * time.Minute},
				},
			},
			expectedDisabled: false,
			expectedError:    false,
		},
		{
			name:             "config not found",
			config:           nil,
			expectedDisabled: false,
			expectedError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client
			var objs []runtime.Object
			if tt.config != nil {
				objs = append(objs, tt.config)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			// Create manager
			mgr := NewManager(fakeClient, ManagerConfig{
				ControllerNamespace: "test-namespace",
			})

			// Test
			ctx := context.Background()
			disabled, err := mgr.IsArgoCDLocalClusterMonitoringDisabled(ctx)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedDisabled, disabled)
			}
		})
	}
}
