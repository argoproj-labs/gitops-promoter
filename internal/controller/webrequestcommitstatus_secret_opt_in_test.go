package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Secret opt-in tests: a Secret named by httpRequest.authentication must carry
// promoter.argoproj.io/allow-webrequest=true before the controller reads it. Each test uses unique resources (via promotionStrategyResource).
var _ = Describe("WebRequestCommitStatus Controller - Secret opt-in", func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *corev1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		testServer        *httptest.Server
		wrcs              *promoterv1alpha1.WebRequestCommitStatus
		authSecret        *corev1.Secret
	)

	Context("when the referenced Secret lacks the opt-in label", Label(fakeSCMOnlyLabel), func() {
		BeforeEach(func() {
			ctx = context.Background()
			testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
			}))

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "wrcs-secret-opt-in", "default")
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{{Branch: testBranchDevelopment}}
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: "secret-opt-in"}}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			declareDependentsSuccessfulGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createDependentsSuccessfulCommitStatus(ctx, promotionStrategy)

			authSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-bearer", Namespace: "default"},
				StringData: map[string]string{"token": "bearer-token"},
			}
			Expect(k8sClient.Create(ctx, authSecret)).To(Succeed())

			wrcs = &promoterv1alpha1.WebRequestCommitStatus{
				Name:      name + "-opt-in",
				Namespace: "default",
				Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					Key:                  "secret-opt-in",
					ReportOn:             constants.CommitRefProposed,
					HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
						URLTemplate:    testServer.URL + "/validate",
						MethodTemplate: "GET",
						Timeout:        metav1.Duration{Duration: 10 * time.Second},
						Authentication: &promoterv1alpha1.HTTPAuthentication{
							Bearer: &promoterv1alpha1.BearerAuth{SecretRef: &corev1.LocalObjectReference{Name: authSecret.Name}},
						},
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
			Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
				testServer = nil
			}
			for _, obj := range []client.Object{wrcs, promotionStrategy, gitRepo, scmProvider, scmSecret, authSecret} {
				if obj != nil {
					_ = client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
				}
			}
			wrcs, promotionStrategy, gitRepo, scmProvider, scmSecret, authSecret = nil, nil, nil, nil, nil, nil
		})

		It("refuses an unlabeled Secret and proceeds once the Secret is labeled", func() {
			By("Expecting the WRCS to report Ready=False naming the opt-in label")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wrcs), &fetched)).To(Succeed())
				ready := meta.FindStatusCondition(fetched.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring(promoterv1alpha1.WebRequestSecretOptInLabel))
				g.Expect(fetched.Status.Environments).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Labeling the Secret")
			Eventually(func(g Gomega) {
				var s corev1.Secret
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(authSecret), &s)).To(Succeed())
				if s.Labels == nil {
					s.Labels = map[string]string{}
				}
				s.Labels[promoterv1alpha1.WebRequestSecretOptInLabel] = promoterv1alpha1.WebRequestSecretOptInLabelValue
				g.Expect(k8sClient.Update(ctx, &s)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Bumping the WRCS spec so it reconciles now instead of after the failure backoff")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wrcs), &fetched)).To(Succeed())
				fetched.Spec.HTTPRequest.Timeout = metav1.Duration{Duration: 11 * time.Second}
				g.Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Expecting the WRCS to make the request and reach success")
			Eventually(func(g Gomega) {
				var fetched promoterv1alpha1.WebRequestCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wrcs), &fetched)).To(Succeed())
				g.Expect(fetched.Status.Environments).To(HaveLen(1))
				g.Expect(fetched.Status.Environments[0].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
