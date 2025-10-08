package azuredevops

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
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

// NewAzdoCommitStatusProvider creates a new instance of CommitStatus for Azure DevOps.
func NewAzdoCommitStatusProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CommitStatus, error) {
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
func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
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
		return nil, fmt.Errorf("status name is required for commit status creation")
	}
	if commitStatus.Spec.Sha == "" {
		return nil, fmt.Errorf("commit SHA is required for commit status creation")
	}

	// Map GitOps Promoter status phase to Azure DevOps status state
	var state git.GitStatusState
	switch commitStatus.Spec.Phase {
	case promoterv1alpha1.CommitPhasePending:
		state = git.GitStatusStateValues.Pending
	case promoterv1alpha1.CommitPhaseSuccess:
		state = git.GitStatusStateValues.Succeeded
	case promoterv1alpha1.CommitPhaseFailure:
		state = git.GitStatusStateValues.Failed
	default:
		state = git.GitStatusStateValues.Pending
	}

	// Create Git commit status
	genre := "promoter"
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
func mapAzureDevOpsStateToPhase(state git.GitStatusState) promoterv1alpha1.CommitStatusPhase {
	switch state {
	case git.GitStatusStateValues.Pending:
		return promoterv1alpha1.CommitPhasePending
	case git.GitStatusStateValues.Succeeded:
		return promoterv1alpha1.CommitPhaseSuccess
	case git.GitStatusStateValues.Failed:
		return promoterv1alpha1.CommitPhaseFailure
	case git.GitStatusStateValues.Error:
		return promoterv1alpha1.CommitPhaseFailure // Map error to failure as we only have 3 states
	default:
		return promoterv1alpha1.CommitPhasePending
	}
}
