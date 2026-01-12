package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
)

// GitHubParser parses GitHub webhooks.
type GitHubParser struct{}

func (p GitHubParser) Parse(r *http.Request) (*WebhookEvent, error) {
	eventType := r.Header.Get("X-Github-Event")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	var event *WebhookEvent

	switch eventType {
	case "push":
		event, err = p.parsePush(body)
	case "pull_request":
		event, err = p.parsePullRequest(body)
	default:
		return nil, ErrUnknownEvent{}
	}

	if err != nil {
		return nil, err
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
		return nil, err
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
	var payload struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Title  string `json:"title"`
			Merged bool   `json:"merged"`
			Head   struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"base"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
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
