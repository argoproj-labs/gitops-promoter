package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CheckParser implements the scms.SCMCheckParser interface for Azure DevOps.
type CheckParser struct {
	client    *azuredevops.Connection
	k8sClient client.Client
}

// NewAzdoCheckParser creates a new CheckParser for Azure DevOps
func NewAzdoCheckParser(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CheckParser, error) {
	client, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a Azure DevOps PR based on the most recent commit in the PR
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, branch string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	branchID, err := utils.GetPullRequestIDForBranch(ps, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request id: %w", err)
	}
	prNumber, err := strconv.Atoi(branchID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert PR ID to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{Namespace: ps.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	gitClient, err := git.NewClient(ctx, c.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create Git client: %w", err)
	}

	start := time.Now()
	azdoPR, err := gitClient.GetPullRequestById(ctx, git.GetPullRequestByIdArgs{
		PullRequestId: &prNumber,
		Project:       &gitRepo.Spec.AzureDevOps.Project,
	})
	statusCode := http.StatusOK
	if err != nil {
		if isAzureDevOpsNotFound(err) {
			metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, http.StatusNotFound, time.Since(start), nil)
			return nil, errors.New("pull request was not found")
		}
		statusCode = http.StatusInternalServerError
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, statusCode, time.Since(start), nil)
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, statusCode, time.Since(start), nil)

	if azdoPR == nil {
		return nil, errors.New("retrieved PR is empty")
	}

	if azdoPR.Commits == nil || len(*azdoPR.Commits) < 1 {
		return nil, errors.New("pull request has no commits")
	}

	// Azure DevOps returns commits in reverse chronological order
	// https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get-commits
	commits := azdoPR.Commits
	latestCommit := (*commits)[0]

	var checks []scms.SCMCheckResult
	for _, status := range *latestCommit.Statuses {
		name := "Azure DevOps Check"
		if status.Context.Name != nil {
			name = *status.Context.Name
		}

		url := ""
		if status.TargetUrl != nil {
			url = *status.TargetUrl
		}

		checks = append(checks, scms.SCMCheckResult{
			Name:   name,
			Status: determineCheckStatusFromAzDoStatus(*status.State),
			URL:    url,
		})
	}
	return checks, nil
}

// determineCheckStatusFromAzDoStatus is a helper function that determines the checks status based on the status.
func determineCheckStatusFromAzDoStatus(status git.GitStatusState) scms.SCMCheckStatus {
	switch status {
	case git.GitStatusStateValues.Failed, git.GitStatusStateValues.Error, git.GitStatusStateValues.NotApplicable:
		return scms.SCMCheckStatusFailed
	case git.GitStatusStateValues.Succeeded:
		return scms.SCMCheckStatusSucceeded
	case git.GitStatusStateValues.Pending, git.GitStatusStateValues.NotSet:
		return scms.SCMCheckStatusPending
	default:
		return scms.SCMCheckStatusInProgress
	}
}
