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

var _ = Describe("GitLabParser", func() {
	var parser webhooks.GitLabParser

	BeforeEach(func() {
		parser = webhooks.GitLabParser{}
	})

	Context("Push Events", func() {
		It("should parse valid push hook", func() {
			payload := `{
				"object_kind": "push",
				"before": "abc123def456",
				"after": "def456abc789",
				"ref": "refs/heads/main"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Push Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("gitlab"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
			Expect(event.Push.Before).To(Equal("abc123def456"))
			Expect(event.Push.After).To(Equal("def456abc789"))
		})

		It("should handle branch push", func() {
			payload := `{
				"ref": "refs/heads/feature/test",
				"before": "111111",
				"after": "222222"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Push Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Ref).To(Equal("refs/heads/feature/test"))
		})
	})

	Context("Merge Request Events", func() {
		It("should parse MR opened event", func() {
			payload := `{
				"object_attributes": {
					"action": "open",
					"iid": 42,
					"title": "Add new feature",
					"state": "opened",
					"source_branch": "feature-branch",
					"target_branch": "main",
					"last_commit": {
						"id": "abc123"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(event.PullRequest).NotTo(BeNil())

			mr := event.PullRequest
			Expect(mr.Provider).To(Equal("gitlab"))
			Expect(mr.Action).To(Equal("open"))
			Expect(mr.ID).To(Equal(42))
			Expect(mr.Title).To(Equal("Add new feature"))
			Expect(mr.Ref).To(Equal("feature-branch"))
			Expect(mr.SHA).To(Equal("abc123"))
			Expect(mr.BaseRef).To(Equal("main"))
			Expect(mr.Merged).To(BeFalse())
		})

		It("should parse MR merged event", func() {
			payload := `{
				"object_attributes": {
					"action": "merge",
					"iid": 99,
					"title": "Fix bug",
					"state": "merged",
					"source_branch": "fix-bug",
					"target_branch": "main",
					"last_commit": {
						"id": "fix123"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("merge"))
			Expect(event.PullRequest.Merged).To(BeTrue())
		})

		It("should parse MR update event", func() {
			payload := `{
				"object_attributes": {
					"action": "update",
					"iid": 123,
					"title": "Update docs",
					"state": "opened",
					"source_branch": "docs",
					"target_branch": "main",
					"last_commit": {
						"id": "doc123"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Merge Request Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("update"))
			Expect(event.PullRequest.ID).To(Equal(123))
		})
	})

	Context("Unknown Events", func() {
		It("should return UnknownEventError for unsupported events", func() {
			payload := `{"action": "created"}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Tag Push Hook") // Unsupported

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			var errUnknownEvent webhooks.UnknownEventError
			ok := errors.As(err, &errUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret Validation", func() {
		It("should allow ValidateSecret to be called", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123",
				"after": "def456"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Push Hook")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "gitlab-token")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Invalid Payloads", func() {
		It("should handle malformed JSON", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(invalidJSON))
			req.Header.Set("X-Gitlab-Event", "Push Hook")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
		})
	})
})
