package webhooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver/webhooks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BitbucketParser", func() {
	var parser webhooks.BitbucketParser

	BeforeEach(func() {
		parser = webhooks.BitbucketParser{}
	})

	Context("Push Events", func() {
		It("should parse repo:push event with single change", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "def456abc789"
								}
							},
							"old": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "abc123def456"
								}
							}
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePush))
			Expect(event.Push).NotTo(BeNil())
			Expect(event.Push.Provider).To(Equal("bitbucket"))
			Expect(event.Push.Ref).To(Equal("refs/heads/main"))
			Expect(event.Push.Before).To(Equal("abc123def456"))
			Expect(event.Push.After).To(Equal("def456abc789"))
		})

		It("should handle multiple changes and use the first one", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": {
								"name": "feature1",
								"type": "branch",
								"target": {
									"hash": "hash1"
								}
							},
							"old": {
								"name": "feature1",
								"type": "branch",
								"target": {
									"hash": "oldhash1"
								}
							}
						},
						{
							"new": {
								"name": "feature2",
								"type": "branch",
								"target": {
									"hash": "hash2"
								}
							},
							"old": {
								"name": "feature2",
								"type": "branch",
								"target": {
									"hash": "oldhash2"
								}
							}
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Should use first change
			Expect(event.Push.Ref).To(Equal("refs/heads/feature1"))
			Expect(event.Push.After).To(Equal("hash1"))
			Expect(event.Push.Before).To(Equal("oldhash1"))
		})

		It("should handle tag push by treating it as branch", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": {
								"name": "v1.0.0",
								"type": "tag",
								"target": {
									"hash": "tag123"
								}
							},
							"old": null
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			// Bitbucket parser treats all refs as branches
			Expect(event.Push.Ref).To(Equal("refs/heads/v1.0.0"))
			Expect(event.Push.After).To(Equal("tag123"))
		})

		It("should handle branch deletion (new is null)", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": null,
							"old": {
								"name": "deleted-branch",
								"type": "branch",
								"target": {
									"hash": "old123"
								}
							}
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.Push.Ref).To(Equal("refs/heads/deleted-branch"))
			Expect(event.Push.Before).To(Equal("old123"))
		})
	})

	Context("Pull Request Events", func() {
		It("should parse PR created event", func() {
			payload := `{
				"pullrequest": {
					"id": 42,
					"title": "Add new feature",
					"state": "OPEN",
					"source": {
						"branch": {
							"name": "feature-branch"
						},
						"commit": {
							"hash": "abc123"
						}
					},
					"destination": {
						"branch": {
							"name": "main"
						},
						"commit": {
							"hash": "def456"
						}
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "pullrequest:created")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())
			Expect(event.Type).To(Equal(webhooks.EventTypePullRequest))
			Expect(event.PullRequest).NotTo(BeNil())

			pr := event.PullRequest
			Expect(pr.Provider).To(Equal("bitbucket"))
			Expect(pr.Action).To(Equal("opened"))
			Expect(pr.ID).To(Equal(42))
			Expect(pr.Title).To(Equal("Add new feature"))
			Expect(pr.Ref).To(Equal("feature-branch"))
			Expect(pr.SHA).To(Equal("abc123"))
			Expect(pr.BaseRef).To(Equal("main"))
			Expect(pr.BaseSHA).To(Equal("def456"))
			Expect(pr.Merged).To(BeFalse())
		})

		It("should parse PR updated event", func() {
			payload := `{
				"pullrequest": {
					"id": 99,
					"title": "Updated PR",
					"state": "OPEN",
					"source": {
						"branch": {
							"name": "update"
						},
						"commit": {
							"hash": "update123"
						}
					},
					"destination": {
						"branch": {
							"name": "main"
						},
						"commit": {
							"hash": "main456"
						}
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "pullrequest:updated")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("synchronize"))
			Expect(event.PullRequest.ID).To(Equal(99))
		})

		It("should parse PR fulfilled (merged) event", func() {
			payload := `{
				"pullrequest": {
					"id": 50,
					"title": "Merged PR",
					"state": "MERGED",
					"source": {
						"branch": {
							"name": "feature"
						},
						"commit": {
							"hash": "feature123"
						}
					},
					"destination": {
						"branch": {
							"name": "main"
						},
						"commit": {
							"hash": "main789"
						}
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "pullrequest:fulfilled")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("closed"))
			Expect(event.PullRequest.Merged).To(BeTrue())
		})

		It("should parse PR rejected event", func() {
			payload := `{
				"pullrequest": {
					"id": 60,
					"title": "Rejected PR",
					"state": "DECLINED",
					"source": {
						"branch": {
							"name": "bad-feature"
						},
						"commit": {
							"hash": "bad123"
						}
					},
					"destination": {
						"branch": {
							"name": "main"
						},
						"commit": {
							"hash": "main999"
						}
					}
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "pullrequest:rejected")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event.PullRequest.Action).To(Equal("closed"))
			Expect(event.PullRequest.Merged).To(BeFalse())
		})
	})

	Context("Unknown Events", func() {
		It("should return ErrUnknownEvent for unsupported events", func() {
			payload := `{}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "issue:created") // Unsupported

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Secret Validation", func() {
		It("should allow ValidateSecret to be called", func() {
			payload := `{
				"push": {
					"changes": [
						{
							"new": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "new123"
								}
							},
							"old": {
								"name": "main",
								"type": "branch",
								"target": {
									"hash": "old123"
								}
							}
						}
					]
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			event, err := parser.Parse(req)

			Expect(err).NotTo(HaveOccurred())
			Expect(event).NotTo(BeNil())

			// Validate with a secret (not implemented yet, but shouldn't error)
			err = parser.ValidateSecret(req, event, "bitbucket-secret")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Invalid Payloads", func() {
		It("should handle malformed JSON", func() {
			payload := `{invalid`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
		})

		It("should return error for empty changes array", func() {
			payload := `{
				"push": {
					"changes": []
				}
			}`

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
			req.Header.Set("X-Event-Key", "repo:push")

			_, err := parser.Parse(req)

			Expect(err).To(HaveOccurred())
			// Empty changes is treated as unknown event
			_, ok := err.(webhooks.ErrUnknownEvent)
			Expect(ok).To(BeTrue())
		})
	})
})
