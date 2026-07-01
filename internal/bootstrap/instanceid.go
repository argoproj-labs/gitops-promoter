package bootstrap

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadInstanceID reads promoter-controller-configuration from the install namespace via a
// direct (non-cached) API client. Returns nil when spec.instanceID is unset (default install:
// only unlabeled resources). Returns a non-nil pointer when multi-install mode is configured.
func ReadInstanceID(ctx context.Context, cfg *rest.Config, namespace string) (*string, error) {
	c, err := client.New(cfg, client.Options{Scheme: utils.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap client: %w", err)
	}

	cc := &promoterv1alpha1.ControllerConfiguration{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      settings.ControllerConfigurationName,
		Namespace: namespace,
	}, cc); err != nil {
		return nil, fmt.Errorf("failed to get ControllerConfiguration %q: %w", settings.ControllerConfigurationName, err)
	}

	return cc.Spec.InstanceID, nil
}
