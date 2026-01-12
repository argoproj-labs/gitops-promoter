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

var _ = Describe("ParseAny", func() {
	Context("When parsing webhooks from different providers", func() {
		It("should parse GitHub push event", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Github-Event", "push")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("github"))
		})

		It("should parse GitLab push event", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Gitlab-Event", "Push Hook")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("gitlab"))
		})

		It("should parse Gitea push event", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Gitea-Event", "push")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("gitea"))
		})

		It("should parse Forgejo push event", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Forgejo-Event", "push")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("forgejo"))
		})

		It("should parse Bitbucket push event", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "def456"
								}
							},
							"old": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "abc123"
								}
							}
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("bitbucket"))
		})

		It("should parse Azure DevOps push event", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"oldObjectId": "abc123",
							"newObjectId": "def456"
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePush))
			Expect(result.Event.Push.Provider).To(Equal("azure"))
		})

		It("should parse GitHub pull request event", func() {
			payload := `{
				"action": "opened",
				"number": 42,
				"pull_request": {
					"title": "Test PR",
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
			req.Header.Set("X-Github-Event", "pull_request")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Event).NotTo(BeNil())
			Expect(result.Event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(result.Event.PullRequest.Provider).To(Equal("github"))
		})
	})

	Context("When no parser can handle the request", func() {
		It("should return UnknownEventError", func() {
			payload := `{"some": "data"}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Custom-Header", "unknown")

			_, err := webhooks.ParseAny(req)

			Expect(err).To(HaveOccurred())
			var errUnknownEvent webhooks.UnknownEventError
			ok := errors.As(err, &errUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret validation", func() {
		It("should allow ValidateSecret to be called on result", func() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(simplePushPayload))
			req.Header.Set("X-Github-Event", "push")

			result, err := webhooks.ParseAny(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())

			// Validate with empty secret (should not error)
			err = result.ValidateSecret("")
			Expect(err).NotTo(HaveOccurred())

			// Validate with a secret (not implemented yet, but shouldn't panic)
			err = result.ValidateSecret("test-secret")
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
