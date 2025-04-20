package settings

import (
	"context"
	"fmt"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ControllerConfigurationName = "promoter-controller-configuration"

	defaultPromotionStrategyRequeueDuration    = 5 * time.Minute
	defaultChangeTransferPolicyRequeueDuration = 5 * time.Minute
	defaultArgoCDCommitStatusRequeueDuration   = 15 * time.Second
)

type ManagerConfig struct {
	GlobalNamespace string
}

type Manager struct {
	client client.Client
	config ManagerConfig
}

func (m *Manager) GetControllerConfiguration(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ControllerConfigurationName, Namespace: m.config.GlobalNamespace}, controllerConfiguration); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return controllerConfiguration, nil
}

func (m *Manager) GetPromotionStrategyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	if controllerConfiguration.Spec.PromotionStrategyRequeueDuration == nil {
		return defaultPromotionStrategyRequeueDuration, nil
	}

	return controllerConfiguration.Spec.PromotionStrategyRequeueDuration.Duration, nil
}

func (m *Manager) GetChangeTransferPolicyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	if controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration == nil {
		return defaultChangeTransferPolicyRequeueDuration, nil
	}

	return controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration.Duration, nil
}

func (m *Manager) GetArgoCDCommitStatusRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	if controllerConfiguration.Spec.ArgoCDCommitStatusRequeueDuration == nil {
		return defaultArgoCDCommitStatusRequeueDuration, nil
	}

	return controllerConfiguration.Spec.ArgoCDCommitStatusRequeueDuration.Duration, nil
}

func NewManager(client client.Client, config ManagerConfig) *Manager {
	return &Manager{
		client: client,
		config: config,
	}
}
