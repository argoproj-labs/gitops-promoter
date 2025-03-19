package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	. "github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	pullRequests map[string]PullRequestProviderState
	mutexPR      sync.RWMutex
)

type PullRequestProviderState struct {
	ID    string
	State v1alpha1.PullRequestState
}
type PullRequest struct {
	k8sClient client.Client
}

func NewFakePullRequestProvider(k8sClient client.Client) *PullRequest {
	return &PullRequest{k8sClient: k8sClient}
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (id string, err error) {
	logger := log.FromContext(ctx)
	mutexPR.Lock()
	if pullRequests == nil {
		pullRequests = make(map[string]PullRequestProviderState)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	pullRequestCopy := pullRequest.DeepCopy()
	if p, ok := pullRequests[pr.getMapKey(*pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)]; ok {
		logger.Info("Pull request already exists", "id", p.ID)
	}
	if pullRequestCopy == nil {
		return "", errors.New("pull request is nil")
	}

	pullRequests[pr.getMapKey(*pullRequestCopy, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)] = PullRequestProviderState{
		ID:    fmt.Sprintf("%d", len(pullRequests)+1),
		State: v1alpha1.PullRequestOpen,
	}

	mutexPR.Unlock()
	return id, nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error {
	return nil
}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	mutexPR.Lock()

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prKey := pr.getMapKey(*pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = PullRequestProviderState{
		ID:    pullRequests[prKey].ID,
		State: v1alpha1.PullRequestClosed,
	}
	mutexPR.Unlock()
	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)
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

	gitServerPort := 5000 + GinkgoParallelProcess()
	gitServerPortStr := fmt.Sprintf("%d", gitServerPort)
	err = pr.runGitCmd(gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", "-b", pullRequest.Spec.TargetBranch, fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name), ".")
	if err != nil {
		return err
	}

	err = pr.runGitCmd(gitPath, "config", "pull.rebase", "false")
	if err != nil {
		return err
	}

	err = pr.runGitCmd(gitPath, "pull", "origin", pullRequest.Spec.SourceBranch)
	if err != nil {
		return err
	}
	err = pr.runGitCmd(gitPath, "push")
	if err != nil {
		return err
	}

	mutexPR.Lock()
	prKey := pr.getMapKey(*pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = PullRequestProviderState{
		ID:    pullRequests[prKey].ID,
		State: v1alpha1.PullRequestMerged,
	}
	mutexPR.Unlock()
	return nil
}

func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error) {
	mutexPR.RLock()
	found := pr.findOpen(ctx, pullRequest)
	mutexPR.RUnlock()
	return found, nil
}

func (pr *PullRequest) findOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) bool {
	log.FromContext(ctx).Info("Finding open pull request", "pullRequest", pullRequest)
	if pullRequests == nil {
		return false
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to get GitRepository")
		return false
	}

	pullRequestState, ok := pullRequests[pr.getMapKey(*pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)]
	return ok && pullRequestState.State == v1alpha1.PullRequestOpen
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
