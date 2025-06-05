package forgejo

import (
	"context"
	"fmt"
	"strconv"
	"time"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	k8sV1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

type PullRequest struct {
	foregejoClient *forgejo.Client
	k8sClient      k8sClient.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

func NewForgejoPullRequestProvider(k8sClient k8sClient.Client, secret k8sV1.Secret, domain string) (*PullRequest, error) {
	client, err := GetClient(domain, secret)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		foregejoClient: client,
		k8sClient:      k8sClient,
	}, nil
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, prObj *promoterv1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := forgejo.CreatePullRequestOption{
		Head:  head,
		Base:  base,
		Title: title,
		Body:  description,
	}

	start := time.Now()
	pullRequest, resp, err := pr.foregejoClient.CreatePullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	logger.V(4).Info("forgejo response status", "status", resp.Status)
	return strconv.FormatInt(pullRequest.ID, 10), nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj *promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := forgejo.EditPullRequestOption{
		Title: title,
		Body:  description,
	}

	start := time.Now()
	_, resp, err := pr.foregejoClient.EditPullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to update pull request: %w", err)
	}

	logger.V(4).Info("forgejo response status", "status", resp.Status)
	return nil
}

func (pr *PullRequest) Close(ctx context.Context, prObj *promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	state := forgejo.StateClosed
	options := forgejo.EditPullRequestOption{
		State: &state,
	}

	start := time.Now()
	_, resp, err := pr.foregejoClient.EditPullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to close pull request: %w", err)
	}

	logger.V(4).Info("forgejo response status", "status", resp.Status)
	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, prObj *promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := forgejo.MergePullRequestOption{
		Message: commitMessage,
	}

	start := time.Now()
	_, resp, err := pr.foregejoClient.MergePullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to merge pull request: %w", err)
	}
	logger.V(4).Info("forgejo response status", "status", resp.Status)
	return nil
}

func (pr *PullRequest) FindOpen(ctx context.Context, prObj *promoterv1alpha1.PullRequest) (bool, string, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Name,
	})
	if err != nil {
		return false, "", fmt.Errorf("failed to merge pull request: %w", err)
	}

	options := forgejo.ListPullRequestsOptions{}

	start := time.Now()
	prs, resp, err := pr.foregejoClient.ListRepoPullRequests(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return false, "", fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.V(4).Info("forgejo response status", "status", resp.Status)

	for _, pr := range prs {
		// REVIEW: head =? source or is it the other way around
		// REVIEW: we may need to improve the pr selection. This is a hazard and can create weird situations is multiple PRs are targeting the same branches.
		if pr.Head.Name != prObj.Spec.SourceBranch ||
			pr.Base.Name != prObj.Spec.TargetBranch ||
			pr.State != forgejo.StateOpen {
			continue
		}

		prState, err := mapPullRequestState(*pr)
		if err != nil {
			return false, "", err
		}

		prObj.Status.ID = strconv.FormatInt(pr.ID, 10)
		prObj.Status.State = prState
		return true, prObj.Status.ID, nil
	}

	return false, "", nil
}
