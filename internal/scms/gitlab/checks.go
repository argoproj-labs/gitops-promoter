package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CheckParser implements the scms.SCMCheckParser interface for GitLab.
type CheckParser struct {
	client    *gitlab.Client
	k8sClient client.Client
}

// The below constants represent the values status of merge checks can be through Gitlabs API
const (
	MergeStatusCheckPassed  string = "passed"
	MergeStatusCheckFailed  string = "failed"
	MergeStatusCheckPending string = "pending"
)

// NewGitLabCheckParser creates a new CheckParser for Gitlab
func NewGitLabCheckParser(ctx context.Context, k8sClient client.Client, secret v1.Secret, domain string) (*CheckParser, error) {
	client, err := GetClient(secret, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gitlab client: %w", err)
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a GitLab MR based on the most recent commit in the MR
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, branch string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	branchID, err := utils.GetPullRequestIDForBranch(ps, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get merge request id: %w", err)
	}
	mrNumber, err := strconv.ParseInt(branchID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert MR ID to int64: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	start := time.Now()
	mr, response, err := c.client.MergeRequests.GetMergeRequest(repo.Spec.GitLab.ProjectID, mrNumber, nil, gitlab.WithContext(ctx))
	if response != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, response.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, errors.New("gitlab merge request was not found")
		}
		return nil, fmt.Errorf("failed to get merge request: %w", err)
	}
	if mr == nil {
		return nil, errors.New("retrieved merge request is empty")
	}

	start = time.Now()
	commitCheckList, response, err := c.client.ExternalStatusChecks.ListProjectMergeRequestExternalStatusChecks(repo.Spec.GitLab.ProjectID, mrNumber, nil)
	if response != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMChecks, metrics.SCMOperationList, response.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list SCM checks: %w", err)
	}

	var checks []scms.SCMCheckResult
	for _, check := range commitCheckList {
		if check == nil {
			return nil, errors.New("received null check from list")
		}

		checks = append(checks, scms.SCMCheckResult{
			Name:   check.Name,
			Status: determineCheckStatusFromGLMergeCheck(*check),
			URL:    check.ExternalURL,
		})
	}

	return checks, nil
}

// determineCheckStatusFromGLMergeCheck is a helper function that determines the check status based on a merge
// check's status
func determineCheckStatusFromGLMergeCheck(check gitlab.MergeStatusCheck) scms.SCMCheckStatus {
	switch check.Status {
	case MergeStatusCheckPassed:
		return scms.SCMCheckStatusSucceeded
	case MergeStatusCheckFailed:
		return scms.SCMCheckStatusFailed
	default:
		return scms.SCMCheckStatusPending
	}
}
