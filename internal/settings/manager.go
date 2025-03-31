package settings

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	GlobalPromotionConfigurationName = "promoter-global"
)

type ManagerConfig struct {
	GlobalNamespace string
}

type Manager struct {
	client client.Client
	config ManagerConfig
}

func (m *Manager) GetPromotionConfiguration(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	promotionConfig := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: GlobalPromotionConfigurationName, Namespace: m.config.GlobalNamespace}, promotionConfig); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return promotionConfig, nil
}

func NewManager(client client.Client, config ManagerConfig) *Manager {
	return &Manager{
		client: client,
		config: config,
	}
}
