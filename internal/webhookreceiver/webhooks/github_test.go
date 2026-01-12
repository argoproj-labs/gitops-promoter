package webhooks_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver/webhooks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHubParser", func() {
	var parser webhooks.GitHubParser

	BeforeEach(func() {
		parser = webhooks.GitHubParser{}
	})

	Context("Push Events", func() {
		It("should parse valid push event", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc789"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("github"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
			Expect(event.Push.Before).To(Equal("abc123def456"))
			Expect(event.Push.After).To(Equal("def456abc789"))
		})

		It("should handle branch push", func() {
			payload := `{
				"ref": "refs/heads/feature/new-feature",
				"before": "111111",
				"after": "222222"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Ref).To(Equal("refs/heads/feature/new-feature"))
		})

		It("should handle tag push", func() {
			payload := `{
				"ref": "refs/tags/v1.0.0",
				"before": "000000",
				"after": "aaa111"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Ref).To(Equal("refs/tags/v1.0.0"))
		})
	})

	Context("Pull Request Events", func() {
		It("should parse PR opened event", func() {
			payload := `{
				"action": "opened",
				"number": 42,
				"pull_request": {
					"title": "Add new feature",
					"merged": false,
					"head": {
						"ref": "feature-branch",
						"sha": "abc123"
					},
					"base": {
						"ref": "main",
						"sha": "def456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(event.PullRequest).NotTo(BeNil())

			pr := event.PullRequest
			Expect(pr.Provider).To(Equal("github"))
			Expect(pr.Action).To(Equal("opened"))
			Expect(pr.ID).To(Equal(42))
			Expect(pr.Title).To(Equal("Add new feature"))
			Expect(pr.Ref).To(Equal("feature-branch"))
			Expect(pr.SHA).To(Equal("abc123"))
			Expect(pr.BaseRef).To(Equal("main"))
			Expect(pr.BaseSHA).To(Equal("def456"))
			Expect(pr.Merged).To(BeFalse())
		})

		It("should parse PR synchronize event", func() {
			payload := `{
				"action": "synchronize",
				"number": 123,
				"pull_request": {
					"title": "Update docs",
					"merged": false,
					"head": {
						"ref": "docs-update",
						"sha": "new123"
					},
					"base": {
						"ref": "main",
						"sha": "base456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("synchronize"))
			Expect(event.PullRequest.ID).To(Equal(123))
		})

		It("should parse PR closed event with merged=true", func() {
			payload := `{
				"action": "closed",
				"number": 99,
				"pull_request": {
					"title": "Fix bug",
					"merged": true,
					"head": {
						"ref": "fix-bug",
						"sha": "fix123"
					},
					"base": {
						"ref": "main",
						"sha": "main456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("closed"))
			Expect(event.PullRequest.Merged).To(BeTrue())
		})

		It("should parse PR closed event with merged=false", func() {
			payload := `{
				"action": "closed",
				"number": 99,
				"pull_request": {
					"title": "Fix bug",
					"merged": false,
					"head": {
						"ref": "fix-bug",
						"sha": "fix123"
					},
					"base": {
						"ref": "main",
						"sha": "main456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("closed"))
			Expect(event.PullRequest.Merged).To(BeFalse())
		})
	})

	Context("Unknown Events", func() {
		It("should return UnknownEventError for unsupported event types", func() {
			payload := `{"action": "created"}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "release") // Unsupported

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			var errUnknownEvent webhooks.UnknownEventError
			ok := errors.As(err, &errUnknownEvent)
			Expect(ok).To(BeTrue())
		})

		It("should return UnknownEventError for issues event", func() {
			payload := `{"action": "opened"}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "issues")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			var errUnknownEvent webhooks.UnknownEventError
			ok := errors.As(err, &errUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret Validation", func() {
		It("should not require secret validation", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
		})

		It("should allow ValidateSecret to be called", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "test-secret")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Invalid Payloads", func() {
		It("should handle malformed JSON", func() {
			payload := `{invalid json`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
		})

		It("should handle empty payload", func() {
			payload := `{}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Empty fields are allowed, just zero values
			Expect(event.Push.Ref).To(Equal(""))
			Expect(event.Push.Before).To(Equal(""))
			Expect(event.Push.After).To(Equal(""))
		})
	})
})
