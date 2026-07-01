package settings

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadInstanceID reads promoter-controller-configuration from the install namespace via a
// direct (non-cached) API client. Returns nil when spec.instanceID is unset (default install:
// only unlabeled resources). Returns a non-nil pointer when multi-install mode is configured.
//
// This must be called before the controller-runtime manager is created: instanceID configures
// the informer cache partition that the manager (and settings Manager clients) are built on.
func ReadInstanceID(ctx context.Context, cfg *rest.Config, namespace string) (*string, error) {
	c, err := client.New(cfg, client.Options{Scheme: utils.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap client: %w", err)
	}

	cc := &promoterv1alpha1.ControllerConfiguration{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      ControllerConfigurationName,
		Namespace: namespace,
	}, cc); err != nil {
		return nil, fmt.Errorf("failed to get ControllerConfiguration %q: %w", ControllerConfigurationName, err)
	}

	return cc.Spec.InstanceID, nil
}

// InstanceIDsEqual reports whether two optional instance ID values are equivalent.
// Both nil means default install; one nil and one non-nil are different.
func InstanceIDsEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
