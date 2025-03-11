package gitlab

import (
	"context"
	"fmt"
	"strconv"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CommitStatus struct {
	client    *gitlab.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewGitlabCommitStatusProvider(k8sClient client.Client, secret v1.Secret, domain string) (*CommitStatus, error) {
	client, err := GetClient(secret, domain)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{client: client, k8sClient: k8sClient}, nil
}

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

	commitStatusOptions := &gitlab.SetCommitStatusOptions{
		State:       phaseToBuildState(commitStatus.Spec.Phase),
		TargetURL:   gitlab.Ptr(commitStatus.Spec.Url),
		Name:        gitlab.Ptr(commitStatus.Spec.Name),
		Description: gitlab.Ptr(commitStatus.Spec.Description),
	}

	glStatus, resp, err := cs.client.Commits.SetCommitStatus(
		repo.Spec.GitLab.ProjectID,
		commitStatus.Spec.Sha,
		commitStatusOptions,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create status: %w", err)
	}

	logGitLabRatelimits(
		logger,
		repo.Spec.ScmProviderRef.Name,
		resp,
	)
	logger.V(4).Info("github response status",
		"status", resp.Status)

	commitStatus.Status.Id = strconv.Itoa(glStatus.ID)
	commitStatus.Status.Phase = buildStateToPhase(gitlab.BuildStateValue(glStatus.Status))
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}
