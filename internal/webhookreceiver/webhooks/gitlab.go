package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
)

// GitLabParser parses GitLab webhooks.
type GitLabParser struct{}

func (p GitLabParser) Parse(r *http.Request) (*WebhookEvent, error) {
	eventType := r.Header.Get("X-Gitlab-Event")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	var event *WebhookEvent

	switch eventType {
	case "Push Hook":
		event, err = p.parsePush(body)
	case "Merge Request Hook":
		event, err = p.parseMergeRequest(body)
	default:
		return nil, ErrUnknownEvent{}
	}

	if err != nil {
		return nil, err
	}

	return event, nil
}

// ValidateSecret validates the GitLab webhook token.
// GitLab sends the secret token in the X-Gitlab-Token header.
func (p GitLabParser) ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error {
	// TODO: Implement token validation
	// Expected header: X-Gitlab-Token
	// For now, validation is not implemented
	_ = secret // Placeholder to avoid unused variable
	return nil
}

func (p GitLabParser) parsePush(body []byte) (*WebhookEvent, error) {
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
			Provider: "gitlab",
			Ref:      payload.Ref,
			Before:   payload.Before,
			After:    payload.After,
		},
	}, nil
}

func (p GitLabParser) parseMergeRequest(body []byte) (*WebhookEvent, error) {
	var payload struct {
		ObjectAttributes struct {
			Action      string `json:"action"`
			IID         int    `json:"iid"` // GitLab uses iid for MR number
			Title       string `json:"title"`
			State       string `json:"state"` // opened, closed, merged
			MergeStatus string `json:"merge_status"`
			LastCommit  struct {
				ID string `json:"id"`
			} `json:"last_commit"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
		} `json:"object_attributes"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	return &WebhookEvent{
		Type: EventTypePullRequest,
		PullRequest: &PullRequestEvent{
			Provider: "gitlab",
			Action:   payload.ObjectAttributes.Action,
			ID:       payload.ObjectAttributes.IID,
			Title:    payload.ObjectAttributes.Title,
			Ref:      payload.ObjectAttributes.SourceBranch,
			SHA:      payload.ObjectAttributes.LastCommit.ID,
			BaseRef:  payload.ObjectAttributes.TargetBranch,
			Merged:   payload.ObjectAttributes.State == "merged",
		},
	}, nil
}
