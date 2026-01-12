package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
)

// BitbucketParser parses Bitbucket Cloud webhooks.
type BitbucketParser struct{}

func (p BitbucketParser) Parse(r *http.Request) (*WebhookEvent, error) {
	eventType := r.Header.Get("X-Event-Key")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	var event *WebhookEvent

	switch eventType {
	case "repo:push":
		event, err = p.parsePush(body)
	case "pullrequest:created", "pullrequest:updated", "pullrequest:fulfilled", "pullrequest:rejected":
		event, err = p.parsePullRequest(body, eventType)
	default:
		return nil, ErrUnknownEvent{}
	}

	if err != nil {
		return nil, err
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
	// Bitbucket has nested structure
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
		return nil, err
	}

	if len(payload.Push.Changes) == 0 {
		return nil, ErrUnknownEvent{}
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
	var payload struct {
		PullRequest struct {
			ID     int    `json:"id"`
			Title  string `json:"title"`
			State  string `json:"state"` // OPEN, MERGED, DECLINED
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
		} `json:"pullrequest"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	// Map Bitbucket event types to actions
	action := "unknown"
	switch eventKey {
	case "pullrequest:created":
		action = "opened"
	case "pullrequest:updated":
		action = "synchronize"
	case "pullrequest:fulfilled":
		action = "closed"
	case "pullrequest:rejected":
		action = "closed"
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
