package bitbucket_cloud

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ktrysmt/go-bitbucket"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CommitStatus implements the scms.CommitStatusProvider interface for Bitbucket Cloud.
type CommitStatus struct {
	client    *bitbucket.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewBitbucketCloudCommitStatusProvider creates a new instance of CommitStatus for Bitbucket Cloud.
func NewBitbucketCloudCommitStatusProvider(k8sClient client.Client, secret v1.Secret, domain string) (*CommitStatus, error) {
	client, err := GetClient(secret)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{client: client, k8sClient: k8sClient}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository.
func (cs *CommitStatus) Set(ctx context.Context, commitStatus *v1alpha1.CommitStatus) (*v1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Phase")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{
		Namespace: commitStatus.Namespace,
		Name:      commitStatus.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	commitOptions := &bitbucket.CommitsOptions{
		Owner:    repo.Spec.Bitbucket.Workspace,
		RepoSlug: repo.Spec.Bitbucket.Repository,
		Revision: commitStatus.Spec.Sha,
	}

	commitStatusOptions := &bitbucket.CommitStatusOptions{
		State:       phaseToBuildState(commitStatus.Spec.Phase),
		Key:         commitStatus.Spec.Name,
		Url:         commitStatus.Spec.Url,
		Description: commitStatus.Spec.Description,
	}

	start := time.Now()
	result, err := cs.client.Repositories.Commits.CreateCommitStatus(
		commitOptions,
		commitStatusOptions,
	)
	statusCode := parseErrorStatusCode(err, http.StatusCreated)
	metrics.RecordSCMCall(repo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	if err != nil {
		return nil, fmt.Errorf("failed to create status: %w", err)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)

	// Parse the response
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from Bitbucket API: %T", result)
	}

	// Extract state
	state, _ := resultMap["state"].(string)

	commitStatus.Status.Phase = buildStateToPhase(state)
	commitStatus.Status.Sha = commitStatus.Spec.Sha

	// Bitbucket doesn't return an ID for commit statuses, use key as identifier
	commitStatus.Status.Id = commitStatus.Spec.Name

	return commitStatus, nil
}
