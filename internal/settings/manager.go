package settings

import (
	"context"
	"errors"
	"fmt"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ControllerConfigurationName is the name of the global controller configuration resource.
	ControllerConfigurationName = "promoter-controller-configuration"
)

// ControllerConfigurationTypes is a constraint that defines the set of controller configuration types
// that include a WorkQueue specification. This type constraint is used with generic functions to
// provide type-safe access to WorkQueue configurations across different controller types.
//
// The following configuration types satisfy this constraint:
//   - PromotionStrategyConfiguration
//   - ChangeTransferPolicyConfiguration
//   - PullRequestConfiguration
//   - CommitStatusConfiguration
//   - ArgoCDCommitStatusConfiguration
//   - TimedCommitStatusConfiguration
//   - GitCommitStatusConfiguration
type ControllerConfigurationTypes interface {
	promoterv1alpha1.PromotionStrategyConfiguration |
		promoterv1alpha1.ChangeTransferPolicyConfiguration |
		promoterv1alpha1.PullRequestConfiguration |
		promoterv1alpha1.CommitStatusConfiguration |
		promoterv1alpha1.ArgoCDCommitStatusConfiguration |
		promoterv1alpha1.TimedCommitStatusConfiguration |
		promoterv1alpha1.GitCommitStatusConfiguration
}

// ControllerResultTypes is a constraint that defines the set of result types returned by controller
// reconciliation methods. This type constraint is used with generic functions to provide type-safe
// handling of different controller result types.
//
// The following result types satisfy this constraint:
//   - controllerruntime.Result (used in single-cluster controllers)
//   - multiclusterruntime.Result (used in multi-cluster controllers)
type ControllerResultTypes interface {
	comparable
}

// ManagerConfig holds the static configuration for the settings Manager.
//
// This configuration is provided at Manager creation time and contains runtime parameters
// that don't change during the Manager's lifetime. It's used in conjunction with the
// dynamically-fetched ControllerConfiguration resource to provide complete configuration
// information for the controller.
type ManagerConfig struct {
	// ControllerNamespace is the namespace where the promoter controller is running.
	// This namespace is used when fetching the ControllerConfiguration resource from the cluster.
	ControllerNamespace string
}

// Manager is responsible for managing the global controller configuration for the promoter controller.
// It provides methods to retrieve controller-specific settings such as requeue durations, rate limiters,
// and concurrency limits from the cluster's ControllerConfiguration resource.
type Manager struct {
	// client is the Kubernetes client used to fetch the ControllerConfiguration resource from the cluster.
	// This client uses the cache and should be used during normal reconciliation.
	client client.Client
	// apiReader is a non-cached client that reads directly from the API server.
	// This is used during SetupWithManager when the cache is not yet started.
	apiReader client.Reader
	// config holds the static configuration for the Manager, including the controller's namespace.
	config ManagerConfig
}

// getControllerConfiguration retrieves the global controller configuration for the promoter controller.
//
// This function fetches the ControllerConfiguration resource from the cluster, which contains
// the global settings for all controllers including WorkQueue configurations, rate limiters,
// and other controller-specific settings.
//
// Important: This method requires the manager's cache to be started. Do not call this method
// during SetupWithManager. Instead, call it from within your Reconcile method or use
// getControllerConfigurationDirect for setup-time configuration.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//
// Returns the ControllerConfiguration resource, or an error if it cannot be retrieved from the cluster.
func (m *Manager) getControllerConfiguration(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ControllerConfigurationName, Namespace: m.config.ControllerNamespace}, controllerConfiguration); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return controllerConfiguration, nil
}

// getControllerConfigurationDirect retrieves the global controller configuration directly from the API server.
//
// This function bypasses the cache and reads directly from the API server, making it safe to call
// during SetupWithManager before the cache has started. Use this method when you need to read
// configuration during controller initialization.
//
// For normal reconciliation operations, prefer getControllerConfiguration which uses the cache.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//
// Returns the ControllerConfiguration resource, or an error if it cannot be retrieved from the cluster.
func (m *Manager) getControllerConfigurationDirect(ctx context.Context) (*promoterv1alpha1.ControllerConfiguration, error) {
	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{}
	if err := m.apiReader.Get(ctx, client.ObjectKey{Name: ControllerConfigurationName, Namespace: m.config.ControllerNamespace}, controllerConfiguration); err != nil {
		return nil, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return controllerConfiguration, nil
}

// GetControllerNamespace returns the namespace where the controller is running.
func (m *Manager) GetControllerNamespace() string {
	return m.config.ControllerNamespace
}

// GetArgoCDCommitStatusControllersWatchLocalApplicationsDirect retrieves the WatchLocalApplications setting from the ArgoCDCommitStatus configuration
// using a non-cached read.
//
// This function bypasses the cache and reads directly from the API server, making it safe to call
// during SetupWithManager before the cache has started. Use this to configure controller options
// at build time based on the ControllerConfiguration resource.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//   - m: Manager instance with access to the cluster client
//
// Returns the configured WatchLocalApplications value, or an error if the configuration cannot be retrieved.
func (m *Manager) GetArgoCDCommitStatusControllersWatchLocalApplicationsDirect(ctx context.Context) (bool, error) {
	config, err := m.getControllerConfigurationDirect(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get controller configuration: %w", err)
	}
	return config.Spec.ArgoCDCommitStatus.WatchLocalApplications, nil
}

// GetPullRequestControllersTemplate retrieves the PullRequest template configuration.
//
// This function fetches the ControllerConfiguration resource from the cluster and extracts
// the PullRequest template settings. It requires the manager's cache to be started, so do not
// call this method during SetupWithManager. Instead, call it from within your Reconcile method.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//
// Returns the PullRequestTemplate configuration, or an error if it cannot be retrieved.
func (m *Manager) GetPullRequestControllersTemplate(ctx context.Context) (promoterv1alpha1.PullRequestTemplate, error) {
	config, err := m.getControllerConfiguration(ctx)
	if err != nil {
		return promoterv1alpha1.PullRequestTemplate{}, fmt.Errorf("failed to get controller configuration: %w", err)
	}
	return config.Spec.PullRequest.Template, nil
}

// GetRequeueDuration retrieves the requeue duration for a specific controller type.
// The type parameter T must satisfy the ControllerConfigurationTypes constraint.
//
// This function queries the global ControllerConfiguration and extracts the RequeueDuration
// from the WorkQueue specification for the given controller type.
//
// Important: This method requires the manager's cache to be started.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//   - m: Manager instance with access to the cluster client
//
// Returns the configured requeue duration, or an error if the configuration cannot be retrieved.
func GetRequeueDuration[T ControllerConfigurationTypes](ctx context.Context, m *Manager) (time.Duration, error) {
	workQueue, err := getWorkQueueForController[T](ctx, m, false)
	if err != nil {
		return 0, fmt.Errorf("failed to get work queue for controller: %w", err)
	}

	return workQueue.RequeueDuration.Duration, nil
}

// GetMaxConcurrentReconcilesDirect retrieves the maximum number of concurrent reconciles for a specific controller type using a non-cached read.
// The type parameter T must satisfy the ControllerConfigurationTypes constraint.
//
// This function bypasses the cache and reads directly from the API server, making it safe to call
// during SetupWithManager before the cache has started. Use this to configure controller options
// at build time based on the ControllerConfiguration resource.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//   - m: Manager instance with access to the cluster client
//
// Returns the configured maximum concurrent reconciles, or an error if the configuration cannot be retrieved.
func GetMaxConcurrentReconcilesDirect[T ControllerConfigurationTypes](ctx context.Context, m *Manager) (int, error) {
	workQueue, err := getWorkQueueForController[T](ctx, m, true)
	if err != nil {
		return 0, fmt.Errorf("failed to get work queue for controller: %w", err)
	}

	return workQueue.MaxConcurrentReconciles, nil
}

// GetRateLimiterDirect retrieves a configured rate limiter for a specific controller type using a non-cached read.
// The type parameter T must satisfy the ControllerConfigurationTypes constraint.
// The type parameter R is the request type for the rate limiter (e.g., ctrl.Request or mcreconcile.Request).
//
// This function bypasses the cache and reads directly from the API server, making it safe to call
// during SetupWithManager before the cache has started. Use this to configure controller options
// at build time based on the ControllerConfiguration resource.
//
// The returned rate limiter can be one of several types (FastSlow, ExponentialFailure, Bucket, or MaxOf)
// depending on the configuration. See buildRateLimiter for details on supported limiter types.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//   - m: Manager instance with access to the cluster client
//
// Returns a configured TypedRateLimiter for the specified request type, or an error if the
// configuration cannot be retrieved or the rate limiter cannot be constructed.
func GetRateLimiterDirect[T ControllerConfigurationTypes, R ControllerResultTypes](ctx context.Context, m *Manager) (workqueue.TypedRateLimiter[R], error) {
	workQueue, err := getWorkQueueForController[T](ctx, m, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get work queue for controller: %w", err)
	}

	limiter, err := buildRateLimiter[R](workQueue.RateLimiter)
	if err != nil {
		return nil, fmt.Errorf("failed to build rate limiter: %w", err)
	}

	return limiter, nil
}

// getWorkQueueForController retrieves the WorkQueue configuration for a specific controller type.
// The type parameter T must satisfy the ControllerConfigurationTypes constraint.
//
// This is an internal helper function that uses a type switch to map the generic type parameter
// to the corresponding WorkQueue configuration in the ControllerConfiguration spec. This approach
// replaces the need for multiple controller-specific getter methods with a single generic implementation.
//
// Parameters:
//   - ctx: Context for the request, used for cancellation and deadlines
//   - m: Manager instance with access to the cluster client
//   - direct: If true, bypasses the cache and reads directly from the API server
//
// Returns the WorkQueue configuration for the specified controller type, or an error if the
// configuration cannot be retrieved or the type is unsupported.
func getWorkQueueForController[T ControllerConfigurationTypes](ctx context.Context, m *Manager, direct bool) (promoterv1alpha1.WorkQueue, error) {
	var config *promoterv1alpha1.ControllerConfiguration
	var err error

	if direct {
		config, err = m.getControllerConfigurationDirect(ctx)
	} else {
		config, err = m.getControllerConfiguration(ctx)
	}

	if err != nil {
		return promoterv1alpha1.WorkQueue{}, fmt.Errorf("failed to get controller configuration: %w", err)
	}

	var controllerConfig T
	switch cfg := any(controllerConfig).(type) {
	case promoterv1alpha1.PromotionStrategyConfiguration:
		return config.Spec.PromotionStrategy.WorkQueue, nil
	case promoterv1alpha1.ChangeTransferPolicyConfiguration:
		return config.Spec.ChangeTransferPolicy.WorkQueue, nil
	case promoterv1alpha1.PullRequestConfiguration:
		return config.Spec.PullRequest.WorkQueue, nil
	case promoterv1alpha1.CommitStatusConfiguration:
		return config.Spec.CommitStatus.WorkQueue, nil
	case promoterv1alpha1.ArgoCDCommitStatusConfiguration:
		return config.Spec.ArgoCDCommitStatus.WorkQueue, nil
	case promoterv1alpha1.TimedCommitStatusConfiguration:
		return config.Spec.TimedCommitStatus.WorkQueue, nil
	case promoterv1alpha1.GitCommitStatusConfiguration:
		return config.Spec.GitCommitStatus.WorkQueue, nil
	default:
		return promoterv1alpha1.WorkQueue{}, fmt.Errorf("unsupported configuration type: %T", cfg)
	}
}

// buildRateLimiter constructs a workqueue.TypedRateLimiter from the RateLimiter configuration.
//
// This function supports composite rate limiting strategies by handling both leaf-level limiters
// (FastSlow, ExponentialFailure, Bucket) and the MaxOf combiner, which returns the maximum delay
// from multiple rate limiters. This allows for sophisticated rate limiting behavior that can
// combine multiple strategies.
//
// The function delegates to buildRateLimiterType for constructing individual rate limiter instances.
// See k8s.io/client-go/util/workqueue for details on rate limiter implementations.
//
// The type parameter R is the request type for the rate limiter (e.g., ctrl.Request or mcreconcile.Request).
//
// Returns a configured TypedRateLimiter, or an error if no valid configuration is found.
func buildRateLimiter[R ControllerResultTypes](rateLimiter promoterv1alpha1.RateLimiter) (workqueue.TypedRateLimiter[R], error) {
	if rateLimiter.FastSlow != nil || rateLimiter.ExponentialFailure != nil || rateLimiter.Bucket != nil {
		// If any of the leaf-level limiters are set, use buildRateLimiterType to handle them
		return buildRateLimiterType[R](rateLimiter.RateLimiterTypes)
	}

	// Handle MaxOf rate limiter
	if len(rateLimiter.MaxOf) > 0 {
		var limiters []workqueue.TypedRateLimiter[R]
		for _, limiterConfig := range rateLimiter.MaxOf {
			limiter, err := buildRateLimiterType[R](limiterConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to build nested rate limiter: %w", err)
			}
			limiters = append(limiters, limiter)
		}
		return workqueue.NewTypedMaxOfRateLimiter[R](limiters...), nil
	}

	return nil, errors.New("no valid rate limiter configuration found")
}

// buildRateLimiterType constructs a specific workqueue.TypedRateLimiter based on RateLimiterTypes configuration.
//
// This function creates one of three types of rate limiters based on the configuration:
//
//   - FastSlow: Returns fastDelay for the first maxFastAttempts failures, then slowDelay for subsequent failures.
//     Useful for quickly retrying transient errors while backing off for persistent failures.
//
//   - ExponentialFailure: Increases delay exponentially with each failure, starting at baseDelay and capping at maxDelay.
//     Standard approach for backing off when operations fail repeatedly.
//
//   - Bucket: Token bucket rate limiter that allows bursts up to 'bucket' size while maintaining 'qps' queries per second.
//     Prevents overwhelming external APIs while allowing occasional bursts of activity.
//
// The type parameter R is the request type for the rate limiter (e.g., ctrl.Request or mcreconcile.Request).
//
// See k8s.io/client-go/util/workqueue for implementation details of each limiter type.
//
// Returns a configured TypedRateLimiter, or an error if no valid configuration is found.
func buildRateLimiterType[R ControllerResultTypes](config promoterv1alpha1.RateLimiterTypes) (workqueue.TypedRateLimiter[R], error) {
	// Handle FastSlow rate limiter
	if config.FastSlow != nil {
		fastDelay := config.FastSlow.FastDelay.Duration
		slowDelay := config.FastSlow.SlowDelay.Duration
		maxFastAttempts := config.FastSlow.MaxFastAttempts
		return workqueue.NewTypedItemFastSlowRateLimiter[R](fastDelay, slowDelay, maxFastAttempts), nil
	}

	// Handle ExponentialFailure rate limiter
	if config.ExponentialFailure != nil {
		baseDelay := config.ExponentialFailure.BaseDelay.Duration
		maxDelay := config.ExponentialFailure.MaxDelay.Duration
		return workqueue.NewTypedItemExponentialFailureRateLimiter[R](baseDelay, maxDelay), nil
	}

	// Handle Bucket rate limiter
	if config.Bucket != nil {
		qps := config.Bucket.Qps
		bucket := config.Bucket.Bucket
		return &workqueue.TypedBucketRateLimiter[R]{
			Limiter: rate.NewLimiter(rate.Limit(qps), bucket),
		}, nil
	}

	return nil, errors.New("no valid rate limiter configuration found")
}

// NewManager creates a new settings Manager instance with the provided client and configuration.
//
// The Manager is used to retrieve and manage controller configuration settings from the cluster's
// ControllerConfiguration resource. It provides a centralized interface for accessing WorkQueue
// settings, rate limiters, and other controller-specific configurations.
//
// Parameters:
//   - client: A Kubernetes client.Client for accessing cluster resources (uses cache)
//   - apiReader: A client.Reader that reads directly from the API server without cache (for setup-time reads)
//   - config: ManagerConfig containing the controller's namespace and other static settings
//
// Returns a configured Manager instance ready for use.
func NewManager(client client.Client, apiReader client.Reader, config ManagerConfig) *Manager {
	return &Manager{
		client:    client,
		apiReader: apiReader,
		config:    config,
	}
}
