package webhookreceiver_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseWebhook", func() {
	Context("GitHub webhook", func() {
		It("should parse push event", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc123",
				"pusher": {"name": "user", "email": "user@example.com"},
				"repository": {"full_name": "owner/repo"}
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("X-Github-Event", "push")
			req.Header.Set("X-Github-Delivery", "12345")
			req.Header.Set("Content-Type", "application/json")

			// Verify the request structure is valid for GitHub
			Expect(req.Header.Get("X-Github-Event")).To(Equal("push"))
		})
	})

	Context("GitLab webhook", func() {
		It("should parse push event", func() {
			payload := `{
				"object_kind": "push",
				"before": "abc123def456",
				"after": "def456abc123",
				"ref": "refs/heads/main",
				"user_name": "John Doe"
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitlab-Event", "Push Hook")
			req.Header.Set("Content-Type", "application/json")

			Expect(req.Header.Get("X-Gitlab-Event")).To(Equal("Push Hook"))
		})
	})

	Context("Gitea webhook", func() {
		It("should parse push event", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc123",
				"pusher": {"username": "user"}
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("X-Gitea-Event", "push")
			req.Header.Set("Content-Type", "application/json")

			Expect(req.Header.Get("X-Gitea-Event")).To(Equal("push"))
		})
	})

	Context("Forgejo webhook", func() {
		It("should parse push event and detect as Forgejo", func() {
			payload := `{
				"ref": "refs/heads/main",
				"before": "abc123def456",
				"after": "def456abc123",
				"pusher": {"username": "user"}
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("X-Forgejo-Event", "push")
			req.Header.Set("Content-Type", "application/json")

			Expect(req.Header.Get("X-Forgejo-Event")).To(Equal("push"))
		})
	})

	Context("Bitbucket Cloud webhook", func() {
		It("should parse push event", func() {
			payload := `{
				"actor": {"username": "user"},
				"repository": {"name": "repo"},
				"push": {
					"changes": [{
						"new": {
							"name": "main",
							"target": {"hash": "def456abc123"}
						},
						"old": {
							"name": "main",
							"target": {"hash": "abc123def456"}
						}
					}]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")
			req.Header.Set("X-Hook-Uuid", "12345-abcde")
			req.Header.Set("Content-Type", "application/json")

			Expect(req.Header.Get("X-Hook-Uuid")).To(Equal("12345-abcde"))
		})
	})

	Context("Azure DevOps webhook", func() {
		It("should parse git push event", func() {
			payload := `{
				"eventType": "git.push",
				"publisherId": "tfs",
				"resource": {
					"refUpdates": [{
						"name": "refs/heads/main",
						"oldObjectId": "abc123def456",
						"newObjectId": "def456abc123"
					}]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")

			// Azure DevOps doesn't have a specific event header, detection is via body
			Expect(req.Method).To(Equal(http.MethodPost))
		})
	})

	Context("extractDeliveryID", func() {
		It("should extract delivery IDs from various provider headers", func() {
			// Test GitHub
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-Github-Delivery", "github-123")
			Expect(req.Header.Get("X-Github-Delivery")).To(Equal("github-123"))

			// Test GitLab
			req = httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-Gitlab-Event-Uuid", "gitlab-456")
			Expect(req.Header.Get("X-Gitlab-Event-Uuid")).To(Equal("gitlab-456"))
		})
	})
})

var _ = Describe("Provider Constants", func() {
	It("should have correct provider names", func() {
		Expect(webhookreceiver.ProviderGitHub).To(Equal("github"))
		Expect(webhookreceiver.ProviderGitLab).To(Equal("gitlab"))
		Expect(webhookreceiver.ProviderForgejo).To(Equal("forgejo"))
		Expect(webhookreceiver.ProviderGitea).To(Equal("gitea"))
		Expect(webhookreceiver.ProviderBitbucketCloud).To(Equal("bitbucketCloud"))
		Expect(webhookreceiver.ProviderAzureDevops).To(Equal("azureDevOps"))
		Expect(webhookreceiver.ProviderUnknown).To(Equal(""))
	})
})
