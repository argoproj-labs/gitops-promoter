package scmserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// AdminClient is a client for the admin API of a running Server.
type AdminClient struct {
	http    *http.Client
	baseURL string
}

// NewAdminClient returns a client for the admin API at baseURL, such as "http://localhost:9000".
func NewAdminClient(baseURL string) *AdminClient {
	return &AdminClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *AdminClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Info returns how to reach every provider listener.
func (c *AdminClient) Info(ctx context.Context) (ServerInfo, error) {
	var out ServerInfo
	return out, c.do(ctx, http.MethodGet, "/api/info", nil, &out)
}

// CAPEM returns the CA certificate clients must trust.
func (c *AdminClient) CAPEM(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ca.pem", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get CA: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get CA: %s", resp.Status)
	}
	return b, nil
}

// Reset deletes all server state and restores default behavior.
func (c *AdminClient) Reset(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/reset", nil, nil)
}

// AddCredential registers a token or username/password for a provider.
func (c *AdminClient) AddCredential(ctx context.Context, cred Credential) error {
	return c.do(ctx, http.MethodPost, "/api/credentials", cred, nil)
}

// RegisterGitHubApp registers a GitHub App and its installations.
func (c *AdminClient) RegisterGitHubApp(ctx context.Context, req RegisterGitHubAppRequest) error {
	return c.do(ctx, http.MethodPost, "/api/github/apps", req, nil)
}

// CreateRepo creates a hosted repository.
func (c *AdminClient) CreateRepo(ctx context.Context, req CreateRepoRequest) (RepoInfo, error) {
	var out RepoInfo
	return out, c.do(ctx, http.MethodPost, "/api/repos", req, &out)
}

// Repo returns a hosted repository, including its branches.
func (c *AdminClient) Repo(ctx context.Context, id int64) (RepoInfo, error) {
	var out RepoInfo
	return out, c.do(ctx, http.MethodGet, "/api/repos/"+strconv.FormatInt(id, 10), nil, &out)
}

// DeleteRepo deletes a hosted repository.
func (c *AdminClient) DeleteRepo(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, "/api/repos/"+strconv.FormatInt(id, 10), nil, nil)
}

// Commit writes a commit to a branch, as if it had been pushed.
func (c *AdminClient) Commit(ctx context.Context, repoID int64, req CommitRequest) (CommitResponse, error) {
	var out CommitResponse
	return out, c.do(ctx, http.MethodPost, "/api/repos/"+strconv.FormatInt(repoID, 10)+"/commits", req, &out)
}

// SetProtection configures the required statuses of a branch.
func (c *AdminClient) SetProtection(ctx context.Context, repoID int64, branch string, req ProtectionRequest) error {
	return c.do(ctx, http.MethodPut, "/api/repos/"+strconv.FormatInt(repoID, 10)+"/protection/"+branch, req, nil)
}

// PullRequests lists pull requests of a repository (all when repoID is 0), optionally filtered by state.
func (c *AdminClient) PullRequests(ctx context.Context, repoID int64, state string) ([]PullRequestInfo, error) {
	q := url.Values{}
	if repoID != 0 {
		q.Set("repo", strconv.FormatInt(repoID, 10))
	}
	if state != "" {
		q.Set("state", state)
	}
	var out []PullRequestInfo
	return out, c.do(ctx, http.MethodGet, "/api/pulls?"+q.Encode(), nil, &out)
}

// PullRequest returns one pull request.
func (c *AdminClient) PullRequest(ctx context.Context, repoID int64, number int) (PullRequestInfo, error) {
	var out PullRequestInfo
	path := fmt.Sprintf("/api/repos/%d/pulls/%d", repoID, number)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// PullRequestAction changes a pull request out of band.
func (c *AdminClient) PullRequestAction(ctx context.Context, repoID int64, number int, action PullRequestAction) (PullRequestInfo, error) {
	var out PullRequestInfo
	path := fmt.Sprintf("/api/repos/%d/pulls/%d/actions", repoID, number)
	return out, c.do(ctx, http.MethodPost, path, action, &out)
}

// CommitStatuses lists statuses of a repository, optionally for one commit.
func (c *AdminClient) CommitStatuses(ctx context.Context, repoID int64, sha string) ([]CommitStatusInfo, error) {
	q := url.Values{}
	q.Set("repo", strconv.FormatInt(repoID, 10))
	if sha != "" {
		q.Set("sha", sha)
	}
	var out []CommitStatusInfo
	return out, c.do(ctx, http.MethodGet, "/api/statuses?"+q.Encode(), nil, &out)
}

// Requests returns the request log.
func (c *AdminClient) Requests(ctx context.Context) ([]RequestRecord, error) {
	var out []RequestRecord
	return out, c.do(ctx, http.MethodGet, "/api/requests", nil, &out)
}

// ClearRequests clears the request log.
func (c *AdminClient) ClearRequests(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/requests", nil, nil)
}

// Behavior returns the behavior of every provider.
func (c *AdminClient) Behavior(ctx context.Context) (map[Provider]Behavior, error) {
	var out map[Provider]Behavior
	return out, c.do(ctx, http.MethodGet, "/api/behavior", nil, &out)
}

// SetBehavior replaces the behavior of a provider.
func (c *AdminClient) SetBehavior(ctx context.Context, p Provider, b Behavior) error {
	return c.do(ctx, http.MethodPut, "/api/behavior/"+string(p), b, nil)
}

// ResetBehavior restores the default behavior of every provider.
func (c *AdminClient) ResetBehavior(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/behavior", nil, nil)
}

// AddFault adds a fault injection rule.
func (c *AdminClient) AddFault(ctx context.Context, rule FaultRule) (FaultRule, error) {
	var out FaultRule
	return out, c.do(ctx, http.MethodPost, "/api/faults", rule, &out)
}

// Faults lists fault injection rules, with their hit counts.
func (c *AdminClient) Faults(ctx context.Context) ([]FaultRule, error) {
	var out []FaultRule
	return out, c.do(ctx, http.MethodGet, "/api/faults", nil, &out)
}

// ClearFaults removes every fault injection rule.
func (c *AdminClient) ClearFaults(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/faults", nil, nil)
}

// AddWebhook subscribes a URL to push events.
func (c *AdminClient) AddWebhook(ctx context.Context, sub WebhookSubscription) (WebhookSubscription, error) {
	var out WebhookSubscription
	return out, c.do(ctx, http.MethodPost, "/api/webhooks", sub, &out)
}

// WebhookDeliveries returns recorded webhook deliveries.
func (c *AdminClient) WebhookDeliveries(ctx context.Context) ([]WebhookDelivery, error) {
	var out []WebhookDelivery
	return out, c.do(ctx, http.MethodGet, "/api/webhooks/deliveries", nil, &out)
}
