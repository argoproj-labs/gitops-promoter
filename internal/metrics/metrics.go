package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GitOperation represents the type of git operation being performed.
type GitOperation string

const (
	// GitOperationClone is used when cloning a git repository.
	GitOperationClone GitOperation = "clone"
	// GitOperationFetch is used when fetching updates from a git repository.
	GitOperationFetch GitOperation = "fetch"
	// GitOperationFetchNotes is used when fetching git notes from a git repository.
	GitOperationFetchNotes GitOperation = "fetch-notes"
	// GitOperationPull is used when pulling changes from a git repository.
	GitOperationPull GitOperation = "pull"
	// GitOperationPush is used when pushing changes to a git repository.
	GitOperationPush GitOperation = "push"
	// GitOperationLsRemote is used when listing remote references in a git repository.
	GitOperationLsRemote GitOperation = "ls-remote"
)

// GitOperationResult represents the result of a git operation, indicating whether it was successful or failed.
type GitOperationResult string

const (
	// GitOperationResultSuccess indicates that the git operation was successful.
	GitOperationResultSuccess GitOperationResult = "success"
	// GitOperationResultFailure indicates that the git operation failed.
	GitOperationResultFailure GitOperationResult = "failure"
)

// GitOperationResultFromError converts an error to a GitOperationResult based on whether the error is nil or not.
func GitOperationResultFromError(err error) GitOperationResult {
	if err == nil {
		return GitOperationResultSuccess
	}
	return GitOperationResultFailure
}

// SCMAPI represents the type of API being used in the SCM operations.
type SCMAPI string

const (
	// SCMAPICommitStatus is used for operations related to commit statuses.
	SCMAPICommitStatus SCMAPI = "CommitStatus"
	// SCMAPIPullRequest is used for operations related to pull requests.
	SCMAPIPullRequest SCMAPI = "PullRequest"
)

// SCMOperation represents the type of operation being performed on the SCM API.
type SCMOperation string

const (
	// SCMOperationCreate is used when creating resources such as pull requests or commit statuses.
	SCMOperationCreate SCMOperation = "create"
	// SCMOperationUpdate is used when updating resources such as pull requests.
	SCMOperationUpdate SCMOperation = "update"
	// SCMOperationMerge is used when merging pull requests.
	SCMOperationMerge SCMOperation = "merge"
	// SCMOperationClose is used when closing pull requests.
	SCMOperationClose SCMOperation = "close"
	// SCMOperationList is used when listing resources, such as pull requests.
	SCMOperationList SCMOperation = "list"
	// SCMOperationGet is used when getting a single resource, such as a specific pull request.
	SCMOperationGet SCMOperation = "get"
)

// RateLimit represents the rate limit information for SCM API calls.
type RateLimit struct {
	// Limit is the maximum number of requests allowed in the current rate limit window.
	Limit int
	// Remaining is the number of requests remaining in the current rate limit window.
	Remaining int
	// ResetRemaining is the duration until the rate limit resets.
	ResetRemaining time.Duration
}

var (
	// Labels for git_operations metrics
	gitOperationLabels = []string{"git_repository", "scm_provider", "operation", "result"}

	// Labels for scm_calls metrics
	scmCallLabels = []string{"git_repository", "scm_provider", "api", "operation", "response_code"}

	// Labels for scm_calls_rate_limit metrics
	scmCallRateLimitLabels = []string{"scm_provider"}

	// git_operations_total
	gitOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "git_operations_total",
			Help: "A counter of git clone operations.",
		},
		gitOperationLabels,
	)

	gitOperationsDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "git_operations_duration_seconds",
			Help:    "A histogram of the duration of git clone operations.",
			Buckets: prometheus.DefBuckets,
		},
		gitOperationLabels,
	)

	scmCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "scm_calls_total",
			Help: "A counter of SCM API calls.",
		},
		scmCallLabels,
	)

	scmCallsDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "scm_calls_duration_seconds",
			Help:    "A histogram of the duration of SCM API calls.",
			Buckets: prometheus.DefBuckets,
		},
		scmCallLabels,
	)

	scmCallsRateLimitLimit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "scm_calls_rate_limit_limit",
			Help: "A gauge for the rate limit of SCM API calls.",
		},
		scmCallRateLimitLabels,
	)

	scmCallsRateLimitRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "scm_calls_rate_limit_remaining",
			Help: "A gauge for the remaining rate limit of SCM API calls.",
		},
		scmCallRateLimitLabels,
	)

	scmCallsRateLimitResetRemainingSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "scm_calls_rate_limit_reset_remaining_seconds",
			Help: "A gauge for the remaining seconds until the SCM API rate limit resets.",
		},
		scmCallRateLimitLabels,
	)

	webhookCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "webhook_calls_total",
			Help: "A counter of webhook calls.",
		},
		[]string{"ctp_found", "response_code"},
	)

	webhookProcessingDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "webhook_processing_duration_seconds",
			Help: "A histogram of the duration of webhook processing.",
		},
		[]string{"ctp_found", "response_code"},
	)

	// FinalizerDependentCount tracks the current number of dependent resources blocking deletion
	FinalizerDependentCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "promoter_finalizer_dependent_resources",
			Help: "Current number of dependent resources preventing deletion (gauge is cleared when finalizer is removed)",
		},
		[]string{"resource_type", "resource_name", "namespace"},
	)

	// ApplicationWatchEventsHandled tracks the number of times the ArgoCD application event handler is called
	ApplicationWatchEventsHandled = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "application_watch_events_handled_total",
			Help: "Number of times the ArgoCD application event handler is called.",
		},
	)

	// If you add metrics here, document them in docs/monitoring/metrics.md.
)

func init() {
	// Register custom metrics with the k8s controller-runtime metrics registry
	metrics.Registry.MustRegister(
		gitOperationsTotal,
		gitOperationsDurationSeconds,
		scmCallsTotal,
		scmCallsDurationSeconds,
		scmCallsRateLimitLimit,
		scmCallsRateLimitRemaining,
		scmCallsRateLimitResetRemainingSeconds,
		webhookProcessingDurationSeconds,
		FinalizerDependentCount,
		ApplicationWatchEventsHandled,
	)
}

// RecordGitOperation records both the increment and observation for git operations.
func RecordGitOperation(gitRepo *v1alpha1.GitRepository, operation GitOperation, result GitOperationResult, duration time.Duration) {
	labels := prometheus.Labels{
		"git_repository": gitRepo.Name,
		"scm_provider":   gitRepo.Spec.ScmProviderRef.Name,
		"operation":      string(operation),
		"result":         string(result),
	}
	gitOperationsTotal.With(labels).Inc()
	gitOperationsDurationSeconds.With(labels).Observe(duration.Seconds())
}

// RecordSCMCall records both the increment and observation for SCM API calls, and optionally observes rate limit metrics.
func RecordSCMCall(gitRepo *v1alpha1.GitRepository, api SCMAPI, operation SCMOperation, responseCode int, duration time.Duration, rateLimit *RateLimit) {
	labels := prometheus.Labels{
		"git_repository": gitRepo.Name,
		"scm_provider":   gitRepo.Spec.ScmProviderRef.Name,
		"api":            string(api),
		"operation":      string(operation),
		"response_code":  strconv.Itoa(responseCode),
	}
	scmCallsTotal.With(labels).Inc()
	scmCallsDurationSeconds.With(labels).Observe(duration.Seconds())

	if rateLimit != nil {
		rateLimitLabels := prometheus.Labels{
			"scm_provider": gitRepo.Spec.ScmProviderRef.Name,
		}

		scmCallsRateLimitLimit.With(rateLimitLabels).Set(float64(rateLimit.Limit))
		scmCallsRateLimitRemaining.With(rateLimitLabels).Set(float64(rateLimit.Remaining))
		scmCallsRateLimitResetRemainingSeconds.With(rateLimitLabels).Set(rateLimit.ResetRemaining.Seconds())
	}
}

// RecordWebhookCall records the duration of webhook processing.
func RecordWebhookCall(ctpFound bool, responseCode int, duration time.Duration) {
	labels := prometheus.Labels{
		"ctp_found":     strconv.FormatBool(ctpFound),
		"response_code": strconv.Itoa(responseCode),
	}
	webhookCallsTotal.With(labels).Inc()
	webhookProcessingDurationSeconds.With(labels).Observe(duration.Seconds())
}
