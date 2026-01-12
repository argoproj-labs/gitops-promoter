package webhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GiteaParser parses Gitea and Forgejo webhooks (compatible format).
type GiteaParser struct{}

// Parse parses a Gitea or Forgejo webhook request and returns a WebhookEvent.
func (p GiteaParser) Parse(r *http.Request) (*WebhookEvent, error) {
	// Gitea uses X-Gitea-Event, Forgejo uses X-Forgejo-Event
	eventType := r.Header.Get("X-Gitea-Event")
	forgejoEvent := r.Header.Get("X-Forgejo-Event")

	if eventType == "" {
		eventType = forgejoEvent
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	provider := "gitea"
	if forgejoEvent != "" {
		provider = "forgejo"
	}

	var event *WebhookEvent

	switch eventType {
	case "push":
		event, err = p.parsePush(body, provider)
	case "pull_request":
		event, err = p.parsePullRequest(body, provider)
	default:
		return nil, UnknownEventError{}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	return event, nil
}

// ValidateSecret validates the Gitea/Forgejo webhook signature using HMAC-SHA256.
// Gitea sends signatures in X-Gitea-Signature, Forgejo in X-Forgejo-Signature.
func (p GiteaParser) ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error {
	// TODO: Implement HMAC-SHA256 signature validation
	// Expected header: X-Gitea-Signature or X-Forgejo-Signature
	// For now, validation is not implemented
	_ = secret // Placeholder to avoid unused variable
	return nil
}

func (p GiteaParser) parsePush(body []byte, provider string) (*WebhookEvent, error) {
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
			Provider: provider,
			Ref:      payload.Ref,
			Before:   payload.Before,
			After:    payload.After,
		},
	}, nil
}

func (p GiteaParser) parsePullRequest(body []byte, provider string) (*WebhookEvent, error) {
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
			Provider: provider,
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
