package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type GitOperation string

const (
	GitOperationClone    GitOperation = "clone"
	GitOperationFetch    GitOperation = "fetch"
	GitOperationPull     GitOperation = "pull"
	GitOperationPush     GitOperation = "push"
	GitOperationLsRemote GitOperation = "ls-remote"
)

type GitOperationResult string

const (
	GitOperationResultSuccess GitOperationResult = "success"
	GitOperationResultFailure GitOperationResult = "failure"
)

func GitOperationResultFromError(err error) GitOperationResult {
	if err == nil {
		return GitOperationResultSuccess
	}
	return GitOperationResultFailure
}

type SCMAPI string

const (
	SCMAPICommitStatus SCMAPI = "CommitStatus"
	SCMAPIPullRequest  SCMAPI = "PullRequest"
)

type SCMOperation string

const (
	SCMOperationCreate SCMOperation = "create"
	SCMOperationUpdate SCMOperation = "update"
	SCMOperationMerge  SCMOperation = "merge"
	SCMOperationClose  SCMOperation = "close"
	SCMOperationList   SCMOperation = "list"
)

type RateLimit struct {
	Limit          int
	Remaining      int
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
