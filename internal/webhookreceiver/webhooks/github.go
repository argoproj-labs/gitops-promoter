package webhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GitHubParser parses GitHub webhooks.
type GitHubParser struct{}

// Parse parses a GitHub webhook request and returns a WebhookEvent.
func (p GitHubParser) Parse(r *http.Request) (*WebhookEvent, error) {
	eventType := r.Header.Get("X-Github-Event")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	var event *WebhookEvent

	switch eventType {
	case "push":
		event, err = p.parsePush(body)
	case "pull_request":
		event, err = p.parsePullRequest(body)
	default:
		return nil, UnknownEventError{}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	return event, nil
}

// ValidateSecret validates the GitHub webhook signature using HMAC-SHA256.
// GitHub sends signatures in X-Hub-Signature-256 (SHA256) or X-Hub-Signature (SHA1) headers.
func (p GitHubParser) ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error {
	// TODO: Implement HMAC-SHA256 signature validation
	// Expected header: X-Hub-Signature-256 or X-Hub-Signature
	// For now, validation is not implemented
	_ = secret // Placeholder to avoid unused variable
	return nil
}

func (p GitHubParser) parsePush(body []byte) (*WebhookEvent, error) {
	var payload struct {
		Ref    string `json:"ref"`
		Before string `json:"before"`
		After  string `json:"after"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse push event: %w", err)
	}

	return &WebhookEvent{
		Type: EventTypePush,
		Push: &PushEvent{
			Provider: "github",
			Ref:      payload.Ref,
			Before:   payload.Before,
			After:    payload.After,
		},
	}, nil
}

func (p GitHubParser) parsePullRequest(body []byte) (*WebhookEvent, error) {
	//nolint:revive // nested structs required for JSON unmarshaling
	var payload struct {
		Action      string `json:"action"`
		PullRequest struct {
			Head struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"base"`
			Title  string `json:"title"`
			Merged bool   `json:"merged"`
		} `json:"pull_request"`
		Number int `json:"number"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pull request event: %w", err)
	}

	return &WebhookEvent{
		Type: EventTypePullRequest,
		PullRequest: &PullRequestEvent{
			Provider: "github",
			Action:   payload.Action,
			ID:       payload.Number,
			Title:    payload.PullRequest.Title,
			Ref:      payload.PullRequest.Head.Ref,
			SHA:      payload.PullRequest.Head.SHA,
			BaseRef:  payload.PullRequest.Base.Ref,
			BaseSHA:  payload.PullRequest.Base.SHA,
			Merged:   payload.PullRequest.Merged,
		},
	}, nil
}
