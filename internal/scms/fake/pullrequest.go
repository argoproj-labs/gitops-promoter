package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	ginkgov2 "github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	pullRequests map[string]pullRequestProviderState
	mutexPR      sync.RWMutex
)

type pullRequestProviderState struct {
	id    string
	state v1alpha1.PullRequestState
}

// PullRequest implements the scms.PullRequestProvider interface for testing purposes.
type PullRequest struct {
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewFakePullRequestProvider creates a new instance of PullRequest for testing purposes.
func NewFakePullRequestProvider(k8sClient client.Client) *PullRequest {
	return &PullRequest{k8sClient: k8sClient}
}

// Create creates a new pull request with the specified title, head, base, and description.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest v1alpha1.PullRequest) (id string, err error) {
	logger := log.FromContext(ctx)
	mutexPR.Lock()
	if pullRequests == nil {
		pullRequests = make(map[string]pullRequestProviderState)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	pullRequestCopy := pullRequest.DeepCopy()
	if p, ok := pullRequests[pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)]; ok {
		logger.Info("Pull request already exists", "id", p.id, "pullRequestSpec", pullRequest.Spec, "pullRequestStatus", pullRequest.Status)
		if p.state == v1alpha1.PullRequestOpen {
			return id, errors.New("pull request already exists and is open")
		}
	}
	if pullRequestCopy == nil {
		return "", errors.New("pull request is nil")
	}

	id = strconv.Itoa(len(pullRequests) + 1)
	pullRequests[pr.getMapKey(*pullRequestCopy, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)] = pullRequestProviderState{
		id:    id,
		state: v1alpha1.PullRequestOpen,
	}

	mutexPR.Unlock()
	return id, nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest v1alpha1.PullRequest) error {
	return nil
}

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	mutexPR.Lock()
	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = pullRequestProviderState{
		id:    pullRequests[prKey].id,
		state: v1alpha1.PullRequestClosed,
	}
	mutexPR.Unlock()
	return nil
}

// Merge merges an existing pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	if pullRequest.Status.ID == "" {
		return errors.New("pull request ID is empty, cannot merge")
	}

	gitPath, err := os.MkdirTemp("", "*")
	defer func() {
		err := os.RemoveAll(gitPath)
		if err != nil {
			logger.Error(err, "failed to remove temp dir")
		}
	}()
	if err != nil {
		panic("could not make temp dir for repo server")
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	gitServerPort := 5000 + ginkgov2.GinkgoParallelProcess()
	gitServerPortStr := strconv.Itoa(gitServerPort)
	err = pr.runGitCmd(gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", "-b", pullRequest.Spec.TargetBranch, fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name), ".")
	if err != nil {
		return err
	}

	err = pr.runGitCmd(gitPath, "config", "user.name", "GitOps Promoter")
	if err != nil {
		logger.Error(err, "could not set git config")
		return err
	}

	err = pr.runGitCmd(gitPath, "config", "user.email", "GitOpsPromoter@argoproj.io")
	if err != nil {
		logger.Error(err, "could not set git config")
		return err
	}

	err = pr.runGitCmd(gitPath, "config", "pull.rebase", "false")
	if err != nil {
		return err
	}

	err = pr.runGitCmd(gitPath, "fetch", "--all")
	if err != nil {
		return fmt.Errorf("failed to fetch all: %w", err)
	}

	err = pr.runGitCmd(gitPath, "merge", "--no-ff", "origin/"+pullRequest.Spec.SourceBranch, "-m", pullRequest.Spec.PromoteCommitMessage)
	if err != nil {
		return fmt.Errorf("failed to merge branch: %w", err)
	}

	err = pr.runGitCmd(gitPath, "push")
	if err != nil {
		return err
	}

	mutexPR.Lock()
	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = pullRequestProviderState{
		id:    pullRequests[prKey].id,
		state: v1alpha1.PullRequestMerged,
	}
	mutexPR.Unlock()
	return nil
}

// FindOpen checks if a pull request is open and returns its status.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, v1alpha1.PullRequestCommonStatus, error) {
	mutexPR.RLock()
	found, id := pr.findOpen(ctx, pullRequest)
	mutexPR.RUnlock()

	prState := v1alpha1.PullRequestCommonStatus{
		ID:             id,
		State:          v1alpha1.PullRequestOpen,
		Url:            fmt.Sprintf("http://localhost:5000/%s/%s/pull/%s", pullRequest.Spec.RepositoryReference.Name, pullRequest.Spec.SourceBranch, id),
		PRCreationTime: metav1.Now(),
	}
	return found, prState, nil
}

func (pr *PullRequest) findOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string) {
	log.FromContext(ctx).Info("Finding open pull request", "pullRequest", pullRequest)
	if pullRequests == nil {
		return false, ""
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to get GitRepository")
		return false, ""
	}

	pullRequestState, ok := pullRequests[pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)]
	return ok && pullRequestState.state == v1alpha1.PullRequestOpen, pullRequestState.id
}

func (pr *PullRequest) getMapKey(pullRequest v1alpha1.PullRequest, owner, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s", owner, name, pullRequest.Spec.SourceBranch, pullRequest.Spec.TargetBranch)
}

func (pr *PullRequest) runGitCmd(gitPath string, args ...string) error {
	cmd := exec.Command("git", args...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = gitPath

	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if strings.Contains(stderrBuf.String(), "already exists and is not an empty directory") ||
			strings.Contains(stdoutBuf.String(), "nothing to commit, working tree clean") {
			return nil
		}
		return fmt.Errorf("failed to run git command: %s", stderrBuf.String())
	}

	return nil
}

// GetUrl retrieves the URL of the pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, pullRequest v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	if prState, ok := pullRequests[prKey]; ok {
		return fmt.Sprintf("http://localhost:5000/%s/%s/pull/%s", gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, prState.id), nil
	}

	logger.Info("Pull request not found", "pullRequest", pullRequest)
	return "", errors.New("pull request not found")
}
