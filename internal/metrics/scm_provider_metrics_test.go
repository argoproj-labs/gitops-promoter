package metrics

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("RecordSCMCall", func() {
	It("records scm_calls_total with empty git_repository for provider-only scope", func() {
		provider := &promoterv1alpha1.ClusterScmProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "scm-provider-metrics-test"},
		}
		RecordSCMCall(context.Background(), provider, SCMAPIProvider, GitHubSCMProviderOperationListInstallations, 200, 50*time.Millisecond, nil)
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues("", "scm-provider-metrics-test", "ClusterScmProvider", "Provider", "list-installations", "200"))).To(Equal(1.0))

		RecordSCMCall(context.Background(), provider, SCMAPIProvider, GitHubSCMProviderOperationListInstallations, 200, 25*time.Millisecond, nil)
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues("", "scm-provider-metrics-test", "ClusterScmProvider", "Provider", "list-installations", "200"))).To(Equal(2.0))
	})

	It("keeps GitHub rate-limit gauges separate per repository owner", func() {
		providerRef := promoterv1alpha1.ScmProviderObjectReference{Kind: "ClusterScmProvider", Name: "scm-account-metrics-test"}
		repo := func(owner string) *promoterv1alpha1.GitRepository {
			return &promoterv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: owner + "-repo", Namespace: "default"},
				Spec: promoterv1alpha1.GitRepositorySpec{
					ScmProviderRef: providerRef,
					GitHub:         &promoterv1alpha1.GitHubRepo{Owner: owner, Name: "app"},
				},
			}
		}

		RecordSCMCall(context.Background(), repo("org-a"), SCMAPIPullRequest, SCMOperationList, 200, time.Millisecond, &RateLimit{
			Limit: 5000, Remaining: 100, ResetRemaining: time.Minute,
		})
		RecordSCMCall(context.Background(), repo("org-b"), SCMAPIPullRequest, SCMOperationList, 200, time.Millisecond, &RateLimit{
			Limit: 5000, Remaining: 4900, ResetRemaining: time.Minute,
		})

		Expect(testutil.ToFloat64(scmCallsRateLimitRemaining.WithLabelValues("scm-account-metrics-test", "ClusterScmProvider", "org-a"))).To(Equal(100.0))
		Expect(testutil.ToFloat64(scmCallsRateLimitRemaining.WithLabelValues("scm-account-metrics-test", "ClusterScmProvider", "org-b"))).To(Equal(4900.0))
		Expect(testutil.ToFloat64(scmCallsRateLimitLimit.WithLabelValues("scm-account-metrics-test", "ClusterScmProvider", "org-a"))).To(Equal(5000.0))
	})

	It("leaves scm_account empty when the provider credential is the rate-limit bucket", func() {
		repo := &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "gl-repo", Namespace: "default"},
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Name: "scm-account-gitlab-test"},
				GitLab:         &promoterv1alpha1.GitLabRepo{Namespace: "group/sub", Name: "app", ProjectID: 1},
			},
		}
		RecordSCMCall(context.Background(), repo, SCMAPIPullRequest, SCMOperationList, 200, time.Millisecond, &RateLimit{
			Limit: 2000, Remaining: 1500, ResetRemaining: time.Minute,
		})
		Expect(testutil.ToFloat64(scmCallsRateLimitRemaining.WithLabelValues("scm-account-gitlab-test", "ScmProvider", ""))).To(Equal(1500.0))
	})
})
