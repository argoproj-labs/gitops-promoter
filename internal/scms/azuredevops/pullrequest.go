package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for Azure DevOps.
type PullRequest struct {
	client    *azuredevops.Connection
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewAzdoPullRequestProvider creates a new instance of PullRequest for Azure DevOps.
func NewAzdoPullRequestProvider(k8sClient client.Client, secret v1.Secret, scmProvider v1alpha1.GenericScmProvider, org string) (*PullRequest, error) {
	prClient, _, err := GetClient(context.Background(), scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		client:    prClient,
		k8sClient: k8sClient,
	}, nil
}

// Create creates a new pull request with the specified title, head, base, and description.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)
	logger.Info("Creating Pull Request in Azure DevOps")

	if title == "" {
		return "", errors.New("title is required for pull request creation")
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get Git client
	gitClient, err := git.NewClient(ctx, pr.client)
	if err != nil {
		return "", fmt.Errorf("failed to create Git client: %w", err)
	}

	sourceRef, targetRef, err := getFormattedRefs(head, base)
	if err != nil {
		return "", fmt.Errorf("failed to validate branch reference: %w", err)
	}

	// Create Git pull request
	gitPullRequest := git.GitPullRequest{
		Title:         &title,
		Description:   &description,
		SourceRefName: &sourceRef,
		TargetRefName: &targetRef,
	}

	start := time.Now()
	createdPR, err := gitClient.CreatePullRequest(ctx, git.CreatePullRequestArgs{
		GitPullRequestToCreate: &gitPullRequest,
		RepositoryId:           &gitRepo.Spec.AzureDevOps.Name,
		Project:                &gitRepo.Spec.AzureDevOps.Project,
	})

	// Record metrics and handle response
	statusCode := 201 // Created status as per Azure DevOps API
	if err != nil {
		statusCode = 500
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	logger.Info("Azure DevOps pull request created successfully", "prId", *createdPR.PullRequestId)

	return strconv.Itoa(*createdPR.PullRequestId), nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)
	logger.Info("Updating Pull Request in Azure DevOps")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prId, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID to int: %w", err)
	}

	// Get Git client
	gitClient, err := git.NewClient(ctx, pr.client)
	if err != nil {
		return fmt.Errorf("failed to create Git client: %w", err)
	}

	// Update Git pull request
	gitPullRequest := git.GitPullRequest{
		Title:       &title,
		Description: &description,
	}

	start := time.Now()
	_, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		GitPullRequestToUpdate: &gitPullRequest,
		RepositoryId:           &gitRepo.Spec.AzureDevOps.Name,
		PullRequestId:          &prId,
		Project:                &gitRepo.Spec.AzureDevOps.Project,
	})

	// Record metrics and handle response
	statusCode := 200
	if err != nil {
		statusCode = 500
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, statusCode, time.Since(start), nil)
		return fmt.Errorf("failed to update pull request: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, statusCode, time.Since(start), nil)

	logger.V(4).Info("Azure DevOps pull request updated successfully", "prId", prId)

	return nil
}

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)
	logger.Info("Closing Pull Request in Azure DevOps")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prId, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID to int: %w", err)
	}

	// Get Git client
	gitClient, err := git.NewClient(ctx, pr.client)
	if err != nil {
		return fmt.Errorf("failed to create Git client: %w", err)
	}

	// Close pull request by setting status to abandoned
	status := git.PullRequestStatusValues.Abandoned
	gitPullRequest := git.GitPullRequest{
		Status: &status,
	}

	start := time.Now()
	_, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		GitPullRequestToUpdate: &gitPullRequest,
		RepositoryId:           &gitRepo.Spec.AzureDevOps.Name,
		PullRequestId:          &prId,
		Project:                &gitRepo.Spec.AzureDevOps.Project,
	})

	// Record metrics and handle response
	statusCode := 200
	if err != nil {
		statusCode = 500
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, statusCode, time.Since(start), nil)
		return fmt.Errorf("failed to close pull request: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, statusCode, time.Since(start), nil)

	logger.V(4).Info("Azure DevOps pull request closed successfully", "prId", prId)

	return nil
}

// Merge merges an existing pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)
	logger.Info("Merging Pull Request in Azure DevOps")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prId, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID to int: %w", err)
	}

	// Get Git client
	gitClient, err := git.NewClient(ctx, pr.client)
	if err != nil {
		return fmt.Errorf("failed to create Git client: %w", err)
	}

	// Complete the pull request (merge it) using Azure DevOps completion API
	completionOptions := git.GitPullRequestCompletionOptions{
		MergeCommitMessage: &pullRequest.Spec.Commit.Message,
		DeleteSourceBranch: &[]bool{false}[0], // Keep source branch by default
	}

	// Set merge status to completed
	status := git.PullRequestStatusValues.Completed
	gitPullRequest := git.GitPullRequest{
		Status:                &status,
		CompletionOptions:     &completionOptions,
		LastMergeSourceCommit: &git.GitCommitRef{CommitId: &pullRequest.Spec.MergeSha},
	}

	start := time.Now()
	_, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		GitPullRequestToUpdate: &gitPullRequest,
		RepositoryId:           &gitRepo.Spec.AzureDevOps.Name,
		PullRequestId:          &prId,
		Project:                &gitRepo.Spec.AzureDevOps.Project,
	})

	// Record metrics and handle response
	statusCode := 200
	if err != nil {
		statusCode = 500
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, statusCode, time.Since(start), nil)
		return fmt.Errorf("failed to merge pull request: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, statusCode, time.Since(start), nil)

	logger.V(4).Info("Azure DevOps pull request merged successfully", "prId", prId)

	return nil
}

// FindOpen checks if a pull request is open and returns its status.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string, time.Time, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request in Azure DevOps")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get Git client
	gitClient, err := git.NewClient(ctx, pr.client)
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to create Git client: %w", err)
	}

	// Ensure branch names are in correct format for Azure DevOps
	sourceRef := ensureRefsFormat(pullRequest.Spec.SourceBranch)
	targetRef := ensureRefsFormat(pullRequest.Spec.TargetBranch)

	// Search for open pull requests with matching source and target branches
	searchCriteria := git.GitPullRequestSearchCriteria{
		Status:        &git.PullRequestStatusValues.Active,
		SourceRefName: &sourceRef,
		TargetRefName: &targetRef,
	}

	start := time.Now()
	pullRequests, err := gitClient.GetPullRequests(ctx, git.GetPullRequestsArgs{
		RepositoryId:   &gitRepo.Spec.AzureDevOps.Name,
		Project:        &gitRepo.Spec.AzureDevOps.Project,
		SearchCriteria: &searchCriteria,
	})

	// Record metrics and handle response
	statusCode := 200
	if err != nil {
		statusCode = 500
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)
		return false, "", time.Time{}, fmt.Errorf("failed to search pull requests: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)

	if len(*pullRequests) > 0 {
		firstPR := (*pullRequests)[0]

		// Generate URL directly since pullRequest.Status.ID is not set yet
		url, err := pr.generatePullRequestUrl(ctx, pullRequest, *firstPR.PullRequestId)
		if err != nil {
			return false, "", time.Time{}, fmt.Errorf("failed to generate pull request URL: %w", err)
		}

		prStatus := v1alpha1.PullRequestCommonStatus{
			ID:             strconv.Itoa(*firstPR.PullRequestId),
			State:          mapAzureDevOpsPRStatusToState(*firstPR.Status),
			PRCreationTime: metav1.Time{Time: firstPR.CreationDate.Time},
			Url:            url,
		}
		return true, prStatus.ID, prStatus.PRCreationTime.Time, nil
	}

	return false, "", time.Time{}, nil
}

// GetUrl returns the URL of the pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, pullRequest v1alpha1.PullRequest) (string, error) {
	prId, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return "", fmt.Errorf("failed to convert PR ID to int when generating pull request url: %w", err)
	}

	return pr.generatePullRequestUrl(ctx, pullRequest, prId)
}

// generatePullRequestUrl generates a pull request URL for the given repository and PR ID
func (pr *PullRequest) generatePullRequestUrl(ctx context.Context, prObj v1alpha1.PullRequest, prId int) (string, error) {
	// Get the SCM provider to determine the domain
	// Convert ScmProviderObjectReference to ObjectReference
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: prObj.Namespace, Name: prObj.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}
	scmProvider, _, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, pr.k8sClient, "", prObj.Spec.RepositoryReference, &prObj)
	if err != nil {
		return "", fmt.Errorf("failed to get SCM provider: %w", err)
	}

	var baseUrl string
	if scmProvider.GetSpec().AzureDevOps.Domain != "" {
		baseUrl = fmt.Sprintf("https://%s/%s/%s/_git/%s/pullrequest/%d",
			scmProvider.GetSpec().AzureDevOps.Domain,
			scmProvider.GetSpec().AzureDevOps.Organization,
			gitRepo.Spec.AzureDevOps.Project,
			gitRepo.Spec.AzureDevOps.Name,
			prId)
	} else {
		baseUrl = fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
			scmProvider.GetSpec().AzureDevOps.Organization,
			gitRepo.Spec.AzureDevOps.Project,
			gitRepo.Spec.AzureDevOps.Name,
			prId)
	}

	return baseUrl, nil
}

// mapAzureDevOpsPRStatusToState maps Azure DevOps PullRequestStatus to GitOps Promoter PullRequestState
func mapAzureDevOpsPRStatusToState(status git.PullRequestStatus) v1alpha1.PullRequestState {
	switch status { //nolint:revive
	case git.PullRequestStatusValues.Active:
		return v1alpha1.PullRequestOpen
	case git.PullRequestStatusValues.Completed:
		return v1alpha1.PullRequestMerged
	case git.PullRequestStatusValues.Abandoned:
		return v1alpha1.PullRequestClosed
	default:
		return v1alpha1.PullRequestOpen
	}
}

// ensureRefsFormat ensures branch names are in the correct Azure DevOps refs format
func ensureRefsFormat(branchName string) string {
	if branchName == "" {
		return branchName
	}
	// If it already starts with refs/, use as-is
	if len(branchName) >= 5 && branchName[:5] == "refs/" {
		return branchName
	}
	// Otherwise, add refs/heads/ prefix
	return "refs/heads/" + branchName
}

// Get and validate required branch for pull request creation
func getFormattedRefs(head, base string) (string, string, error) {
	sourceRef := ensureRefsFormat(head)
	targetRef := ensureRefsFormat(base)

	if sourceRef == "" || targetRef == "" {
		return "", "", errors.New("source and target branches are required for pull request creation")
	}

	return sourceRef, targetRef, nil
}
