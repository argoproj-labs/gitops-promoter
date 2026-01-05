package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

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
	defer mutexPR.Unlock()
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

	return id, nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest v1alpha1.PullRequest) error {
	return nil
}

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	// Simulate real SCM provider behavior: require status.id to close a PR
	if pullRequest.Status.ID == "" {
		return errors.New("cannot close pull request: status.id is empty")
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	mutexPR.Lock()
	defer mutexPR.Unlock()
	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = pullRequestProviderState{
		id:    pullRequests[prKey].id,
		state: v1alpha1.PullRequestClosed,
	}
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
	_, err = pr.runGitCmd(ctx, gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", "-b", pullRequest.Spec.TargetBranch, fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name), ".")
	if err != nil {
		return err
	}

	_, err = pr.runGitCmd(ctx, gitPath, "config", "user.name", "GitOps Promoter")
	if err != nil {
		logger.Error(err, "could not set git config")
		return err
	}

	_, err = pr.runGitCmd(ctx, gitPath, "config", "user.email", "GitOpsPromoter@argoproj.io")
	if err != nil {
		logger.Error(err, "could not set git config")
		return err
	}

	_, err = pr.runGitCmd(ctx, gitPath, "config", "pull.rebase", "false")
	if err != nil {
		return err
	}

	_, err = pr.runGitCmd(ctx, gitPath, "fetch", "--all")
	if err != nil {
		return fmt.Errorf("failed to fetch all: %w", err)
	}

	// Verify that the source branch HEAD matches the expected merge SHA
	actualSha, err := pr.runGitCmd(ctx, gitPath, "rev-parse", "origin/"+pullRequest.Spec.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to get SHA of source branch: %w", err)
	}
	actualSha = strings.TrimSpace(actualSha)
	if actualSha != pullRequest.Spec.MergeSha {
		return fmt.Errorf("source branch HEAD SHA %q does not match expected merge SHA %q", actualSha, pullRequest.Spec.MergeSha)
	}

	// Get the SHA of the target branch before merging - this is the "before" SHA for the webhook
	beforeSha, err := pr.runGitCmd(ctx, gitPath, "rev-parse", "origin/"+pullRequest.Spec.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to get SHA of target branch before merge: %w", err)
	}
	beforeSha = strings.TrimSpace(beforeSha)

	_, err = pr.runGitCmd(ctx, gitPath, "merge", "--no-ff", "origin/"+pullRequest.Spec.SourceBranch, "-m", pullRequest.Spec.Commit.Message)
	if err != nil {
		return err
	}

	_, err = pr.runGitCmd(ctx, gitPath, "push")
	if err != nil {
		return err
	}

	// Send webhook after merge to simulate SCM provider webhook behavior
	// The webhook uses the "before" SHA (target branch SHA before the merge)
	// The new findChangeTransferPolicy code will search by active.hydrated.sha as fallback
	pr.sendWebhook(ctx, pullRequest, beforeSha)

	mutexPR.Lock()
	defer mutexPR.Unlock()
	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

	if _, ok := pullRequests[prKey]; !ok {
		return errors.New("pull request not found")
	}
	pullRequests[prKey] = pullRequestProviderState{
		id:    pullRequests[prKey].id,
		state: v1alpha1.PullRequestMerged,
	}
	return nil
}

// FindOpen checks if a pull request is open and returns its status.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string, time.Time, error) {
	mutexPR.RLock()
	found, id := pr.findOpen(ctx, pullRequest)
	mutexPR.RUnlock()

	return found, id, time.Now(), nil
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

// DeletePullRequest deletes a pull request from the fake provider's internal map.
// This is used for testing to simulate externally merged/closed PRs.
func (pr *PullRequest) DeletePullRequest(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	mutexPR.Lock()
	defer mutexPR.Unlock()
	if pullRequests == nil {
		return nil
	}
	prKey := pr.getMapKey(pullRequest, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	delete(pullRequests, prKey)
	return nil
}

// sendWebhook sends a webhook after a PR merge to simulate SCM provider webhook behavior
func (pr *PullRequest) sendWebhook(ctx context.Context, pullRequest v1alpha1.PullRequest, beforeSha string) {
	// Get the webhook receiver port from the test environment
	// This matches the port calculation in suite_test.go
	webhookReceiverPort := 8082 + ginkgov2.GinkgoParallelProcess()

	// Build GitHub-style webhook payload with the beforeSha (SHA before the merge)
	payload := pr.buildGitHubWebhookPayload(beforeSha, "refs/heads/"+pullRequest.Spec.TargetBranch)

	// Send the webhook request
	webhookURL := fmt.Sprintf("http://localhost:%d/", webhookReceiverPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewBufferString(payload))
	if err != nil {
		// Don't fail the merge if webhook fails - log it instead
		fmt.Printf("Failed to create webhook request after PR merge: %v\n", err)
		return
	}

	// Set GitHub webhook headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Github-Delivery", fmt.Sprintf("pr-merge-delivery-%d", time.Now().Unix()))

	// Send the request
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Don't fail the merge if webhook fails - log it instead
		fmt.Printf("Failed to send webhook request after PR merge: %v\n", err)
		return
	}
	if resp == nil {
		fmt.Println("Webhook receiver returned nil response after PR merge")
		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusNoContent {
		fmt.Printf("Webhook receiver returned unexpected status code after PR merge: %d\n", resp.StatusCode)
	}
}

// buildGitHubWebhookPayload builds a GitHub-style push webhook payload
func (pr *PullRequest) buildGitHubWebhookPayload(beforeSha, ref string) string {
	payload := map[string]any{
		"before": beforeSha,
		"ref":    ref,
		"pusher": map[string]any{
			"name": "fake-scm",
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Failed to marshal webhook payload: %v\n", err)
		return "{}"
	}
	return string(payloadBytes)
}

func (pr *PullRequest) runGitCmd(ctx context.Context, gitPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = gitPath

	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start git command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if strings.Contains(stderrBuf.String(), "already exists and is not an empty directory") ||
			strings.Contains(stdoutBuf.String(), "nothing to commit, working tree clean") {
			return stdoutBuf.String(), nil
		}
		return "", fmt.Errorf("failed to run git command: %s", stderrBuf.String())
	}

	return stdoutBuf.String(), nil
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
