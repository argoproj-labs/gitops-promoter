package webhooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver/webhooks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GiteaParser", func() {
	var parser webhooks.GiteaParser

	BeforeEach(func() {
		parser = webhooks.GiteaParser{}
	})

	Context("Gitea Push Events", func() {
		It("should parse Gitea push event", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc789"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("gitea"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
			Expect(event.Push.Before).To(Equal("abc123def456"))
			Expect(event.Push.After).To(Equal("def456abc789"))
		})
	})

	Context("Forgejo Push Events", func() {
		It("should parse Forgejo push event and detect as Forgejo", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc789"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Forgejo-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("forgejo"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
		})

		It("should prefer Forgejo header when both are present", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123",
				"after": "def456"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "push")
			req.Header.Set("X-Forgejo-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Provider).To(Equal("forgejo"))
		})
	})

	Context("Pull Request Events", func() {
		It("should parse Gitea PR opened event", func() {
			payload := `{
				"action": "opened",
				"number": 42,
				"pull_request": {
					"title": "Add feature",
					"merged": false,
					"head": {
						"ref": "feature",
						"sha": "abc123"
					},
					"base": {
						"ref": "main",
						"sha": "def456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(event.PullRequest).NotTo(BeNil())

			pr := event.PullRequest
			Expect(pr.Provider).To(Equal("gitea"))
			Expect(pr.Action).To(Equal("opened"))
			Expect(pr.ID).To(Equal(42))
			Expect(pr.Title).To(Equal("Add feature"))
			Expect(pr.Ref).To(Equal("feature"))
			Expect(pr.SHA).To(Equal("abc123"))
			Expect(pr.BaseRef).To(Equal("main"))
			Expect(pr.BaseSHA).To(Equal("def456"))
			Expect(pr.Merged).To(BeFalse())
		})

		It("should parse Forgejo PR event", func() {
			payload := `{
				"action": "opened",
				"number": 99,
				"pull_request": {
					"title": "Fix bug",
					"merged": false,
					"head": {
						"ref": "fix",
						"sha": "fix123"
					},
					"base": {
						"ref": "main",
						"sha": "main456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Forgejo-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Provider).To(Equal("forgejo"))
			Expect(event.PullRequest.ID).To(Equal(99))
		})

		It("should parse PR merged event", func() {
			payload := `{
				"action": "closed",
				"number": 50,
				"pull_request": {
					"title": "Merge this",
					"merged": true,
					"head": {
						"ref": "branch",
						"sha": "sha1"
					},
					"base": {
						"ref": "main",
						"sha": "sha2"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "pull_request")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Merged).To(BeTrue())
		})
	})

	Context("Unknown Events", func() {
		It("should return ErrUnknownEvent for unsupported Gitea events", func() {
			payload := `{}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "release") // Unsupported

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})

		It("should return ErrUnknownEvent for unsupported Forgejo events", func() {
			payload := `{}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Forgejo-Event", "issues")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret Validation", func() {
		It("should allow ValidateSecret for Gitea", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123",
				"after": "def456"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("gitea"))

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "secret")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow ValidateSecret for Forgejo", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123",
				"after": "def456"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Forgejo-Event", "push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("forgejo"))

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "secret")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Invalid Payloads", func() {
		It("should handle malformed JSON", func() {
			payload := `{invalid json`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "push")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
		})
	})
})
