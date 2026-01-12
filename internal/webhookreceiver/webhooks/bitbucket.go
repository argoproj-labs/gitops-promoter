package webhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// BitbucketParser parses Bitbucket Cloud webhooks.
type BitbucketParser struct{}

// Parse parses a Bitbucket Cloud webhook request and returns a WebhookEvent.
func (p BitbucketParser) Parse(r *http.Request) (*WebhookEvent, error) {
	eventType := r.Header.Get("X-Event-Key")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	var event *WebhookEvent

	switch eventType {
	case "repo:push":
		event, err = p.parsePush(body)
	case "pullrequest:created", "pullrequest:updated", "pullrequest:fulfilled", "pullrequest:rejected":
		event, err = p.parsePullRequest(body, eventType)
	default:
		return nil, UnknownEventError{}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	return event, nil
}

// ValidateSecret validates the Bitbucket webhook signature.
// Bitbucket uses different mechanisms depending on webhook configuration.
func (p BitbucketParser) ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error {
	// TODO: Implement signature validation
	// Bitbucket uses different mechanisms depending on webhook configuration
	// For now, validation is not implemented
	_ = secret // Placeholder to avoid unused variable
	return nil
}

func (p BitbucketParser) parsePush(body []byte) (*WebhookEvent, error) {
	//nolint:revive // nested structs required for JSON unmarshaling
	var payload struct {
		Push struct {
			Changes []struct {
				New struct {
					Name   string `json:"name"`
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"new"`
				Old struct {
					Name   string `json:"name"`
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"old"`
			} `json:"changes"`
		} `json:"push"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse push event: %w", err)
	}

	if len(payload.Push.Changes) == 0 {
		return nil, UnknownEventError{}
	}

	change := payload.Push.Changes[0]
	ref := "refs/heads/" + change.New.Name
	if ref == "refs/heads/" {
		ref = "refs/heads/" + change.Old.Name
	}

	return &WebhookEvent{
		Type: EventTypePush,
		Push: &PushEvent{
			Provider: "bitbucket",
			Ref:      ref,
			Before:   change.Old.Target.Hash,
			After:    change.New.Target.Hash,
		},
	}, nil
}

func (p BitbucketParser) parsePullRequest(body []byte, eventKey string) (*WebhookEvent, error) {
	//nolint:revive // nested structs required for JSON unmarshaling
	var payload struct {
		PullRequest struct {
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
				Commit struct {
					Hash string `json:"hash"`
				} `json:"commit"`
			} `json:"source"`
			Destination struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
				Commit struct {
					Hash string `json:"hash"`
				} `json:"commit"`
			} `json:"destination"`
			Title string `json:"title"`
			State string `json:"state"`
			ID    int    `json:"id"`
		} `json:"pullrequest"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pull request event: %w", err)
	}

	// Map Bitbucket event types to actions
	var action string
	switch eventKey {
	case "pullrequest:created":
		action = "opened"
	case "pullrequest:updated":
		action = "synchronize"
	case "pullrequest:fulfilled", "pullrequest:rejected":
		action = "closed"
	default:
		action = "unknown"
	}

	return &WebhookEvent{
		Type: EventTypePullRequest,
		PullRequest: &PullRequestEvent{
			Provider: "bitbucket",
			Action:   action,
			ID:       payload.PullRequest.ID,
			Title:    payload.PullRequest.Title,
			Ref:      payload.PullRequest.Source.Branch.Name,
			SHA:      payload.PullRequest.Source.Commit.Hash,
			BaseRef:  payload.PullRequest.Destination.Branch.Name,
			BaseSHA:  payload.PullRequest.Destination.Commit.Hash,
			Merged:   payload.PullRequest.State == "MERGED",
		},
	}, nil
}
