package forgejo

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		Name:      prObj.Spec.RepositoryReference.Name,
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
	return strconv.FormatInt(pullRequest.Index, 10), nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj *promoterv1alpha1.PullRequest) error {
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
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	shouldReturn, err := checkOpenPR(ctx, pr, repo, prID)
	if shouldReturn {
		return err
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
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get git repository from object: %w", err)
	}

	shouldReturn, err := checkOpenPR(ctx, pr, repo, prID)
	if shouldReturn {
		return err
	}

	options := forgejo.MergePullRequestOption{
		Style:   forgejo.MergeStyleMerge, // TODO: make the merge style configurable
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

func (pr *PullRequest) FindOpen(ctx context.Context, prObj *promoterv1alpha1.PullRequest) (bool, promoterv1alpha1.PullRequestCommonStatus, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return false, promoterv1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to get git repository from object: %w", err)
	}

	options := forgejo.ListPullRequestsOptions{
		State: forgejo.StateOpen,
	}

	start := time.Now()
	prs, resp, err := pr.foregejoClient.ListRepoPullRequests(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return false, promoterv1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.V(4).Info("forgejo response status", "status", resp.Status)

	for _, prItem := range prs {
		if prItem.Head.Name != prObj.Spec.SourceBranch ||
			prItem.Base.Name != prObj.Spec.TargetBranch {
			continue
		}

		prState, err := forgejoPullRequestStateToPullRequestState(*prItem)
		if err != nil {
			return false, promoterv1alpha1.PullRequestCommonStatus{}, err
		}

		url, err := pr.GetUrl(ctx, prObj)
		if err != nil {
			return false, promoterv1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to get pull request URL: %w", err)
		}

		pullRequestStatus := promoterv1alpha1.PullRequestCommonStatus{
			ID:             strconv.FormatInt(prItem.Index, 10),
			State:          prState,
			Url:            url,
			PRCreationTime: metav1.Time{Time: *prItem.Created},
		}
		return true, pullRequestStatus, nil
	}

	return false, promoterv1alpha1.PullRequestCommonStatus{}, nil
}

func checkOpenPR(ctx context.Context, pr *PullRequest, repo *promoterv1alpha1.GitRepository, prID int64) (bool, error) {
	logger := log.FromContext(ctx)

	start := time.Now()
	existingPr, resp, err := pr.foregejoClient.GetPullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, prID)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return true, fmt.Errorf("failed to get pull request: %w", err)
	}
	logger.V(4).Info("forgejo response status", "status", resp.Status)

	return existingPr.State != forgejo.StateOpen, nil
}

func (pr *PullRequest) GetUrl(ctx context.Context, pullRequest *promoterv1alpha1.PullRequest) (string, error) {
	return "", nil
}
