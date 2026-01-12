package webhooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver/webhooks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AzureParser", func() {
	var parser webhooks.AzureParser

	BeforeEach(func() {
		parser = webhooks.AzureParser{}
	})

	Context("Push Events", func() {
		It("should parse git.push event with single refUpdate", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"oldObjectId": "abc123def456",
							"newObjectId": "def456abc789"
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("azure"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
			Expect(event.Push.Before).To(Equal("abc123def456"))
			Expect(event.Push.After).To(Equal("def456abc789"))
		})

		It("should handle multiple refUpdates and use the first one", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/feature1",
							"oldObjectId": "old1",
							"newObjectId": "new1"
						},
						{
							"name": "refs/heads/feature2",
							"oldObjectId": "old2",
							"newObjectId": "new2"
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Should use first refUpdate
			Expect(event.Push.Ref).To(Equal("refs/heads/feature1"))
			Expect(event.Push.Before).To(Equal("old1"))
			Expect(event.Push.After).To(Equal("new1"))
		})

		It("should handle tag push", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/tags/v1.0.0",
							"oldObjectId": "0000000000000000000000000000000000000000",
							"newObjectId": "tag123abc"
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Ref).To(Equal("refs/tags/v1.0.0"))
			Expect(event.Push.After).To(Equal("tag123abc"))
		})
	})

	Context("Pull Request Events", func() {
		It("should parse git.pullrequest.created event", func() {
			payload := `{
				"eventType": "git.pullrequest.created",
				"resource": {
					"pullRequestId": 42,
					"title": "Add new feature",
					"status": "active",
					"mergeStatus": "succeeded",
					"sourceRefName": "refs/heads/feature-branch",
					"targetRefName": "refs/heads/main",
					"lastMergeSourceCommit": {
						"commitId": "abc123"
					},
					"lastMergeTargetCommit": {
						"commitId": "def456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(event.PullRequest).NotTo(BeNil())

			pr := event.PullRequest
			Expect(pr.Provider).To(Equal("azure"))
			Expect(pr.Action).To(Equal("opened"))
			Expect(pr.ID).To(Equal(42))
			Expect(pr.Title).To(Equal("Add new feature"))
			Expect(pr.Ref).To(Equal("refs/heads/feature-branch"))
			Expect(pr.SHA).To(Equal("abc123"))
			Expect(pr.BaseRef).To(Equal("refs/heads/main"))
			Expect(pr.BaseSHA).To(Equal("def456"))
			Expect(pr.Merged).To(BeFalse())
		})

		It("should parse git.pullrequest.updated event", func() {
			payload := `{
				"eventType": "git.pullrequest.updated",
				"resource": {
					"pullRequestId": 99,
					"title": "Updated PR",
					"status": "active",
					"mergeStatus": "succeeded",
					"sourceRefName": "refs/heads/update",
					"targetRefName": "refs/heads/main",
					"lastMergeSourceCommit": {
						"commitId": "update123"
					},
					"lastMergeTargetCommit": {
						"commitId": "main456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("synchronize"))
			Expect(event.PullRequest.ID).To(Equal(99))
		})

		It("should parse git.pullrequest.merged event with status=completed", func() {
			payload := `{
				"eventType": "git.pullrequest.merged",
				"resource": {
					"pullRequestId": 50,
					"title": "Merged PR",
					"status": "completed",
					"mergeStatus": "succeeded",
					"sourceRefName": "refs/heads/feature",
					"targetRefName": "refs/heads/main",
					"lastMergeSourceCommit": {
						"commitId": "feature123"
					},
					"lastMergeTargetCommit": {
						"commitId": "main789"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("closed"))
			Expect(event.PullRequest.Merged).To(BeTrue())
		})

		It("should detect merged=false from status=completed with updated eventType", func() {
			payload := `{
				"eventType": "git.pullrequest.updated",
				"resource": {
					"pullRequestId": 60,
					"title": "Completed PR",
					"status": "completed",
					"mergeStatus": "succeeded",
					"sourceRefName": "refs/heads/done",
					"targetRefName": "refs/heads/main",
					"lastMergeSourceCommit": {
						"commitId": "done123"
					},
					"lastMergeTargetCommit": {
						"commitId": "main999"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Merged is only true when eventType is "git.pullrequest.merged" AND status is "completed"
			Expect(event.PullRequest.Merged).To(BeFalse())
		})

		It("should preserve refs/heads/ prefix in branch names", func() {
			payload := `{
				"eventType": "git.pullrequest.created",
				"resource": {
					"pullRequestId": 1,
					"title": "Test",
					"status": "active",
					"mergeStatus": "succeeded",
					"sourceRefName": "refs/heads/my/nested/branch",
					"targetRefName": "refs/heads/target/branch",
					"lastMergeSourceCommit": {
						"commitId": "src123"
					},
					"lastMergeTargetCommit": {
						"commitId": "tgt456"
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Azure parser preserves the full ref name
			Expect(event.PullRequest.Ref).To(Equal("refs/heads/my/nested/branch"))
			Expect(event.PullRequest.BaseRef).To(Equal("refs/heads/target/branch"))
		})
	})

	Context("Unknown Events", func() {
		It("should return ErrUnknownEvent for unsupported eventTypes", func() {
			payload := `{
				"eventType": "workitem.created"
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret Validation", func() {
		It("should allow ValidateSecret to be called", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"oldObjectId": "old123",
							"newObjectId": "new123"
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "azure-secret")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Invalid Payloads", func() {
		It("should handle malformed JSON", func() {
			payload := `{invalid`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
		})

		It("should return error for empty refUpdates array", func() {
			payload := `{
				"eventType": "git.push",
				"resource": {
					"refUpdates": []
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			// Empty refUpdates is treated as unknown event
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})
})
