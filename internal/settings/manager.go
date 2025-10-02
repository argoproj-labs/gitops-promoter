package settings

import (
	"context"
	"fmt"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ControllerConfigurationName is the name of the global controller configuration resource.
	ControllerConfigurationName = "promoter-controller-configuration"
)

// ManagerConfig holds the configuration for the settings Manager.
type ManagerConfig struct {
	// ControllerNamespace is the namespace where the promoter controller is running.
	ControllerNamespace string
}

// Manager is responsible for managing the global controller configuration for the promoter controller.
type Manager struct {
	client client.Client
	config ManagerConfig
}

// GetControllerConfiguration retrieves the global controller configuration for the promoter controller.
func (m *Manager) GetControllerConfiguration(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ControllerConfigurationName, Namespace: m.config.ControllerNamespace}, controllerConfiguration); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return controllerConfiguration, nil
}

// GetPromotionStrategyRequeueDuration returns the duration after which PromotionStrategies should be requeued.
func (m *Manager) GetPromotionStrategyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.PromotionStrategyRequeueDuration.Duration, nil
}

// GetChangeTransferPolicyRequeueDuration returns the duration after which ChangeTransferPolicies should be requeued.
func (m *Manager) GetChangeTransferPolicyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration.Duration, nil
}

// GetArgoCDCommitStatusRequeueDuration returns the duration after which ArgoCDCommitStatuses should be requeued.
func (m *Manager) GetArgoCDCommitStatusRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.ArgoCDCommitStatusRequeueDuration.Duration, nil
}

// GetPullRequestRequeueDuration returns the duration after which pull requests should be requeued.
func (m *Manager) GetPullRequestRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.PullRequestRequeueDuration.Duration, nil
}

// GetWebhookMaxPayloadSize returns the maximum allowed webhook payload size.
func (m *Manager) GetWebhookMaxPayloadSize(ctx context.Context) (resource.Quantity, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.Webhook.MaxPayloadSize, nil
}

// GetControllerNamespace returns the namespace where the controller is running.
func (m *Manager) GetControllerNamespace() string {
	return m.config.ControllerNamespace
}

// NewManager creates a new settings Manager instance with the provided client and configuration.
func NewManager(client client.Client, config ManagerConfig) *Manager {
	return &Manager{
		client: client,
		config: config,
	}
}
