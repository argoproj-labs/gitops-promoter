package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// gitlabTransitionErrRe extracts the current state from GitLab's pipeline state machine error:
//
//	"Cannot transition status via :ACTION from :STATE"
//
// See https://gitlab.com/gitlab-org/gitlab-foss/-/issues/25807
// State machine source: https://gitlab.com/gitlab-org/gitlab/-/blob/master/app/models/commit_status.rb
var gitlabTransitionErrRe = regexp.MustCompile(`Cannot transition status via :\w+ from :(\w+)`)

// isAlreadyInDesiredState checks whether a GitLab API error indicates a no-op state transition,
// i.e. the commit status is already in the state we requested. GitLab's pipeline state machine
// rejects transitions to the current state with HTTP 400 and an error like:
//
//	"Cannot transition status via :enqueue from :pending"
//
// We parse the current GitLab state from the error and compare it against the state we requested
// (derived from phase via phaseToBuildState).
func isAlreadyInDesiredState(resp *gitlab.Response, err error, phase v1alpha1.CommitStatusPhase) bool {
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		return false
	}
	matches := gitlabTransitionErrRe.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return false
	}
	return matches[1] == string(phaseToBuildState(phase))
}

// CommitStatus implements the scms.CommitStatusProvider interface for GitLab.
type CommitStatus struct {
	client    *gitlab.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewGitlabCommitStatusProvider creates a new instance of CommitStatus for GitLab.
func NewGitlabCommitStatusProvider(k8sClient client.Client, secret v1.Secret, domain string) (*CommitStatus, error) {
	client, err := GetClient(secret, domain)
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

	commitStatusOptions := &gitlab.SetCommitStatusOptions{
		State:       phaseToBuildState(commitStatus.Spec.Phase),
		TargetURL:   gitlab.Ptr(commitStatus.Spec.Url),
		Name:        gitlab.Ptr(commitStatus.Spec.Name),
		Description: gitlab.Ptr(commitStatus.Spec.Description),
	}

	start := time.Now()
	glStatus, resp, err := cs.client.Commits.SetCommitStatus(
		repo.Spec.GitLab.ProjectID,
		commitStatus.Spec.Sha,
		commitStatusOptions,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		// Status.Id is intentionally left unchanged: a prior successful Set call already populated
		// it. On first reconcile (or after controller restart) Id may be empty, but GitLab's Set
		// endpoint routes by project+SHA+name, not Id, so subsequent calls still succeed.
		if isAlreadyInDesiredState(resp, err, commitStatus.Spec.Phase) {
			logger.Info("GitLab status already in desired state, treating as synced",
				"sha", commitStatus.Spec.Sha, "phase", commitStatus.Spec.Phase)
			commitStatus.Status.Phase = commitStatus.Spec.Phase
			commitStatus.Status.Sha = commitStatus.Spec.Sha
			return commitStatus, nil
		}
		return nil, fmt.Errorf("failed to create status: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		repo.Spec.ScmProviderRef.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	commitStatus.Status.Id = strconv.FormatInt(glStatus.ID, 10)
	commitStatus.Status.Phase = buildStateToPhase(gitlab.BuildStateValue(glStatus.Status))
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}
