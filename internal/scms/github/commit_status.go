package github

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

type CommitStatus struct {
	client    *github.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewGithubCommitStatusProvider(k8sClient client.Client, scmProvider *promoterv1alpha1.ScmProvider, secret v1.Secret) (*CommitStatus, error) {
	client, err := GetClient(scmProvider, secret)
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
		State:       github.Ptr(string(commitStatus.Spec.Phase)),
		TargetURL:   github.Ptr(commitStatus.Spec.Url),
		Description: github.Ptr(commitStatus.Spec.Description),
		Context:     github.Ptr(commitStatus.Spec.Name),
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{Namespace: commitStatus.Namespace, Name: commitStatus.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	repoStatus, response, err := cs.client.Repositories.CreateStatus(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, commitStatus.Spec.Sha, commitStatusS)
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create status: %w", err)
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
