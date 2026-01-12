package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
)

// AzureParser parses Azure DevOps webhooks.
type AzureParser struct{}

func (p AzureParser) Parse(r *http.Request) (*WebhookEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Azure doesn't have specific header, check payload eventType
	var basePayload struct {
		EventType string `json:"eventType"`
	}

	if err := json.Unmarshal(body, &basePayload); err != nil {
		return nil, err
	}

	var event *WebhookEvent

	switch basePayload.EventType {
	case "git.push":
		event, err = p.parsePush(body)
	case "git.pullrequest.created", "git.pullrequest.updated", "git.pullrequest.merged":
		event, err = p.parsePullRequest(body, basePayload.EventType)
	default:
		return nil, ErrUnknownEvent{}
	}

	if err != nil {
		return nil, err
	}

	return event, nil
}

// ValidateSecret validates the Azure DevOps webhook authentication.
// Azure DevOps can use basic auth or bearer tokens.
func (p AzureParser) ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error {
	// TODO: Implement basic auth or token validation
	// Azure DevOps can use basic auth or bearer tokens
	// For now, validation is not implemented
	_ = secret // Placeholder to avoid unused variable
	return nil
}

func (p AzureParser) parsePush(body []byte) (*WebhookEvent, error) {
	var payload struct {
		Resource struct {
			RefUpdates []struct {
				Name        string `json:"name"`
				OldObjectID string `json:"oldObjectId"`
				NewObjectID string `json:"newObjectId"`
			} `json:"refUpdates"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	if len(payload.Resource.RefUpdates) == 0 {
		return nil, ErrUnknownEvent{}
	}

	refUpdate := payload.Resource.RefUpdates[0]

	return &WebhookEvent{
		Type: EventTypePush,
		Push: &PushEvent{
			Provider: "azure",
			Ref:      refUpdate.Name,
			Before:   refUpdate.OldObjectID,
			After:    refUpdate.NewObjectID,
		},
	}, nil
}

func (p AzureParser) parsePullRequest(body []byte, eventType string) (*WebhookEvent, error) {
	var payload struct {
		Resource struct {
			PullRequestID         int    `json:"pullRequestId"`
			Title                 string `json:"title"`
			Status                string `json:"status"` // active, completed, abandoned
			MergeStatus           string `json:"mergeStatus"`
			SourceRefName         string `json:"sourceRefName"`
			TargetRefName         string `json:"targetRefName"`
			LastMergeSourceCommit struct {
				CommitID string `json:"commitId"`
			} `json:"lastMergeSourceCommit"`
			LastMergeTargetCommit struct {
				CommitID string `json:"commitId"`
			} `json:"lastMergeTargetCommit"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	// Map Azure event types to actions
	action := "unknown"
	switch eventType {
	case "git.pullrequest.created":
		action = "opened"
	case "git.pullrequest.updated":
		action = "synchronize"
	case "git.pullrequest.merged":
		action = "closed"
	}

	return &WebhookEvent{
		Type: EventTypePullRequest,
		PullRequest: &PullRequestEvent{
			Provider: "azure",
			Action:   action,
			ID:       payload.Resource.PullRequestID,
			Title:    payload.Resource.Title,
			Ref:      payload.Resource.SourceRefName,
			SHA:      payload.Resource.LastMergeSourceCommit.CommitID,
			BaseRef:  payload.Resource.TargetRefName,
			BaseSHA:  payload.Resource.LastMergeTargetCommit.CommitID,
			Merged:   payload.Resource.Status == "completed" && eventType == "git.pullrequest.merged",
		},
	}, nil
}
