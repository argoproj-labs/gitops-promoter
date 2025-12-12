package gitea

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"code.gitea.io/sdk/gitea"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	k8sV1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CommitStatus implements the scms.CommitStatusProvider interface for Gitea.
type CommitStatus struct {
	giteaClient *gitea.Client
	k8sClient   k8sClient.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewGiteaCommitStatusProvider creates a new instance of CommitStatus for Gitea.
func NewGiteaCommitStatusProvider(k8sClient k8sClient.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret k8sV1.Secret) (*CommitStatus, error) {
	client, err := GetClient(scmProvider.GetSpec().Gitea.Domain, secret)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		giteaClient: client,
		k8sClient:   k8sClient,
	}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository.
func (cs CommitStatus) Set(ctx context.Context, csObj *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Phase")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, k8sClient.ObjectKey{
		Namespace: csObj.Namespace,
		Name:      csObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	status, err := commitPhaseToGiteaStatusState(csObj.Spec.Phase)
	if err != nil {
		return nil, err
	}
	options := gitea.CreateStatusOption{
		State:       status,
		TargetURL:   csObj.Spec.Url,
		Description: csObj.Spec.Description,
		Context:     csObj.Spec.Name,
	}

	start := time.Now()
	commitStatus, resp, err := cs.giteaClient.CreateStatus(
		repo.Spec.Gitea.Owner,
		repo.Spec.Gitea.Name,
		csObj.Spec.Sha,
		options,
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to set commit status: %w", err)
	}
	logger.V(4).Info("gitea response status", "status", resp.Status)

	csObj.Status.Id = strconv.FormatInt(commitStatus.ID, 16)
	csObj.Status.Phase = csObj.Spec.Phase
	csObj.Status.Sha = csObj.Spec.Sha

	return csObj, nil
}
