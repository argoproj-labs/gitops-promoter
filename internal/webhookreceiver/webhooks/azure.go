package webhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AzureParser parses Azure DevOps webhooks.
type AzureParser struct{}

const (
	azureEventPush      = "git.push"
	azureEventPRCreated = "git.pullrequest.created"
	azureEventPRUpdated = "git.pullrequest.updated"
	azureEventPRMerged  = "git.pullrequest.merged"
)

// Parse parses an Azure DevOps webhook request and returns a WebhookEvent.
func (p AzureParser) Parse(r *http.Request) (*WebhookEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	// Azure doesn't have specific header, check payload eventType
	var basePayload struct {
		EventType string `json:"eventType"`
	}

	if err := json.Unmarshal(body, &basePayload); err != nil {
		return nil, fmt.Errorf("failed to parse event type: %w", err)
	}

	var event *WebhookEvent

	switch basePayload.EventType {
	case azureEventPush:
		event, err = p.parsePush(body)
	case azureEventPRCreated, azureEventPRUpdated, azureEventPRMerged:
		event, err = p.parsePullRequest(body, basePayload.EventType)
	default:
		return nil, UnknownEventError{}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
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
	//nolint:revive // nested structs required for JSON unmarshaling
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
		return nil, fmt.Errorf("failed to parse push event: %w", err)
	}

	if len(payload.Resource.RefUpdates) == 0 {
		return nil, UnknownEventError{}
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
	//nolint:revive // nested structs required for JSON unmarshaling
	var payload struct {
		Resource struct {
			Title                 string `json:"title"`
			Status                string `json:"status"`
			MergeStatus           string `json:"mergeStatus"`
			SourceRefName         string `json:"sourceRefName"`
			TargetRefName         string `json:"targetRefName"`
			LastMergeSourceCommit struct {
				CommitID string `json:"commitId"`
			} `json:"lastMergeSourceCommit"`
			LastMergeTargetCommit struct {
				CommitID string `json:"commitId"`
			} `json:"lastMergeTargetCommit"`
			PullRequestID int `json:"pullRequestId"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pull request event: %w", err)
	}

	// Map Azure event types to actions
	var action string
	switch eventType {
	case azureEventPRCreated:
		action = "opened"
	case azureEventPRUpdated:
		action = "synchronize"
	case azureEventPRMerged:
		action = "closed"
	default:
		action = "unknown"
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
			Merged:   payload.Resource.Status == "completed" && eventType == azureEventPRMerged,
		},
	}, nil
}
