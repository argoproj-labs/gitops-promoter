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

	defaultPromotionStrategyRequeueDuration    = "5m"
	defaultChangeTransferPolicyRequeueDuration = "5m"
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

	if controllerConfiguration.Spec.PromotionStrategyRequeueDuration == "" {
		controllerConfiguration.Spec.PromotionStrategyRequeueDuration = defaultPromotionStrategyRequeueDuration
	}

	duration, err := time.ParseDuration(controllerConfiguration.Spec.PromotionStrategyRequeueDuration)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to parse requeue duration: %w", err)
	}

	return duration, nil
}

func (m *Manager) GetChangeTransferPolicyRequeueDuration(ctx context.Context) (time.Duration, error) {
	controllerConfiguration, err := m.GetControllerConfiguration(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to get controller configuration: %w", err)
	}

	if controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration == "" {
		controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration = defaultChangeTransferPolicyRequeueDuration
	}

	duration, err := time.ParseDuration(controllerConfiguration.Spec.ChangeTransferPolicyRequeueDuration)
	if err != nil {
		return time.Duration(0), fmt.Errorf("failed to parse change transfer policy requeue duration: %w", err)
	}

	return duration, nil
}

func NewManager(client client.Client, config ManagerConfig) *Manager {
	return &Manager{
		client: client,
		config: config,
	}
}
