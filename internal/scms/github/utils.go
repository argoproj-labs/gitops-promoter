package github

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

// ApplyHTTPAuth returns an http.RoundTripper that authenticates requests using the GitHub App
// credentials from the ScmProvider and secret. It is used for arbitrary HTTP requests (e.g. WebRequestCommitStatus)
// that need to call the GitHub API with the same credentials as the rest of the promoter.
// gitRepo is used to resolve the installation ID from the repo owner when InstallationID is not set on the ScmProvider spec.
func ApplyHTTPAuth(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, gitRepo *v1alpha1.GitRepository) (http.RoundTripper, error) {
	org := ""
	if scmProvider.GetSpec().GitHub.InstallationID == 0 {
		org = gitRepo.Spec.GitHub.Owner
	}
	_, itr, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}
	if scmProvider.GetSpec().GitHub.Domain != "" {
		itr.BaseURL = fmt.Sprintf("https://%s/api/v3", scmProvider.GetSpec().GitHub.Domain)
	}
	return itr, nil
}

// getRateLimitMetrics converts the GitHub rate limit struct to one acceptable for the metrics package.
func getRateLimitMetrics(rate github.Rate) *metrics.RateLimit {
	return &metrics.RateLimit{
		Limit:          rate.Limit,
		Remaining:      rate.Remaining,
		ResetRemaining: time.Until(rate.Reset.Time),
	}
}
