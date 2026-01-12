//nolint:revive // Package defines webhook types and parsers - needs multiple public structs
package webhooks

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// EventType represents the type of webhook event.
type EventType string

const (
	// EventTypePush represents a push/commit event.
	EventTypePush EventType = "push"
	// EventTypePullRequest represents a pull/merge request event.
	EventTypePullRequest EventType = "pull_request"
	// EventTypeUnknown represents an unknown or unsupported event type.
	EventTypeUnknown EventType = "unknown"
)

// SecretFunc is a function that returns the webhook secret for validation.
// If it returns an empty string, validation is skipped.
// The function receives the parsed event to allow secret lookup based on repository/provider.
type SecretFunc func(event *WebhookEvent) (string, error)

// ValidationError is returned when webhook signature validation fails.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return "webhook validation failed: " + e.Message
}

// PushEvent represents a push/commit event from any SCM provider.
type PushEvent struct {
	Provider string // github, gitlab, gitea, forgejo, bitbucket, azure
	Ref      string // refs/heads/main
	Before   string // SHA before push
	After    string // SHA after push
}

// PullRequestEvent represents a pull/merge request event from any SCM provider.
type PullRequestEvent struct {
	Provider string
	Action   string
	Title    string
	Ref      string
	SHA      string
	BaseRef  string
	BaseSHA  string
	ID       int
	Merged   bool
}

// WebhookEvent is a union type for different webhook events.
type WebhookEvent struct {
	Push        *PushEvent
	PullRequest *PullRequestEvent
	Type        EventType
}

// Parser can parse webhook requests into WebhookEvents.
type Parser interface {
	// Parse parses an HTTP request and returns a WebhookEvent.
	// Secret validation is NOT performed during parsing.
	Parse(r *http.Request) (*WebhookEvent, error)

	// ValidateSecret validates the webhook signature/secret.
	// Returns nil if validation succeeds or is not applicable.
	// Returns ValidationError if validation fails.
	ValidateSecret(r *http.Request, event *WebhookEvent, secret string) error
}

// ParseResult contains a parsed webhook event and allows secret validation.
type ParseResult struct {
	Event   *WebhookEvent
	parser  Parser
	request *http.Request
}

// ValidateSecret validates the webhook secret/signature for this event.
// If secret is empty, validation is skipped and nil is returned.
// Returns ValidationError if validation fails.
func (pr *ParseResult) ValidateSecret(secret string) error {
	if secret == "" {
		return nil // Skip validation if no secret provided
	}
	if err := pr.parser.ValidateSecret(pr.request, pr.Event, secret); err != nil {
		return fmt.Errorf("webhook secret validation failed: %w", err)
	}
	return nil
}

// UnknownEventError is returned when the webhook event type is not recognized.
type UnknownEventError struct{}

func (e UnknownEventError) Error() string {
	return "unknown event type"
}

// ParseAny attempts to parse a webhook request using all available parsers.
// It tries each parser in sequence (GitHub, GitLab, Gitea/Forgejo, Bitbucket, Azure DevOps)
// and returns the first successful parse result.
// Secret validation is NOT performed - call ValidateSecret on the returned ParseResult.
// If no parser succeeds, it returns an error.
func ParseAny(r *http.Request) (*ParseResult, error) {
	// Read the body once and buffer it so we can replay it for each parser
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	_ = r.Body.Close() // Explicitly ignore close error since we've already read the body

	parsers := []Parser{
		GitHubParser{},
		GitLabParser{},
		GiteaParser{},
		BitbucketParser{},
		AzureParser{},
	}

	for _, parser := range parsers {
		// Create a new request with a fresh body reader for each parser
		reqCopy := r.Clone(r.Context())
		reqCopy.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		if event, err := parser.Parse(reqCopy); err == nil {
			// Store the original request (with buffered body) for later validation
			originalReq := r.Clone(r.Context())
			originalReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			return &ParseResult{
				Event:   event,
				parser:  parser,
				request: originalReq,
			}, nil
		}
	}

	return nil, UnknownEventError{}
}
