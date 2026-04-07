package webhookreceiver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
)

var _ = Describe("DetectProvider", func() {
	var wr *webhookreceiver.WebhookReceiver

	BeforeEach(func() {
		wr = &webhookreceiver.WebhookReceiver{}
	})

	tests := map[string]struct {
		headers        map[string]string
		expectedResult string
	}{
		"GitHub webhook with X-GitHub-Event": {
			headers: map[string]string{
				"X-GitHub-Event": "push",
			},
			expectedResult: webhookreceiver.ProviderGitHub,
		},
		"GitHub webhook with X-GitHub-Delivery": {
			headers: map[string]string{
				"X-GitHub-Delivery": "12345",
			},
			expectedResult: webhookreceiver.ProviderGitHub,
		},
		"GitLab webhook with X-Gitlab-Event": {
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
			},
			expectedResult: webhookreceiver.ProviderGitLab,
		},
		"GitLab webhook with X-Gitlab-Token": {
			headers: map[string]string{
				"X-Gitlab-Token": "secret",
			},
			expectedResult: webhookreceiver.ProviderGitLab,
		},
		"Forgejo webhook with X-Forgejo-Event": {
			headers: map[string]string{
				"X-Forgejo-Event": "push",
			},
			expectedResult: webhookreceiver.ProviderForgejo,
		},
		"Gitea webhook with X-Gitea-Event": {
			headers: map[string]string{
				"X-Gitea-Event": "push",
			},
			expectedResult: webhookreceiver.ProviderGitea,
		},
		"Bitbucket Cloud webhook with X-Hook-UUID": {
			headers: map[string]string{
				"X-Hook-UUID": "12345-abcde",
			},
			expectedResult: webhookreceiver.ProviderBitbucketCloud,
		},
		"Unknown provider - no headers": {
			headers:        map[string]string{},
			expectedResult: webhookreceiver.ProviderUnknown,
		},
		"Unknown provider - wrong headers": {
			headers: map[string]string{
				"X-Custom-Header": "value",
			},
			expectedResult: webhookreceiver.ProviderUnknown,
		},
	}

	for name, test := range tests {
		It(name, func() {
			req, err := http.NewRequest(http.MethodPost, "/", nil)
			Expect(err).NotTo(HaveOccurred())

			for key, value := range test.headers {
				req.Header.Set(key, value)
			}

			result := wr.DetectProvider(req)
			Expect(result).To(Equal(test.expectedResult))
		})
	}

	It("should detect GitHub first when multiple provider headers are present", func() {
		req, err := http.NewRequest(http.MethodPost, "/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("X-Github-Event", "push")
		req.Header.Set("X-Gitlab-Event", "Push Hook")

		result := wr.DetectProvider(req)
		Expect(result).To(Equal(webhookreceiver.ProviderGitHub))
	})
})

var _ = Describe("postRoot max payload size enforcement", func() {
	const controllerNamespace = "promoter-system"

	// buildReceiverWithPayloadLimit creates a WebhookReceiver backed by a fake k8s client
	// that includes a ControllerConfiguration with the given maxPayloadBytes limit.
	// Pass limit <= 0 to omit the ControllerConfiguration (testing the default-fallback path).
	buildReceiverWithPayloadLimit := func(maxPayloadBytes int64) *webhookreceiver.WebhookReceiver {
		scheme := utils.GetScheme()
		b := fake.NewClientBuilder().WithScheme(scheme).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, constants.ChangeTransferPolicyProposedHydratedSHAIndexField, func(_ client.Object) []string {
				return nil
			}).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, constants.ChangeTransferPolicyActiveHydratedSHAIndexField, func(_ client.Object) []string {
				return nil
			})

		if maxPayloadBytes > 0 {
			b = b.WithObjects(&promoterv1alpha1.ControllerConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "promoter-controller-configuration",
					Namespace: controllerNamespace,
				},
				Spec: promoterv1alpha1.ControllerConfigurationSpec{
					WebhookReceiver: &promoterv1alpha1.WebhookReceiverConfiguration{
						MaxPayloadBytes: maxPayloadBytes,
					},
				},
			})
		}

		return webhookreceiver.NewWebhookReceiverWithClient(b.Build(), nil, controllerNamespace)
	}

	It("rejects a GitHub push whose body exceeds maxPayloadBytes with 413", func() {
		const limit = 10
		wr := buildReceiverWithPayloadLimit(limit)

		body := strings.Repeat("x", limit+1) // one byte over the limit
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")

		rr := httptest.NewRecorder()
		wr.ServeHTTP(rr, req)

		Expect(rr.Code).To(Equal(http.StatusRequestEntityTooLarge))
	})

	It("accepts a GitHub push whose body is exactly at maxPayloadBytes", func() {
		const limit = 10
		wr := buildReceiverWithPayloadLimit(limit)

		body := strings.Repeat("x", limit) // exactly at the limit
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")

		rr := httptest.NewRecorder()
		wr.ServeHTTP(rr, req)

		// 204 (no matching CTP) means the body was accepted
		Expect(rr.Code).To(Equal(http.StatusNoContent))
	})

	It("uses the default limit (25 MiB) when no ControllerConfiguration is present", func() {
		// No ControllerConfiguration in the fake client; a tiny body should still pass.
		wr := buildReceiverWithPayloadLimit(0)

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-GitHub-Event", "push")

		rr := httptest.NewRecorder()
		wr.ServeHTTP(rr, req)

		// 204 means the body was accepted under the default limit
		Expect(rr.Code).To(Equal(http.StatusNoContent))
	})
})
