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
)

type ManagerConfig struct {
	ControllerNamespace string
}

type Manager struct {
	client client.Client
	config ManagerConfig
}

func (m *Manager) GetControllerConfiguration(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ControllerConfigurationName, Namespace: m.config.ControllerNamespace}, controllerConfiguration); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return controllerConfiguration, nil
}

func (m *Manager) GetPromotionStrategyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.PromotionStrategyRequeueDuration.Duration, nil
}

func (m *Manager) GetChangeTransferPolicyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration.Duration, nil
}

func (m *Manager) GetArgoCDCommitStatusRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.ArgoCDCommitStatusRequeueDuration.Duration, nil
}

func (m *Manager) GetPullRequestRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	return controllerConfiguration.Spec.PullRequestRequeueDuration.Duration, nil
}

func (m *Manager) GetControllerNamespace() string {
	return m.config.ControllerNamespace
}

func NewManager(client client.Client, config ManagerConfig) *Manager {
	return &Manager{
		client: client,
		config: config,
	}
}
