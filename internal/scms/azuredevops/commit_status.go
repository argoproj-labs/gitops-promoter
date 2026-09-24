package azuredevops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

const azureDevopsDomain = "dev.azure.com"

// azdoGitClient is the Azure DevOps git API surface used for commit and PR statuses.
type azdoGitClient interface {
	CreateCommitStatus(context.Context, git.CreateCommitStatusArgs) (*git.GitStatus, error)
	CreatePullRequestStatus(context.Context, git.CreatePullRequestStatusArgs) (*git.GitPullRequestStatus, error)
	GetPullRequests(context.Context, git.GetPullRequestsArgs) (*[]git.GitPullRequest, error)
	GetPullRequestIterations(context.Context, git.GetPullRequestIterationsArgs) (*[]git.GitPullRequestIteration, error)
}

// CommitStatus implements the scms.CommitStatusProvider interface for Azure DevOps.
type CommitStatus struct {
	client    *azuredevops.Connection
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewAzureDevopsCommitStatusProvider creates a new instance of CommitStatus for Azure DevOps.
func NewAzureDevopsCommitStatusProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CommitStatus, error) {
	azureClient, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		client:    azureClient,
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

	scmProvider, _, err := utils.GetScmProviderAndSecretFromRepositoryReference(
		ctx,
		cs.k8sClient,
		commitStatus.Namespace,
		commitStatus.Spec.RepositoryReference,
		commitStatus,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get SCM provider and secret: %w", err)
	}

	// Get Git client from Azure DevOps connection
	gitClient, err := git.NewClient(ctx, cs.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create Git client: %w", err)
	}

	state := mapPhaseToAzureDevOpsState(commitStatus.Spec.Phase)
	genre := "promoter"

	if commitStatus.Spec.Url == "" {
		commitStatus.Spec.Url = createCommitURL(gitRepo, scmProvider.GetSpec().AzureDevOps.Organization, commitStatus.Spec.Sha)
	}
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
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)
		return nil, fmt.Errorf("failed to create commit status: %w", err)
	}

	metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	logger.V(4).Info("Azure DevOps commit status created successfully",
		"statusId", *createdStatus.Id,
		"state", *createdStatus.State,
		"context", *createdStatus.Context.Name)

	// Update the commit status with the response
	commitStatus.Status.Id = strconv.Itoa(*createdStatus.Id)
	commitStatus.Status.Phase = mapAzureDevOpsStateToPhase(*createdStatus.State)
	commitStatus.Status.Sha = commitStatus.Spec.Sha

	// Azure DevOps does not show commit statuses on pull requests. Best-effort mirror
	// the status onto the iteration of any open PR whose head is this SHA. This must
	// never make the CommitStatus reconciliation fail: active SHAs commonly have no
	// PR, and the PR APIs may be unavailable or temporarily inconsistent.
	cs.setPullRequestStatusBestEffort(ctx, gitClient, gitRepo, commitStatus)

	return commitStatus, nil
}

func (cs CommitStatus) setPullRequestStatusBestEffort(
	ctx context.Context,
	gitClient azdoGitClient,
	gitRepo *v1alpha1.GitRepository,
	commitStatus *v1alpha1.CommitStatus,
) {
	logger := log.FromContext(ctx)

	prIDs, err := cs.openPullRequestIDsForSHA(ctx, gitClient, gitRepo, commitStatus.Spec.Sha)
	if err != nil {
		logger.V(4).Info("Skipping Azure DevOps PR status: failed to list pull requests", "error", err)
		return
	}

	for _, prID := range prIDs {
		iterationID, err := cs.iterationIDForSHA(ctx, gitClient, gitRepo, prID, commitStatus.Spec.Sha)
		if err != nil {
			logger.V(4).Info("Skipping Azure DevOps PR status: failed to find iteration", "pullRequestID", prID, "error", err)
			continue
		}
		if iterationID == 0 {
			logger.V(4).Info("Skipping Azure DevOps PR status: no iteration matches SHA", "pullRequestID", prID, "sha", commitStatus.Spec.Sha)
			continue
		}

		if err := cs.createPullRequestStatus(ctx, gitClient, gitRepo, commitStatus, prID, iterationID); err != nil {
			logger.V(4).Info("Skipping Azure DevOps PR status: create failed", "pullRequestID", prID, "iterationID", iterationID, "error", err)
		}
	}
}

// openPullRequestIDsForSHA returns the IDs of active pull requests whose source branch head is sha.
func (cs CommitStatus) openPullRequestIDsForSHA(
	ctx context.Context,
	gitClient azdoGitClient,
	gitRepo *v1alpha1.GitRepository,
	sha string,
) ([]int, error) {
	searchCriteria := git.GitPullRequestSearchCriteria{
		Status: &git.PullRequestStatusValues.Active,
	}

	start := time.Now()
	pullRequests, err := gitClient.GetPullRequests(ctx, git.GetPullRequestsArgs{
		RepositoryId:   &gitRepo.Spec.AzureDevOps.Name,
		Project:        &gitRepo.Spec.AzureDevOps.Project,
		SearchCriteria: &searchCriteria,
	})
	statusCode := 200
	if err != nil {
		if sc, ok := azureDevOpsHTTPStatusCode(err); ok {
			statusCode = sc
		} else {
			statusCode = 500
		}
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}
	metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)

	if pullRequests == nil {
		return nil, nil
	}

	var prIDs []int
	for i := range *pullRequests {
		pr := (*pullRequests)[i]
		if pr.PullRequestId == nil || pr.LastMergeSourceCommit == nil || pr.LastMergeSourceCommit.CommitId == nil {
			continue
		}
		if strings.EqualFold(*pr.LastMergeSourceCommit.CommitId, sha) {
			prIDs = append(prIDs, *pr.PullRequestId)
		}
	}
	return prIDs, nil
}

func (cs CommitStatus) iterationIDForSHA(
	ctx context.Context,
	gitClient azdoGitClient,
	gitRepo *v1alpha1.GitRepository,
	prID int,
	sha string,
) (int, error) {
	start := time.Now()
	iterations, err := gitClient.GetPullRequestIterations(ctx, git.GetPullRequestIterationsArgs{
		RepositoryId:  &gitRepo.Spec.AzureDevOps.Name,
		PullRequestId: &prID,
		Project:       &gitRepo.Spec.AzureDevOps.Project,
	})
	statusCode := 200
	if err != nil {
		if sc, ok := azureDevOpsHTTPStatusCode(err); ok {
			statusCode = sc
		} else {
			statusCode = 500
		}
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)
		return 0, fmt.Errorf("failed to list pull request iterations: %w", err)
	}
	metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)

	if iterations == nil {
		return 0, nil
	}
	return iterationIDMatchingSHA(*iterations, sha), nil
}

func (cs CommitStatus) createPullRequestStatus(
	ctx context.Context,
	gitClient azdoGitClient,
	gitRepo *v1alpha1.GitRepository,
	commitStatus *v1alpha1.CommitStatus,
	prID int,
	iterationID int,
) error {
	logger := log.FromContext(ctx)
	state := mapPhaseToAzureDevOpsState(commitStatus.Spec.Phase)
	genre := "promoter"
	prStatus := git.GitPullRequestStatus{
		Context: &git.GitStatusContext{
			Name:  &commitStatus.Spec.Name,
			Genre: &genre,
		},
		State:       &state,
		Description: &commitStatus.Spec.Description,
		TargetUrl:   &commitStatus.Spec.Url,
		IterationId: &iterationID,
	}

	start := time.Now()
	created, err := gitClient.CreatePullRequestStatus(ctx, git.CreatePullRequestStatusArgs{
		Status:        &prStatus,
		RepositoryId:  &gitRepo.Spec.AzureDevOps.Name,
		PullRequestId: &prID,
		Project:       &gitRepo.Spec.AzureDevOps.Project,
	})
	statusCode := 201
	if err != nil {
		if sc, ok := azureDevOpsHTTPStatusCode(err); ok {
			statusCode = sc
		} else {
			statusCode = 500
		}
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)
		return fmt.Errorf("failed to create pull request status: %w", err)
	}
	metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	if created == nil || created.Id == nil {
		return fmt.Errorf("azure DevOps pull request status response missing id")
	}

	logger.V(4).Info("Azure DevOps pull request status created",
		"pullRequestID", prID,
		"iterationID", iterationID,
		"statusId", *created.Id)
	return nil
}

func iterationIDMatchingSHA(iterations []git.GitPullRequestIteration, sha string) int {
	for i := range iterations {
		iter := iterations[i]
		if iter.Id == nil || iter.SourceRefCommit == nil || iter.SourceRefCommit.CommitId == nil {
			continue
		}
		if strings.EqualFold(*iter.SourceRefCommit.CommitId, sha) {
			return *iter.Id
		}
	}
	return 0
}

func mapPhaseToAzureDevOpsState(phase v1alpha1.CommitStatusPhase) git.GitStatusState {
	switch phase { //revive:disable
	case v1alpha1.CommitPhasePending:
		return git.GitStatusStateValues.Pending
	case v1alpha1.CommitPhaseSuccess:
		return git.GitStatusStateValues.Succeeded
	case v1alpha1.CommitPhaseFailure:
		return git.GitStatusStateValues.Failed
	default:
		return git.GitStatusStateValues.Pending
	}
}

// mapAzureDevOpsStateToPhase maps Azure DevOps GitStatusState to GitOps Promoter CommitStatusPhase
func mapAzureDevOpsStateToPhase(state git.GitStatusState) v1alpha1.CommitStatusPhase {
	switch state { //revive:disable
	case git.GitStatusStateValues.Pending:
		return v1alpha1.CommitPhasePending
	case git.GitStatusStateValues.Succeeded:
		return v1alpha1.CommitPhaseSuccess
	case git.GitStatusStateValues.Failed, git.GitStatusStateValues.Error:
		return v1alpha1.CommitPhaseFailure
	default:
		return v1alpha1.CommitPhasePending
	}
}

func createCommitURL(repo *v1alpha1.GitRepository, org string, sha string) string {
	return fmt.Sprintf("https://%s/%s/%s/_git/%s/commit/%s",
		azureDevopsDomain,
		org,
		repo.Spec.AzureDevOps.Project,
		repo.Spec.AzureDevOps.Name,
		sha)
}
