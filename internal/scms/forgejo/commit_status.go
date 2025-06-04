package forgejo

import (
	"context"
	"fmt"
	"time"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	k8sV1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

type CommitStatus struct {
	foregejoClient *forgejo.Client
	k8sClient      k8sClient.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewForgejoCommitStatusProvider(k8sClient k8sClient.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret k8sV1.Secret) (*CommitStatus, error) {
	client, err := GetClient(scmProvider.GetSpec().Forgejo.Domain, secret)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		foregejoClient: client,
		k8sClient:      k8sClient,
	}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Phase")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, k8sClient.ObjectKey{
		Namespace: commitStatus.Namespace,
		Name:      commitStatus.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	start := time.Now()
	combinedStatus, resp, err := cs.foregejoClient.GetCombinedStatus(
		repo.Spec.Forgejo.Owner,
		repo.Spec.Forgejo.Name,
		commitStatus.Spec.Sha,
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get commit info: %w", err)
	}
	logger.V(4).Info("forgejo response status", "status", resp.Status)

	commitPhase, err := buildStateToPhase(combinedStatus.State)
	if err != nil {
		return nil, err
	}

	commitStatus.Status.Id = commitStatus.Spec.Sha
	commitStatus.Status.Phase = commitPhase
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}
