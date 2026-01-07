package gitea

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"code.gitea.io/sdk/gitea"
	k8sV1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for Gitea.
type PullRequest struct {
	giteaClient *gitea.Client
	k8sClient   k8sClient.Client
	domain      string
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewGiteaPullRequestProvider creates a new instance of PullRequest for Gitea.
func NewGiteaPullRequestProvider(k8sClient k8sClient.Client, secret k8sV1.Secret, domain string) (*PullRequest, error) {
	client, err := GetClient(domain, secret)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		giteaClient: client,
		k8sClient:   k8sClient,
		domain:      domain,
	}, nil
}

// Create creates a new pull request with the specified title, head branch, base branch, and description.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, prObj promoterv1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := gitea.CreatePullRequestOption{
		Head:  head,
		Base:  base,
		Title: title,
		Body:  description,
	}

	start := time.Now()
	pullRequest, resp, err := pr.giteaClient.CreatePullRequest(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return "", err //nolint:wrapcheck // Error wrapping handled at top level
	}

	logger.V(4).Info("gitea response status", "status", resp.Status)
	return strconv.FormatInt(pullRequest.Index, 10), nil
}

// Update updates the title and description of an existing pull request.
func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := gitea.EditPullRequestOption{
		Title: title,
		Body:  &description,
	}

	start := time.Now()
	_, resp, err := pr.giteaClient.EditPullRequest(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}

	logger.V(4).Info("gitea response status", "status", resp.Status)
	return nil
}

// Close closes a pull request by changing its state to closed.
func (pr *PullRequest) Close(ctx context.Context, prObj promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	shouldReturn, err := checkOpenPR(ctx, *pr, repo, prID)
	if shouldReturn {
		return err
	}

	state := gitea.StateClosed
	options := gitea.EditPullRequestOption{
		State: &state,
	}

	start := time.Now()
	_, resp, err := pr.giteaClient.EditPullRequest(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}

	logger.V(4).Info("gitea response status", "status", resp.Status)
	return nil
}

// Merge merges a pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, prObj promoterv1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	shouldReturn, err := checkOpenPR(ctx, *pr, repo, prID)
	if shouldReturn {
		return err
	}

	options := gitea.MergePullRequestOption{
		Style:        gitea.MergeStyleMerge, // TODO: make the merge style configurable
		Message:      prObj.Spec.Commit.Message,
		HeadCommitId: prObj.Spec.MergeSha,
	}

	start := time.Now()
	_, resp, err := pr.giteaClient.MergePullRequest(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, prID, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	logger.V(4).Info("gitea response status", "status", resp.Status)
	return nil
}

// FindOpen checks if a pull request with the specified source and target branches exists and is open.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) (bool, string, time.Time, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: pullRequest.Namespace,
		Name:      pullRequest.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := gitea.ListPullRequestsOptions{
		State: gitea.StateOpen,
	}

	start := time.Now()
	prs, resp, err := pr.giteaClient.ListRepoPullRequests(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.V(4).Info("gitea response status", "status", resp.Status)

	for _, prItem := range prs {
		if prItem.Head.Name != pullRequest.Spec.SourceBranch ||
			prItem.Base.Name != pullRequest.Spec.TargetBranch {
			continue
		}
		return true, strconv.FormatInt(prItem.Index, 10), *prItem.Created, nil
	}

	return false, "", time.Time{}, nil
}

func checkOpenPR(ctx context.Context, pr PullRequest, repo *promoterv1alpha1.GitRepository, prID int64) (bool, error) {
	logger := log.FromContext(ctx)

	start := time.Now()
	existingPr, resp, err := pr.giteaClient.GetPullRequest(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name, prID)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return true, fmt.Errorf("failed to get pull request: %w", err)
	}
	logger.V(4).Info("gitea response status", "status", resp.Status)

	return existingPr.State != gitea.StateOpen, nil
}

// GetUrl constructs the URL for a pull request based on the provided PullRequest object.
func (pr *PullRequest) GetUrl(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) (string, error) {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	return fmt.Sprintf("https://%s/%s/%s/pulls/%s", pr.domain, gitRepo.Spec.Gitea.Owner, gitRepo.Spec.Gitea.Name, pullRequest.Status.ID), nil
}
