package github

import (
	"context"
	"fmt"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/google/go-github/v61/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CommitStatus struct {
	client    *github.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewGithubCommitStatusProvider(k8sClient client.Client, secret v1.Secret) (*CommitStatus, error) {
	client, err := GetClient(secret)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Phase")

	commitStatusS := &github.RepoStatus{
		State:       github.String(string(commitStatus.Spec.Phase)),
		TargetURL:   github.String(commitStatus.Spec.Url),
		Description: github.String(commitStatus.Spec.Description),
		Context:     github.String(commitStatus.Spec.Name),
	}

	gitRepo, err := utils.GetGitRepositorytFromRepositoryReference(ctx, cs.k8sClient, commitStatus.Spec.RepositoryReference)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	repoStatus, response, err := cs.client.Repositories.CreateStatus(ctx, gitRepo.Spec.Owner, commitStatus.Spec.RepositoryReference.Name, commitStatus.Spec.Sha, commitStatusS)
	if err != nil {
		return nil, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	commitStatus.Status.Id = strconv.FormatInt(*repoStatus.ID, 10)
	commitStatus.Status.Phase = promoterv1alpha1.CommitStatusPhase(*repoStatus.State)
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}
