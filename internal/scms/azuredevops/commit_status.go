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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CommitStatus implements the scms.CommitStatusProvider interface for Azure DevOps.
type CommitStatus struct {
	client    *azuredevops.Connection
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewAzureDevopsCommitStatusProvider creates a new instance of CommitStatus for Azure DevOps.
func NewAzureDevopsCommitStatusProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CommitStatus, error) {
	client, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository.
func (cs CommitStatus) Set(ctx context.Context, commitStatus *v1alpha1.CommitStatus) (*v1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Status for Azure DevOps")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{Namespace: commitStatus.Namespace, Name: commitStatus.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get Git client from Azure DevOps connection
	gitClient, err := git.NewClient(ctx, cs.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create Git client: %w", err)
	}

	// Validate required fields for Azure DevOps API
	if commitStatus.Spec.Name == "" {
		return nil, errors.New("status name is required for commit status creation")
	}
	if commitStatus.Spec.Sha == "" {
		return nil, errors.New("commit SHA is required for commit status creation")
	}

	// Map GitOps Promoter status phase to Azure DevOps status state
	var state git.GitStatusState
	switch commitStatus.Spec.Phase {
	case v1alpha1.CommitPhasePending:
		state = git.GitStatusStateValues.Pending
	case v1alpha1.CommitPhaseSuccess:
		state = git.GitStatusStateValues.Succeeded
	case v1alpha1.CommitPhaseFailure:
		state = git.GitStatusStateValues.Failed
	default:
		state = git.GitStatusStateValues.Pending
	}

	// Create Git commit status
	genre := "promoter"
	fmt.Println("URL", commitStatus.Spec.Url)

	if commitStatus.Spec.Url == "" {
		commitStatus.Spec.Url = createCommitURL(gitRepo, commitStatus.Spec.Sha)
	}
	fmt.Println("URL2", commitStatus.Spec.Url)
	gitCommitStatus := git.GitStatus{
		Context: &git.GitStatusContext{
			Name:  &commitStatus.Spec.Name,
			Genre: &genre,
		},
		State:       &state,
		Description: &commitStatus.Spec.Description,
		TargetUrl:   &commitStatus.Spec.Url,
	}

	start := time.Now()
	// Create the status using Azure DevOps REST API
	// Repository identifier should be the repository name for Azure DevOps
	createdStatus, err := gitClient.CreateCommitStatus(ctx, git.CreateCommitStatusArgs{
		CommitId:                &commitStatus.Spec.Sha,
		RepositoryId:            &gitRepo.Spec.AzureDevOps.Name,
		Project:                 &gitRepo.Spec.AzureDevOps.Project,
		GitCommitStatusToCreate: &gitCommitStatus,
	})

	// Record metrics and handle response
	statusCode := 201 // Created status as per Azure DevOps API
	if err != nil {
		statusCode = 500 // Server error
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)
		return nil, fmt.Errorf("failed to create commit status: %w", err)
	}

	metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	logger.V(4).Info("Azure DevOps commit status created successfully",
		"statusId", *createdStatus.Id,
		"state", *createdStatus.State,
		"context", *createdStatus.Context.Name)

	// Update the commit status with the response
	commitStatus.Status.Id = strconv.Itoa(*createdStatus.Id)
	commitStatus.Status.Phase = mapAzureDevOpsStateToPhase(*createdStatus.State)
	commitStatus.Status.Sha = commitStatus.Spec.Sha

	return commitStatus, nil
}

// mapAzureDevOpsStateToPhase maps Azure DevOps GitStatusState to GitOps Promoter CommitStatusPhase
func mapAzureDevOpsStateToPhase(state git.GitStatusState) v1alpha1.CommitStatusPhase {
	switch state {
	case git.GitStatusStateValues.Pending:
		return v1alpha1.CommitPhasePending
	case git.GitStatusStateValues.Succeeded:
		return v1alpha1.CommitPhaseSuccess
	case git.GitStatusStateValues.Failed:
		return v1alpha1.CommitPhaseFailure
	case git.GitStatusStateValues.Error:
		return v1alpha1.CommitPhaseFailure // Map error to failure as we only have 3 states
	default:
		return v1alpha1.CommitPhasePending
	}
}

func createCommitURL(repo *v1alpha1.GitRepository, sha string) string {
	return fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/commit/%s",
		repo.Spec.AzureDevOps.Project,
		repo.Spec.AzureDevOps.Project,
		repo.Spec.AzureDevOps.Name,
		sha)
}
