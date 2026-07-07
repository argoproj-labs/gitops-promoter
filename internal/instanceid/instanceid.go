package instanceid

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const controllerConfigurationName = "promoter-controller-configuration"

var (
	controllerInstanceID *string
	bootstrapped         bool
)

// BootstrapControllerInstanceID reads ControllerConfiguration.spec.instanceID via direct API
// (rest.Config, not informer) and caches it for the process lifetime. Subsequent calls are
// no-ops that return the cached value. Must be called before any consumer needs ControllerInstanceID.
func BootstrapControllerInstanceID(ctx context.Context, cfg *rest.Config, namespace string) error {
	if bootstrapped {
		return nil
	}
	id, err := readInstanceIDFromAPI(ctx, cfg, namespace)
	if err != nil {
		return err
	}
	setControllerInstanceID(id)
	return nil
}

func readInstanceIDFromAPI(ctx context.Context, cfg *rest.Config, namespace string) (*string, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(promoterv1alpha1.AddToScheme(scheme))

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap client: %w", err)
	}

	cc := &promoterv1alpha1.ControllerConfiguration{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      controllerConfigurationName,
		Namespace: namespace,
	}, cc); err != nil {
		return nil, fmt.Errorf("failed to get ControllerConfiguration %q: %w", controllerConfigurationName, err)
	}

	return cc.Spec.InstanceID, nil
}

// ControllerInstanceID returns the value set by BootstrapControllerInstanceID.
// Returns nil when spec.instanceID is unset. Panics if bootstrap was not called.
func ControllerInstanceID() *string {
	if !bootstrapped {
		panic("instanceid.ControllerInstanceID called before instanceid.BootstrapControllerInstanceID")
	}
	return controllerInstanceID
}

func setControllerInstanceID(id *string) {
	controllerInstanceID = id
	bootstrapped = true
}

// SetControllerInstanceIDForTest sets the cached controller instance ID for tests.
func SetControllerInstanceIDForTest(id *string) {
	setControllerInstanceID(id)
}

// ResetControllerInstanceIDForTest clears the bootstrap cache between tests.
func ResetControllerInstanceIDForTest() {
	controllerInstanceID = nil
	bootstrapped = false
}

func controllerInstanceIDString(id *string) string {
	if id == nil {
		return "<unset>"
	}
	return *id
}

// DriftMessage formats a drift error for cached vs live instance IDs.
func DriftMessage(cached, live *string) string {
	return fmt.Sprintf(
		"ControllerConfiguration.spec.instanceID drifted since startup (cached %s, live %s)",
		controllerInstanceIDString(cached),
		controllerInstanceIDString(live),
	)
}

// LiveInstanceIDReader returns the live ControllerConfiguration.spec.instanceID.
type LiveInstanceIDReader interface {
	GetInstanceID(ctx context.Context) (*string, error)
}

// EnsureStable returns an error when live ControllerConfiguration.spec.instanceID does not
// match the cached controller instance ID from bootstrap.
func EnsureStable(ctx context.Context, liveReader LiveInstanceIDReader) error {
	live, err := liveReader.GetInstanceID(ctx)
	if err != nil {
		return fmt.Errorf("read live ControllerConfiguration instanceID: %w", err)
	}
	cached := ControllerInstanceID()
	if ptr.Equal(cached, live) {
		return nil
	}
	return fmt.Errorf("%s", DriftMessage(cached, live))
}
