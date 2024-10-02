package fake

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var pullRequests map[string]PullRequestProviderState
var mutexPR sync.RWMutex

type PullRequestProviderState struct {
	ID    string
	State v1alpha1.PullRequestState
}
type PullRequest struct {
}

func NewFakePullRequestProvider() *PullRequest {
	return &PullRequest{}
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (id string, err error) {
	logger := log.FromContext(ctx)
	mutexPR.Lock()
	if pullRequests == nil {
		pullRequests = make(map[string]PullRequestProviderState)
	}

	pullRequestCopy := pullRequest.DeepCopy()
	if p, ok := pullRequests[pr.getMapKey(*pullRequest)]; ok {
		logger.Info("Pull request already exists", "id", p.ID)
	}

	pullRequests[pr.getMapKey(*pullRequestCopy)] = PullRequestProviderState{
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
	prKey := pr.getMapKey(*pullRequest)
	if _, ok := pullRequests[prKey]; !ok {
		return fmt.Errorf("pull request not found")
	}
	pullRequests[prKey] = PullRequestProviderState{
		ID:    pullRequests[prKey].ID,
		State: v1alpha1.PullRequestClosed,
	}
	mutexPR.Unlock()
	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	gitPath, err := os.MkdirTemp("", "*")
	defer os.RemoveAll(gitPath)
	if err != nil {
		panic("could not make temp dir for repo server")
	}

	gitServerPort := 5000 + GinkgoParallelProcess()
	gitServerPortStr := fmt.Sprintf("%d", gitServerPort)
	_, err = pr.runGitCmd(gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", "-b", pullRequest.Spec.TargetBranch, fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name), ".")
	if err != nil {
		return err
	}

	_, err = pr.runGitCmd(gitPath, "config", "pull.rebase", "false")
	if err != nil {
		return err
	}

	_, err = pr.runGitCmd(gitPath, "pull", "origin", pullRequest.Spec.SourceBranch)
	if err != nil {
		return err
	}
	_, err = pr.runGitCmd(gitPath, "push")

	mutexPR.Lock()
	prKey := pr.getMapKey(*pullRequest)

	if _, ok := pullRequests[prKey]; !ok {
		return fmt.Errorf("pull request not found")
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
	pullRequestState, ok := pullRequests[pr.getMapKey(*pullRequest)]
	return ok && pullRequestState.State == v1alpha1.PullRequestOpen
}

func (pr *PullRequest) getMapKey(pullRequest v1alpha1.PullRequest) string {
	return fmt.Sprintf("%s/%s/%s/%s", pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, pullRequest.Spec.SourceBranch, pullRequest.Spec.TargetBranch)
}

func (pr *PullRequest) runGitCmd(gitPath string, args ...string) (string, error) {
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
		return "", err
	}

	if err := cmd.Wait(); err != nil {
		if strings.Contains(stderrBuf.String(), "already exists and is not an empty directory") ||
			strings.Contains(stdoutBuf.String(), "nothing to commit, working tree clean") {
			return "", nil
		}
		return "", fmt.Errorf("failed to run git command: %s", stderrBuf.String())
	}

	return stdoutBuf.String(), nil
}
