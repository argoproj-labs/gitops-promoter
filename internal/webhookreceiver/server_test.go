package webhookreceiver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("DetectProvider", func() {
	var wr *WebhookReceiver

	BeforeEach(func() {
		wr = &WebhookReceiver{}
	})

	tests := map[string]struct {
		headers        map[string]string
		expectedResult string
	}{
		"GitHub webhook with X-GitHub-Event": {
			headers: map[string]string{
				"X-GitHub-Event": "push",
			},
			expectedResult: ProviderGitHub,
		},
		"GitHub webhook with X-GitHub-Delivery": {
			headers: map[string]string{
				"X-GitHub-Delivery": "12345",
			},
			expectedResult: ProviderGitHub,
		},
		"GitLab webhook with X-Gitlab-Event": {
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
			},
			expectedResult: ProviderGitLab,
		},
		"GitLab webhook with X-Gitlab-Token": {
			headers: map[string]string{
				"X-Gitlab-Token": "secret",
			},
			expectedResult: ProviderGitLab,
		},
		"Forgejo webhook with X-Forgejo-Event": {
			headers: map[string]string{
				"X-Forgejo-Event": "push",
			},
			expectedResult: ProviderForgejo,
		},
		"Gitea webhook with X-Gitea-Event": {
			headers: map[string]string{
				"X-Gitea-Event": "push",
			},
			expectedResult: ProviderGitea,
		},
		"Bitbucket Cloud webhook with X-Hook-UUID": {
			headers: map[string]string{
				"X-Hook-UUID": "12345-abcde",
			},
			expectedResult: ProviderBitbucketCloud,
		},
		"Unknown provider - no headers": {
			headers:        map[string]string{},
			expectedResult: ProviderUnknown,
		},
		"Unknown provider - wrong headers": {
			headers: map[string]string{
				"X-Custom-Header": "value",
			},
			expectedResult: ProviderUnknown,
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
		Expect(result).To(Equal(ProviderGitHub))
	})
})

const (
	proposedHydratedShaField = ".status.proposed.hydrated.sha"
	activeHydratedShaField   = ".status.active.hydrated.sha"
	testNamespace            = "default"
	testProposedRef          = "refs/heads/environment/development-next"
	testShaA                 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testShaB                 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testRepoOwner            = "acme"
	testRepoName             = "app"
)

func githubPushPayload(beforeSha, owner, repoName string) []byte {
	payload := map[string]any{
		"before": beforeSha,
		"ref":    testProposedRef,
		"pusher": map[string]any{
			"name":  "test-user",
			"email": "test@example.com",
		},
	}
	if owner != "" || repoName != "" {
		payload["repository"] = map[string]any{
			"name":      repoName,
			"full_name": owner + "/" + repoName,
			"owner": map[string]any{
				"login": owner,
			},
		}
	}
	b, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return b
}

func githubLabelEventPayload(owner, repoName string) []byte {
	payload := map[string]any{
		"action": "labeled",
		"repository": map[string]any{
			"name":      repoName,
			"full_name": owner + "/" + repoName,
			"owner": map[string]any{
				"login": owner,
			},
		},
	}
	b, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return b
}

func newCTP(name string) *promoterv1alpha1.ChangeTransferPolicy {
	return &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Status: promoterv1alpha1.ChangeTransferPolicyStatus{
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: testShaA},
			},
			Active: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: testShaA},
			},
		},
	}
}

func newGitRepo(name, owner, repoName string) *promoterv1alpha1.GitRepository {
	return &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: promoterv1alpha1.GitRepositorySpec{
			GitHub: &promoterv1alpha1.GitHubRepo{
				Owner: owner,
				Name:  repoName,
			},
		},
	}
}

func newPS(name, gitRepoName string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: gitRepoName},
		},
	}
}

func newWRCS(name, psName string) *promoterv1alpha1.WebRequestCommitStatus {
	return &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
			Key:                  "external-approval",
		},
	}
}

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, proposedHydratedShaField, func(obj client.Object) []string {
			ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return nil
			}
			return []string{ctp.Status.Proposed.Hydrated.Sha}
		}).
		WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, activeHydratedShaField, func(obj client.Object) []string {
			ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return nil
			}
			return []string{ctp.Status.Active.Hydrated.Sha}
		}).
		WithIndex(&promoterv1alpha1.GitRepository{}, gitRepositoryRepoKeyField, func(obj client.Object) []string {
			gr, ok := obj.(*promoterv1alpha1.GitRepository)
			if !ok {
				return nil
			}
			if key := utils.GitRepositoryRepoKey(gr); key != "" {
				return []string{key}
			}
			return nil
		}).
		WithIndex(&promoterv1alpha1.PromotionStrategy{}, promotionStrategyGitRepositoryRefField, func(obj client.Object) []string {
			ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
			if !ok {
				return nil
			}
			if ps.Spec.RepositoryReference.Name == "" {
				return nil
			}
			return []string{ps.Spec.RepositoryReference.Name}
		}).
		WithIndex(&promoterv1alpha1.WebRequestCommitStatus{}, promotionStrategyRefField, func(obj client.Object) []string {
			wrcs, ok := obj.(*promoterv1alpha1.WebRequestCommitStatus)
			if !ok {
				return nil
			}
			if wrcs.Spec.PromotionStrategyRef.Name == "" {
				return nil
			}
			return []string{wrcs.Spec.PromotionStrategyRef.Name}
		})
	return builder.Build()
}

func postGitHubPush(wr *WebhookReceiver, beforeSha, owner, repoName string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(githubPushPayload(beforeSha, owner, repoName)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Github-Delivery", "test-delivery")
	rec := httptest.NewRecorder()
	wr.postRoot(rec, req)
	return rec
}

func postGitHubLabelEvent(wr *WebhookReceiver, owner, repoName string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(githubLabelEventPayload(owner, repoName)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "pull_request")
	req.Header.Set("X-Github-Delivery", "test-delivery-label")
	rec := httptest.NewRecorder()
	wr.postRoot(rec, req)
	return rec
}

type enqueueRecorder struct {
	calls [][2]string
	mu    sync.Mutex
}

func (e *enqueueRecorder) enqueue(namespace, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, [2]string{namespace, name})
}

func (e *enqueueRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *enqueueRecorder) last() (namespace, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) == 0 {
		return "", ""
	}
	c := e.calls[len(e.calls)-1]
	return c[0], c[1]
}

func (e *enqueueRecorder) all() [][2]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][2]string, len(e.calls))
	copy(out, e.calls)
	return out
}

// flakyListClient fails List a fixed number of times, then delegates to the wrapped client.
// When failOnlyGitRepositories is true, only GitRepositoryList calls consume failure budget.
// When failOnlyChangeTransferPolicies is true, only ChangeTransferPolicyList calls do.
type flakyListClient struct {
	client.Client
	mu                             sync.Mutex
	failuresLeft                   int
	failOnlyGitRepositories        bool
	failOnlyChangeTransferPolicies bool
}

func (f *flakyListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	f.mu.Lock()
	shouldFail := f.failuresLeft > 0
	if shouldFail && f.failOnlyGitRepositories {
		_, shouldFail = list.(*promoterv1alpha1.GitRepositoryList)
	}
	if shouldFail && f.failOnlyChangeTransferPolicies {
		_, shouldFail = list.(*promoterv1alpha1.ChangeTransferPolicyList)
	}
	if shouldFail {
		f.failuresLeft--
		f.mu.Unlock()
		return errors.New("simulated list failure")
	}
	f.mu.Unlock()
	if err := f.Client.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	return nil
}

// countingGitRepoListClient fails List for GitRepositoryList on the Nth call (1-based).
type countingGitRepoListClient struct {
	client.Client
	mu         sync.Mutex
	calls      int
	failOnCall int
}

func (c *countingGitRepoListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*promoterv1alpha1.GitRepositoryList); ok {
		c.mu.Lock()
		c.calls++
		call := c.calls
		c.mu.Unlock()
		if call == c.failOnCall {
			return errors.New("simulated list failure")
		}
	}
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	return nil
}

func githubHMACSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return sha256Prefix + hex.EncodeToString(mac.Sum(nil))
}

func githubStatusPayload() []byte {
	payload := map[string]any{
		"context": "ArgoCD/app",
		"state":   "success",
		"repository": map[string]any{
			"name":      testRepoName,
			"full_name": testRepoOwner + "/" + testRepoName,
			"owner": map[string]any{
				"login": testRepoOwner,
			},
		},
	}
	b, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return b
}

func newScmProviderWithSecret(scmName, secretName string, secretData map[string][]byte) (*promoterv1alpha1.ScmProvider, *corev1.Secret) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: testNamespace,
		},
		Data: secretData,
	}
	scm := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scmName,
			Namespace: testNamespace,
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &corev1.LocalObjectReference{Name: secretName},
			GitHub:    &promoterv1alpha1.GitHub{AppID: 1},
		},
	}
	return scm, secret
}

func newGitRepoWithScm(name, scmName string) *promoterv1alpha1.GitRepository {
	gr := newGitRepo(name, testRepoOwner, testRepoName)
	gr.Spec.ScmProviderRef = promoterv1alpha1.ScmProviderObjectReference{
		Name: scmName,
		Kind: promoterv1alpha1.ScmProviderKind,
	}
	return gr
}

func postGitHubWithHeaders(wr *WebhookReceiver, body []byte, extraHeaders map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "status")
	req.Header.Set("X-Github-Delivery", "test-delivery-status")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	wr.postRoot(rec, req)
	return rec
}

var _ = Describe("WebhookReceiver signature verification", func() {
	const webhookSecretValue = "whsec_test_secret"

	It("accepts when no webhookSecret is configured (backward compatible)", func() {
		scm, secret := newScmProviderWithSecret("scm-nosig", "sec-nosig", map[string][]byte{
			"token": []byte("scm-token"),
		})
		gitRepo := newGitRepoWithScm("gr-nosig", scm.Name)
		ps := newPS("ps-nosig", gitRepo.Name)
		wrcs := newWRCS("wrcs-nosig", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:           newFakeClient(scm, secret, gitRepo, ps, wrcs),
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, nil)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(wrcsEnqueues.count()).To(Equal(1))
	})

	It("rejects with 401 when webhookSecret is set but signature is missing", func() {
		scm, secret := newScmProviderWithSecret("scm-miss", "sec-miss", map[string][]byte{
			"token": []byte("scm-token"),
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte(webhookSecretValue),
		})
		gitRepo := newGitRepoWithScm("gr-miss", scm.Name)
		ps := newPS("ps-miss", gitRepo.Name)
		wrcs := newWRCS("wrcs-miss", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:           newFakeClient(scm, secret, gitRepo, ps, wrcs),
			enqueueCTP:          ctpEnqueues.enqueue,
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, nil)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(ctpEnqueues.count()).To(Equal(0))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("rejects with 401 when signature is invalid", func() {
		scm, secret := newScmProviderWithSecret("scm-bad", "sec-bad", map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte(webhookSecretValue),
		})
		gitRepo := newGitRepoWithScm("gr-bad", scm.Name)
		ps := newPS("ps-bad", gitRepo.Name)
		wrcs := newWRCS("wrcs-bad", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:           newFakeClient(scm, secret, gitRepo, ps, wrcs),
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, map[string]string{
			"X-Hub-Signature-256": sha256Prefix + "0000000000000000000000000000000000000000000000000000000000000000",
		})
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("accepts and enqueues when signature is valid", func() {
		scm, secret := newScmProviderWithSecret("scm-ok", "sec-ok", map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte(webhookSecretValue),
		})
		gitRepo := newGitRepoWithScm("gr-ok", scm.Name)
		ps := newPS("ps-ok", gitRepo.Name)
		wrcs := newWRCS("wrcs-ok", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:           newFakeClient(scm, secret, gitRepo, ps, wrcs),
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, map[string]string{
			"X-Hub-Signature-256": githubHMACSignature([]byte(webhookSecretValue), body),
		})
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(wrcsEnqueues.count()).To(Equal(1))
	})

	It("rejects with 500 when ScmProvider Secret cannot be resolved for matching GitRepositories", func() {
		scm, _ := newScmProviderWithSecret("scm-missing-sec", "sec-missing", map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte(webhookSecretValue),
		})
		gitRepo := newGitRepoWithScm("gr-missing-sec", scm.Name)
		ps := newPS("ps-missing-sec", gitRepo.Name)
		wrcs := newWRCS("wrcs-missing-sec", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			// ScmProvider is present but its Secret is omitted so resolution fails.
			k8sClient:           newFakeClient(scm, gitRepo, ps, wrcs),
			enqueueCTP:          ctpEnqueues.enqueue,
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, nil)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(ctpEnqueues.count()).To(Equal(0))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("rejects with 401 and enqueues neither CTP nor WRCS when payload lacks repository identity and webhookSecret is configured", func() {
		scm, secret := newScmProviderWithSecret("scm-noid", "sec-noid", map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte(webhookSecretValue),
		})
		ctp := newCTP("ctp-noid")
		gitRepo := newGitRepoWithScm("gr-noid", scm.Name)
		ps := newPS("ps-noid", gitRepo.Name)
		wrcs := newWRCS("wrcs-noid", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:           newFakeClient(scm, secret, ctp, gitRepo, ps, wrcs),
			enqueueCTP:          ctpEnqueues.enqueue,
			enqueueWRCS:         wrcsEnqueues.enqueue,
			controllerNamespace: testNamespace,
		}

		// Push SHA matches the CTP, but without repository identity verification cannot run.
		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(ctpEnqueues.count()).To(Equal(0))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("allows CTP enqueue without repository identity when no webhookSecret is configured", func() {
		ctp := newCTP("ctp-noid-compat")
		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(ctp),
			enqueueCTP:  ctpEnqueues.enqueue,
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctpEnqueues.count()).To(Equal(1))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})
})

var _ = Describe("WebhookReceiver WRCS filter", func() {
	It("enqueues only WRCS whose webhook filter matches Payload", func() {
		gitRepo := newGitRepo("gr-filter", testRepoOwner, testRepoName)
		ps := newPS("ps-filter", gitRepo.Name)
		matching := newWRCS("wrcs-match", ps.Name)
		matching.Spec.Mode = promoterv1alpha1.ModeSpec{
			Polling: &promoterv1alpha1.PollingModeSpec{},
			Webhook: &promoterv1alpha1.WebhookModeSpec{
				Filter: &promoterv1alpha1.WebhookFilterSpec{
					Expression: `Payload.context startsWith "ArgoCD/"`,
				},
			},
		}
		nonMatching := newWRCS("wrcs-skip", ps.Name)
		nonMatching.Spec.Mode = promoterv1alpha1.ModeSpec{
			Polling: &promoterv1alpha1.PollingModeSpec{},
			Webhook: &promoterv1alpha1.WebhookModeSpec{
				Filter: &promoterv1alpha1.WebhookFilterSpec{
					Expression: `Payload.context startsWith "CI/"`,
				},
			},
		}
		noFilter := newWRCS("wrcs-all", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(gitRepo, ps, matching, nonMatching, noFilter),
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		body := githubStatusPayload()
		rec := postGitHubWithHeaders(wr, body, nil)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(wrcsEnqueues.all()).To(ConsistOf(
			[2]string{testNamespace, "wrcs-match"},
			[2]string{testNamespace, "wrcs-all"},
		))
	})
})

type testLifecycle struct {
	ctx  context.Context //nolint:containedctx // stands in for the server-lifetime context passed to Start
	stop func()
}

func newTestLifecycle() testLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return testLifecycle{
		ctx:  ctx,
		stop: cancel,
	}
}

var _ = Describe("WebhookReceiver miss retry", func() {
	var lc testLifecycle

	BeforeEach(func() {
		lc = newTestLifecycle()
		metrics.WebhookMissRetryPending.Set(0)
	})

	AfterEach(func() {
		lc.stop()
		// Allow in-flight retries to release the pending counter/gauge after cancel.
		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("enqueues immediately when a CTP matches on the first lookup", func() {
		ctp := newCTP("ctp-match")
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:    lc.ctx,
			k8sClient:  newFakeClient(ctp),
			enqueueCTP: enqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-match"))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0))
	})

	It("retries asynchronously and enqueues when a CTP appears after the initial miss", func() {
		enqueues := &enqueueRecorder{}
		cl := newFakeClient()
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      cl,
			enqueueCTP:     enqueues.enqueue,
			retryTimeout:   2 * time.Second,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  50 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(1.0))

		ctp := newCTP("ctp-late")
		Expect(cl.Create(context.Background(), ctp)).To(Succeed())

		Eventually(func() int {
			return enqueues.count()
		}, 2*time.Second, 20*time.Millisecond).Should(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-late"))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("does not enqueue when no CTP appears before the retry timeout", func() {
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      newFakeClient(),
			enqueueCTP:     enqueues.enqueue,
			retryTimeout:   150 * time.Millisecond,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  40 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))

		Consistently(func() int {
			return enqueues.count()
		}, 250*time.Millisecond, 20*time.Millisecond).Should(Equal(0))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("does not enqueue or async-retry when multiple CTPs match the same sha", func() {
		ctp1 := newCTP("ctp-one")
		ctp1.Status.Active.Hydrated.Sha = testShaB
		ctp2 := newCTP("ctp-two")
		ctp2.Status.Active.Hydrated.Sha = testShaB
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      newFakeClient(ctp1, ctp2),
			enqueueCTP:     enqueues.enqueue,
			retryTimeout:   200 * time.Millisecond,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  40 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0))

		Consistently(func() int {
			return enqueues.count()
		}, 150*time.Millisecond, 20*time.Millisecond).Should(Equal(0))
	})

	It("retries after transient list failures and enqueues when lookup succeeds", func() {
		ctp := newCTP("ctp-flaky")
		enqueues := &enqueueRecorder{}
		// Fail the sync lookup and the first deferred attempt, then succeed.
		flaky := &flakyListClient{
			Client:                         newFakeClient(ctp),
			failuresLeft:                   2,
			failOnlyChangeTransferPolicies: true,
		}
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      flaky,
			enqueueCTP:     enqueues.enqueue,
			retryTimeout:   2 * time.Second,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  50 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA, testRepoOwner, testRepoName)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))

		Eventually(func() int {
			return enqueues.count()
		}, 2*time.Second, 20*time.Millisecond).Should(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-flaky"))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("returns 204 and drops additional retries when pending miss retries are at capacity", func() {
		enqueues := &enqueueRecorder{}
		// Client that never matches so retries stay in flight until cancelled/timeout.
		wr := &WebhookReceiver{
			baseCtx:           lc.ctx,
			k8sClient:         newFakeClient(),
			enqueueCTP:        enqueues.enqueue,
			maxPendingRetries: 1,
			retryTimeout:      5 * time.Second,
			retryBaseDelay:    50 * time.Millisecond,
			retryMaxDelay:     100 * time.Millisecond,
			retryFactor:       2.0,
		}

		rec1 := postGitHubPush(wr, testShaA, "", "")
		Expect(rec1.Code).To(Equal(http.StatusNoContent))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(1.0))

		rec2 := postGitHubPush(wr, testShaB, "", "")
		Expect(rec2.Code).To(Equal(http.StatusNoContent))
		// Still only one in-flight retry; second was dropped.
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(1.0))
		Expect(enqueues.count()).To(Equal(0))
	})
})

var _ = Describe("WebhookReceiver WRCS repo fan-out", func() {
	It("enqueues matching WRCS resources on a push webhook without affecting the CTP path", func() {
		ctp := newCTP("ctp-match")
		gitRepo := newGitRepo("gr-match", testRepoOwner, testRepoName)
		ps := newPS("ps-match", gitRepo.Name)
		wrcs1 := newWRCS("wrcs-one", ps.Name)
		wrcs2 := newWRCS("wrcs-two", ps.Name)

		unrelatedRepo := newGitRepo("gr-other", "other", "repo")
		unrelatedPS := newPS("ps-other", unrelatedRepo.Name)
		unrelatedWRCS := newWRCS("wrcs-other", unrelatedPS.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(ctp, gitRepo, ps, wrcs1, wrcs2, unrelatedRepo, unrelatedPS, unrelatedWRCS),
			enqueueCTP:  ctpEnqueues.enqueue,
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA, testRepoOwner, testRepoName)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctpEnqueues.count()).To(Equal(1))
		Expect(wrcsEnqueues.count()).To(Equal(2))
		Expect(wrcsEnqueues.all()).To(ConsistOf(
			[2]string{testNamespace, "wrcs-one"},
			[2]string{testNamespace, "wrcs-two"},
		))
	})

	It("enqueues WRCS on a non-push event with no before SHA", func() {
		gitRepo := newGitRepo("gr-label", testRepoOwner, testRepoName)
		ps := newPS("ps-label", gitRepo.Name)
		wrcs := newWRCS("wrcs-label", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(gitRepo, ps, wrcs),
			enqueueCTP:  ctpEnqueues.enqueue,
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubLabelEvent(wr, testRepoOwner, testRepoName)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctpEnqueues.count()).To(Equal(0))
		Expect(wrcsEnqueues.count()).To(Equal(1))
		ns, name := wrcsEnqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("wrcs-label"))
	})

	It("matches repository identity case-insensitively", func() {
		gitRepo := newGitRepo("gr-case", "acme", "app")
		ps := newPS("ps-case", gitRepo.Name)
		wrcs := newWRCS("wrcs-case", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(gitRepo, ps, wrcs),
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubLabelEvent(wr, "Acme", "App")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(wrcsEnqueues.count()).To(Equal(1))
	})

	It("skips fan-out when the payload has no repository identity", func() {
		gitRepo := newGitRepo("gr-norepo", testRepoOwner, testRepoName)
		ps := newPS("ps-norepo", gitRepo.Name)
		wrcs := newWRCS("wrcs-norepo", ps.Name)

		wrcsEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:   newFakeClient(gitRepo, ps, wrcs),
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubLabelEvent(wr, "", "")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("does not panic when enqueueWRCS is nil", func() {
		ctp := newCTP("ctp-nil-wrcs")
		gitRepo := newGitRepo("gr-nil-wrcs", testRepoOwner, testRepoName)
		ps := newPS("ps-nil-wrcs", gitRepo.Name)
		wrcs := newWRCS("wrcs-nil", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			k8sClient:  newFakeClient(ctp, gitRepo, ps, wrcs),
			enqueueCTP: ctpEnqueues.enqueue,
		}

		Expect(func() {
			rec := postGitHubPush(wr, testShaA, testRepoOwner, testRepoName)
			Expect(rec.Code).To(Equal(http.StatusNoContent))
		}).NotTo(Panic())
		Expect(ctpEnqueues.count()).To(Equal(1))
	})

	It("returns 500 when GitRepository list fails during webhook verification", func() {
		ctp := newCTP("ctp-flaky-gr")
		gitRepo := newGitRepo("gr-flaky", testRepoOwner, testRepoName)
		ps := newPS("ps-flaky", gitRepo.Name)
		wrcs := newWRCS("wrcs-flaky", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		flaky := &flakyListClient{
			Client:                  newFakeClient(ctp, gitRepo, ps, wrcs),
			failuresLeft:            1,
			failOnlyGitRepositories: true,
		}
		wr := &WebhookReceiver{
			k8sClient:   flaky,
			enqueueCTP:  ctpEnqueues.enqueue,
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA, testRepoOwner, testRepoName)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(ctpEnqueues.count()).To(Equal(0))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})

	It("still enqueues CTP when WRCS fan-out GitRepository list fails after verification", func() {
		ctp := newCTP("ctp-flaky-gr2")
		gitRepo := newGitRepo("gr-flaky2", testRepoOwner, testRepoName)
		ps := newPS("ps-flaky2", gitRepo.Name)
		wrcs := newWRCS("wrcs-flaky2", ps.Name)

		ctpEnqueues := &enqueueRecorder{}
		wrcsEnqueues := &enqueueRecorder{}
		// verification lists GitRepositories once (success); fan-out lists again (fail).
		flaky := &flakyListClient{
			Client:                  newFakeClient(ctp, gitRepo, ps, wrcs),
			failuresLeft:            0,
			failOnlyGitRepositories: true,
		}
		// Fail only the second GitRepository list by wrapping after a successful first call.
		counting := &countingGitRepoListClient{Client: flaky.Client, failOnCall: 2}
		wr := &WebhookReceiver{
			k8sClient:   counting,
			enqueueCTP:  ctpEnqueues.enqueue,
			enqueueWRCS: wrcsEnqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA, testRepoOwner, testRepoName)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctpEnqueues.count()).To(Equal(1))
		Expect(wrcsEnqueues.count()).To(Equal(0))
	})
})

var _ = Describe("parseWebhookRepo", func() {
	DescribeTable("extracts owner and name per provider",
		func(provider string, payload map[string]any, wantOwner, wantName string) {
			b, err := json.Marshal(payload)
			Expect(err).NotTo(HaveOccurred())
			owner, name := parseWebhookRepo(provider, b)
			Expect(owner).To(Equal(wantOwner))
			Expect(name).To(Equal(wantName))
		},
		Entry("GitHub owner.login + name", ProviderGitHub, map[string]any{
			"repository": map[string]any{
				"name": "app",
				"owner": map[string]any{
					"login": "acme",
				},
			},
		}, "acme", "app"),
		Entry("GitHub full_name fallback", ProviderGitHub, map[string]any{
			"repository": map[string]any{
				"full_name": "acme/app",
			},
		}, "acme", "app"),
		Entry("Forgejo owner.username fallback", ProviderForgejo, map[string]any{
			"repository": map[string]any{
				"name": "app",
				"owner": map[string]any{
					"username": "acme",
				},
			},
		}, "acme", "app"),
		Entry("Gitea owner.username fallback", ProviderGitea, map[string]any{
			"repository": map[string]any{
				"name": "app",
				"owner": map[string]any{
					"username": "acme",
				},
			},
		}, "acme", "app"),
		Entry("GitLab path_with_namespace", ProviderGitLab, map[string]any{
			"project": map[string]any{
				"path_with_namespace": "group/subgroup/app",
			},
		}, "group/subgroup", "app"),
		Entry("Bitbucket Cloud full_name", ProviderBitbucketCloud, map[string]any{
			"repository": map[string]any{
				"full_name": "workspace/app",
			},
		}, "workspace", "app"),
		Entry("Azure DevOps project + name", ProviderAzureDevops, map[string]any{
			"resource": map[string]any{
				"repository": map[string]any{
					"name": "app",
					"project": map[string]any{
						"name": "MyProject",
					},
				},
			},
		}, "MyProject", "app"),
		Entry("missing repository fields", ProviderGitHub, map[string]any{
			"action": "labeled",
		}, "", ""),
	)
})
