/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ControllerConfigurationSpec defines the desired state of ControllerConfiguration.
//
// This spec contains the global configuration for all controllers in the promoter system.
// Each controller has its own configuration section that specifies WorkQueue settings,
// rate limiters, and other controller-specific parameters. All fields should be required,
// with defaults set in manifests rather than in code.
type ControllerConfigurationSpec struct {
	// PromotionStrategy contains the configuration for the PromotionStrategy controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	PromotionStrategy PromotionStrategyConfiguration `json:"promotionStrategy"`

	// ChangeTransferPolicy contains the configuration for the ChangeTransferPolicy controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	ChangeTransferPolicy ChangeTransferPolicyConfiguration `json:"changeTransferPolicy"`

	// PullRequest contains the configuration for the PullRequest controller,
	// including WorkQueue settings and pull request template configuration.
	// +required
	PullRequest PullRequestConfiguration `json:"pullRequest"`

	// CommitStatus contains the configuration for the CommitStatus controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	CommitStatus CommitStatusConfiguration `json:"commitStatus"`

	// ArgoCDCommitStatus contains the configuration for the ArgoCDCommitStatus controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	ArgoCDCommitStatus ArgoCDCommitStatusConfiguration `json:"argocdCommitStatus"`

	// TimedCommitStatus contains the configuration for the TimedCommitStatus controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	TimedCommitStatus TimedCommitStatusConfiguration `json:"timedCommitStatus"`

	// GitCommitStatus contains the configuration for the GitCommitStatus controller,
	// including WorkQueue settings that control reconciliation behavior.
	// +required
	GitCommitStatus GitCommitStatusConfiguration `json:"gitCommitStatus"`
}

// PromotionStrategyConfiguration defines the configuration for the PromotionStrategy controller.
//
// This configuration controls how the PromotionStrategy controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type PromotionStrategyConfiguration struct {
	// WorkQueue contains the work queue configuration for the PromotionStrategy controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// ChangeTransferPolicyConfiguration defines the configuration for the ChangeTransferPolicy controller.
//
// This configuration controls how the ChangeTransferPolicy controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type ChangeTransferPolicyConfiguration struct {
	// WorkQueue contains the work queue configuration for the ChangeTransferPolicy controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// PullRequestConfiguration defines the configuration for the PullRequest controller.
//
// This configuration controls how the PullRequest controller processes reconciliation requests
// and generates pull requests, including WorkQueue settings and template configuration.
type PullRequestConfiguration struct {
	// Template is the template configuration used to generate pull request titles and descriptions.
	// Uses Go template syntax with Sprig functions available.
	// +required
	Template PullRequestTemplate `json:"template"`

	// WorkQueue contains the work queue configuration for the PullRequest controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// CommitStatusConfiguration defines the configuration for the CommitStatus controller.
//
// This configuration controls how the CommitStatus controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type CommitStatusConfiguration struct {
	// WorkQueue contains the work queue configuration for the CommitStatus controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// ArgoCDCommitStatusConfiguration defines the configuration for the ArgoCDCommitStatus controller.
//
// This configuration controls how the ArgoCDCommitStatus controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type ArgoCDCommitStatusConfiguration struct {
	// WorkQueue contains the work queue configuration for the ArgoCDCommitStatus controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`

	// WatchLocalApplications controls whether the controller monitors Argo CD Applications
	// in the local cluster. When false, the controller will only watch Applications in remote clusters
	// configured via kubeconfig secrets. This is useful when the Argo CD Application CRD is not installed
	// in the local cluster or when all Applications are deployed to remote clusters.
	// +required
	// +kubebuilder:default=true
	WatchLocalApplications bool `json:"watchLocalApplications"`
}

// TimedCommitStatusConfiguration defines the configuration for the TimedCommitStatus controller.
//
// This configuration controls how the TimedCommitStatus controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type TimedCommitStatusConfiguration struct {
	// WorkQueue contains the work queue configuration for the TimedCommitStatus controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// GitCommitStatusConfiguration defines the configuration for the GitCommitStatus controller.
//
// This configuration controls how the GitCommitStatus controller processes reconciliation
// requests, including requeue intervals, concurrency limits, and rate limiting behavior.
type GitCommitStatusConfiguration struct {
	// WorkQueue contains the work queue configuration for the GitCommitStatus controller.
	// This includes requeue duration, maximum concurrent reconciles, and rate limiter settings.
	// +required
	WorkQueue WorkQueue `json:"workQueue"`
}

// WorkQueue defines the work queue configuration for a controller.
//
// This configuration directly correlates to parameters used with Kubernetes client-go work queues.
// It controls how frequently resources are reconciled, how many reconciliations can run concurrently,
// and how rate limiting is applied to prevent overwhelming external systems.
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue for implementation details.
type WorkQueue struct {
	// RequeueDuration specifies how frequently resources should be requeued for automatic reconciliation.
	// This creates a periodic reconciliation loop that ensures the desired state is maintained even
	// without external triggers. Format follows Go's time.Duration syntax (e.g., "5m" for 5 minutes).
	// +required
	RequeueDuration metav1.Duration `json:"requeueDuration"`

	// MaxConcurrentReconciles defines the maximum number of concurrent reconcile operations
	// that can run for this controller. Higher values increase throughput but consume more
	// resources. Must be at least 1.
	// +required
	// +Validation:Minimum=1
	MaxConcurrentReconciles int `json:"maxConcurrentReconciles"`

	// RateLimiter defines the rate limiting strategy for the controller's work queue.
	// Rate limiting controls how quickly failed reconciliations are retried and helps
	// prevent overwhelming external APIs or systems.
	// +required
	RateLimiter RateLimiter `json:"rateLimiter"`
}

// ExponentialFailure defines an exponential backoff rate limiter configuration.
//
// This rate limiter increases the delay exponentially with each consecutive failure, starting at
// BaseDelay and capping at MaxDelay. This is useful for backing off when operations fail repeatedly,
// reducing load on external systems while they recover. The delay doubles with each failure until
// reaching the maximum.
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue#NewTypedItemExponentialFailureRateLimiter
type ExponentialFailure struct {
	// BaseDelay is the initial delay after the first failure. Subsequent failures will exponentially
	// increase this delay (2x, 4x, 8x, etc.) until MaxDelay is reached.
	// Format follows Go's time.Duration syntax (e.g., "1s" for 1 second).
	// +required
	BaseDelay metav1.Duration `json:"baseDelay"`

	// MaxDelay is the maximum delay between retry attempts. Once the exponential backoff reaches
	// this value, all subsequent retries will use this delay.
	// Format follows Go's time.Duration syntax (e.g., "1m" for 1 minute).
	// +required
	MaxDelay metav1.Duration `json:"maxDelay"`
}

// RateLimiter defines the rate limiting configuration for controllers.
//
// This type supports both simple rate limiting strategies (FastSlow, ExponentialFailure, Bucket)
// and composite strategies using MaxOf, which returns the maximum delay from multiple rate limiters.
// This allows for sophisticated rate limiting behavior that combines multiple strategies.
//
// Exactly one of the rate limiter types or MaxOf must be specified.
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue for rate limiter implementation details.
// +kubebuilder:validation:AtMostOneOf=fastSlow;exponentialFailure;bucket;maxOf
type RateLimiter struct {
	// RateLimiterTypes can be one of: FastSlow, ExponentialFailure, or Bucket.
	// +optional
	RateLimiterTypes `json:",inline"`

	// MaxOf allows combining multiple rate limiters, where the maximum delay from all
	// limiters is used. This enables sophisticated rate limiting that respects multiple
	// constraints simultaneously (e.g., both per-item exponential backoff and global rate limits).
	// +optional
	// +kubebuilder:validation:MaxItems=3
	MaxOf []RateLimiterTypes `json:"maxOf,omitempty"`
}

// RateLimiterTypes defines the different algorithms available for rate limiting.
//
// Exactly one of the three rate limiter types must be specified:
//   - FastSlow: Quick retry for transient errors, then slower retry for persistent failures
//   - ExponentialFailure: Standard exponential backoff for repeated failures
//   - Bucket: Token bucket algorithm for controlling overall request rate
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue for implementation details.
// +kubebuilder:validation:AtMostOneOf=fastSlow;exponentialFailure;bucket
type RateLimiterTypes struct {
	// FastSlow rate limiter provides fast retries initially, then switches to slow retries.
	// Useful for quickly retrying transient errors while backing off for persistent failures.
	// +optional
	FastSlow *FastSlow `json:"fastSlow,omitempty"`

	// ExponentialFailure rate limiter increases delay exponentially with each failure.
	// Standard approach for backing off when operations fail repeatedly.
	// +optional
	ExponentialFailure *ExponentialFailure `json:"exponentialFailure,omitempty"`

	// Bucket rate limiter uses a token bucket algorithm to control request rate.
	// Allows bursts while maintaining an average rate limit.
	// +optional
	Bucket *Bucket `json:"bucket,omitempty"`
}

// FastSlow defines a rate limiter that uses different delays based on failure count.
//
// This rate limiter returns FastDelay for the first MaxFastAttempts failures, then switches
// to SlowDelay for all subsequent failures. This is useful for quickly retrying transient
// errors while backing off for persistent failures, without the exponential growth of
// ExponentialFailure.
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue#NewTypedItemFastSlowRateLimiter
type FastSlow struct {
	// FastDelay is the delay used for the first MaxFastAttempts retry attempts.
	// Format follows Go's time.Duration syntax (e.g., "100ms" for 100 milliseconds).
	// +required
	FastDelay metav1.Duration `json:"fastDelay"`

	// SlowDelay is the delay used for retry attempts after MaxFastAttempts have been exhausted.
	// Format follows Go's time.Duration syntax (e.g., "10s" for 10 seconds).
	// +required
	SlowDelay metav1.Duration `json:"slowDelay"`

	// MaxFastAttempts is the number of retry attempts that use FastDelay before switching to SlowDelay.
	// Must be at least 1.
	// +required
	// +Validation:Minimum=1
	MaxFastAttempts int `json:"maxFastAttempts"`
}

// Bucket defines a token bucket rate limiter configuration.
//
// This rate limiter uses the token bucket algorithm to control the rate of operations.
// Tokens are added to the bucket at a rate of Qps per second, up to a maximum of Bucket tokens.
// Each operation consumes one token. This allows for bursts of activity up to the bucket size
// while maintaining an average rate of Qps operations per second.
//
// See https://pkg.go.dev/k8s.io/client-go/util/workqueue#TypedBucketRateLimiter
type Bucket struct {
	// Qps (queries per second) is the rate at which tokens are added to the bucket.
	// This defines the sustained rate limit for operations. Must be non-negative.
	// +required
	// +Validation:Minimum=0
	Qps int `json:"qps"`

	// Bucket is the maximum number of tokens that can be accumulated in the bucket.
	// This defines the maximum burst size - how many operations can occur in rapid
	// succession before rate limiting takes effect. Must be non-negative.
	// +required
	// +Validation:Minimum=0
	Bucket int `json:"bucket"`
}

// PullRequestTemplate defines the template configuration for generating pull requests.
//
// Templates use Go template syntax and have access to Sprig functions for flexible string
// manipulation and formatting. The template receives context about the promotion being performed,
// which can be used to generate informative titles and descriptions.
type PullRequestTemplate struct {
	// Title is the template used to generate the title of the pull request.
	// Uses Go template syntax with Sprig functions available for string manipulation.
	// +required
	Title string `json:"title"`

	// Description is the template used to generate the body/description of the pull request.
	// Uses Go template syntax with Sprig functions available for string manipulation.
	// +required
	Description string `json:"description"`
}

// ControllerConfigurationStatus defines the observed state of ControllerConfiguration.
//
// Currently, this resource does not maintain any status information as it is a configuration-only
// resource. Status fields may be added in the future to track configuration validation or
// controller health metrics.
type ControllerConfigurationStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ControllerConfiguration is the Schema for the controllerconfigurations API.
type ControllerConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ControllerConfigurationSpec   `json:"spec,omitempty"`
	Status ControllerConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ControllerConfigurationList contains a list of ControllerConfiguration.
type ControllerConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControllerConfiguration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ControllerConfiguration{}, &ControllerConfigurationList{})
}
