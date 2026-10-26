package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// clientWithSCMRedirectPolicy follows a redirect only while it stays on the allowed host. These run
// against plain httptest servers, without the envtest cluster.
var _ = Describe("clientWithSCMRedirectPolicy", func() {
	It("follows a redirect on the allowed host and refuses one to another host", func() {
		var otherHits atomic.Int32
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			otherHits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(other.Close)

		var scm *httptest.Server
		scm = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/same-host":
				http.Redirect(w, r, scm.URL+"/final", http.StatusFound)
			case "/cross-host":
				http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		DeferCleanup(scm.Close)

		scmURL, err := url.Parse(scm.URL)
		Expect(err).NotTo(HaveOccurred())
		c := clientWithSCMRedirectPolicy(&http.Client{Timeout: 5 * time.Second}, scmURL.Host)

		resp, err := c.Get(scm.URL + "/same-host")
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Body.Close()).To(Succeed())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Request.URL.Path).To(Equal("/final"))

		resp, err = c.Get(scm.URL + "/cross-host")
		if resp != nil {
			_ = resp.Body.Close()
		}
		Expect(err).To(MatchError(ContainSubstring("refusing to follow redirect")))
		Expect(otherHits.Load()).To(BeZero(), "the other host must never receive a request")
	})

	It("leaves the base client untouched", func() {
		base := &http.Client{Timeout: 7 * time.Second}
		c := clientWithSCMRedirectPolicy(base, "example.com")
		Expect(base.CheckRedirect).To(BeNil())
		Expect(c.CheckRedirect).NotTo(BeNil())
		Expect(c.Timeout).To(Equal(base.Timeout))
	})
})

// With authentication.scm, a redirect from the SCM host to another host must not be followed: the
// request fails and the other host never sees the SCM credentials.
var _ = Describe("WebRequestCommitStatus Controller - SCM redirect policy", func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		scmServer         *httptest.Server
		otherServer       *httptest.Server
		otherHits         atomic.Int32
		wrcs              *promoterv1alpha1.WebRequestCommitStatus
	)

	Context("when the SCM host redirects", Label(fakeSCMOnlyLabel), func() {
		BeforeEach(func() {
			ctx = context.Background()
			otherHits.Store(0)
			otherServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				otherHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))
			scmServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cross-host":
					http.Redirect(w, r, otherServer.URL+"/steal", http.StatusFound)
				case "/same-host":
					http.Redirect(w, r, scmServer.URL+"/final", http.StatusFound)
				default:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
				}
			}))

			serverParsed, err := url.Parse(scmServer.URL)
			Expect(err).NotTo(HaveOccurred())

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-scm-redirect", "default")
			scmProvider.Spec.Fake = &promoterv1alpha1.Fake{Domain: serverParsed.Host}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{{Branch: testBranchDevelopment}}
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: "scm-redirect"}}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			declareDependentsSuccessfulGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createDependentsSuccessfulCommitStatus(ctx, promotionStrategy)
		})

		AfterEach(func() {
			for _, obj := range []client.Object{wrcs, promotionStrategy, gitRepo, scmProvider, scmSecret} {
				if obj != nil {
					_ = client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
				}
			}
			wrcs, promotionStrategy, gitRepo, scmProvider, scmSecret = nil, nil, nil, nil, nil
			if scmServer != nil {
				scmServer.Close()
				scmServer = nil
			}
			if otherServer != nil {
				otherServer.Close()
				otherServer = nil
			}
		})

		newWRCS := func(suffix, path string) *promoterv1alpha1.WebRequestCommitStatus {
			return &promoterv1alpha1.WebRequestCommitStatus{
				Name:      name + suffix,
				Namespace: "default",
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "scm-redirect",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    scmServer.URL + path,
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{Scm: &promoterv1alpha1.Scm{}},
					},
					Success: promoterv1alpha1.SuccessSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
						},
					},
					Mode: promoterv1alpha1.ModeSpec{
						Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 5 * time.Second}},
					},
				},
			}
		}

		It("refuses a redirect to another host and never contacts it", func() {
			wrcs = newWRCS("-cross", "/cross-host")
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wrcs), &fetched)).To(Succeed())
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("refusing to follow redirect"))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(otherHits.Load()).To(BeZero(), "the other host must never receive the SCM-authenticated request")
		})

		It("follows a redirect that stays on the SCM host", func() {
			wrcs = newWRCS("-same", "/same-host")
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())

			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wrcs), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Environments).To(HaveLen(1))
				g.Expect(fetched.Status.Environments[0].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
