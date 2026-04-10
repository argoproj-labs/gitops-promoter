package bitbucket_datacenter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// Client is an HTTP client for the Bitbucket DataCenter/Server REST API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
}

// GetClient creates a new Bitbucket DataCenter/Server client using credentials from the given secret.
// The secret must contain either a "token" key (Personal Access Token / HTTP Bearer) or both
// "username" and "password" keys (HTTP Basic Auth).
func GetClient(domain string, secret corev1.Secret) (*Client, error) {
	token := string(secret.Data["token"])
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])

	var authHeader string
	switch {
	case token != "":
		authHeader = "Bearer " + token
	case username != "" && password != "":
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		authHeader = "Basic " + credentials
	default:
		return nil, fmt.Errorf("secret %q must contain either 'token' or both 'username' and 'password'", secret.Name)
	}

	return &Client{
		baseURL:    "https://" + domain,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		authHeader: authHeader,
	}, nil
}

// do executes an authenticated HTTP request against the Bitbucket DataCenter API.
// It reads the response body, closes it, and returns the HTTP status code and body bytes.
func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // Closing response body; read error checked below

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return resp.StatusCode, respBody, nil
}

// ApplyHTTPAuth applies Bitbucket DataCenter authentication to the HTTP request.
// It prefers Bearer token auth, and falls back to HTTP Basic auth.
func ApplyHTTPAuth(secret corev1.Secret, req *http.Request) error {
	token := string(secret.Data["token"])
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username != "" && password != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+credentials)
		return nil
	}
	return errors.New("token or username/password required in secret for Bitbucket DataCenter SCM auth")
}

// phaseToBuildState converts a CommitStatusPhase to a Bitbucket DataCenter/Server build state.
// Bitbucket DataCenter states: SUCCESSFUL, FAILED, INPROGRESS
func phaseToBuildState(phase v1alpha1.CommitStatusPhase) string {
	switch phase {
	case v1alpha1.CommitPhaseSuccess:
		return "SUCCESSFUL"
	case v1alpha1.CommitPhasePending:
		return "INPROGRESS"
	default:
		return "FAILED"
	}
}

// pullRequestRefProject is the project identifier within a pull-request branch reference.
type pullRequestRefProject struct {
	Key string `json:"key"`
}

// pullRequestRefRepository is the nested repository JSON structure for a pull-request branch reference.
type pullRequestRefRepository struct {
	Project pullRequestRefProject `json:"project"`
	Slug    string                `json:"slug"`
}

// pullRequestRef is the JSON structure for a Bitbucket DataCenter pull-request branch reference.
type pullRequestRef struct {
	Repository pullRequestRefRepository `json:"repository"`
	ID         string                   `json:"id"`
}

// pullRequestPayload is the request body for creating or updating a pull request.
type pullRequestPayload struct {
	FromRef     pullRequestRef `json:"fromRef"`
	ToRef       pullRequestRef `json:"toRef"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Reviewers   []any          `json:"reviewers"`
	ID          int            `json:"id,omitempty"`
	Version     int            `json:"version,omitempty"`
}

// newPullRequestRef creates a pullRequestRef for the given branch and repository details.
func newPullRequestRef(branch, projectKey, repoSlug string) pullRequestRef {
	ref := pullRequestRef{
		ID: "refs/heads/" + branch,
	}
	ref.Repository.Slug = repoSlug
	ref.Repository.Project.Key = projectKey
	return ref
}
