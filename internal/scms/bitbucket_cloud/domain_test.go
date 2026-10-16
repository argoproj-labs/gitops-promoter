package bitbucket_cloud

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"

	"github.com/ktrysmt/go-bitbucket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const testNamespace = "default"

func bitbucketScmProvider(domain string) *v1alpha1.ScmProvider {
	return &v1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "bb", Namespace: testNamespace},
		Spec: v1alpha1.ScmProviderSpec{
			SecretRef:      &corev1.LocalObjectReference{Name: "bb"},
			BitbucketCloud: &v1alpha1.BitbucketCloud{Domain: domain},
		},
	}
}

func bitbucketGitRepository() *v1alpha1.GitRepository {
	return &v1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "bb-repo", Namespace: testNamespace},
		Spec: v1alpha1.GitRepositorySpec{
			BitbucketCloud: &v1alpha1.BitbucketCloudRepo{Owner: "acme", Name: "widgets"},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: v1alpha1.ScmProviderKind, Name: "bb"},
		},
	}
}

func bitbucketSecret() corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bb", Namespace: testNamespace},
		Data:       map[string][]byte{"token": []byte("bb-token")},
	}
}

var _ = Describe("Bitbucket Cloud domain override", func() {
	DescribeTable("builds web and git URLs",
		func(domain, wantWeb, wantClone, wantCommit string) {
			Expect(webBaseURL(domain)).To(Equal(wantWeb))

			secret := bitbucketSecret()
			gap, err := NewBitbucketCloudGitAuthenticationProvider(bitbucketScmProvider(domain), &secret)
			Expect(err).NotTo(HaveOccurred())
			Expect(gap.GetGitHttpsRepoUrl(*bitbucketGitRepository())).To(Equal(wantClone))

			Expect(createCommitURL(domain, bitbucketGitRepository(), "abc123")).To(Equal(wantCommit))
		},
		Entry("without a domain, Bitbucket Cloud is used", "",
			"https://bitbucket.org",
			"https://bitbucket.org/acme/widgets.git",
			"https://bitbucket.org/acme/widgets/commits/abc123"),
		Entry("with a domain, that host is used", "bitbucket.example.com:8443",
			"https://bitbucket.example.com:8443",
			"https://bitbucket.example.com:8443/acme/widgets.git",
			"https://bitbucket.example.com:8443/acme/widgets/commits/abc123"),
	)

	It("builds pull request URLs on the overridden domain", func() {
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bitbucketGitRepository()).Build()

		pr, err := NewBitbucketCloudPullRequestProvider(k8sClient, bitbucketSecret(), "bitbucket.example.com")
		Expect(err).NotTo(HaveOccurred())

		prObj := v1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "pr", Namespace: testNamespace},
			Spec: v1alpha1.PullRequestSpec{
				RepositoryReference: v1alpha1.ObjectReference{Name: "bb-repo"},
			},
			Status: v1alpha1.PullRequestStatus{ID: "7"},
		}
		Expect(pr.GetUrl(context.Background(), prObj)).To(Equal("https://bitbucket.example.com/acme/widgets/pull-requests/7"))
	})

	It("sends API requests to https://<domain>/2.0 with the bearer token", func() {
		var (
			mu       sync.Mutex
			gotPath  string
			gotToken string
		)
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotPath = r.URL.Path
			gotToken = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 7, "state": "OPEN"}`))
		}))
		DeferCleanup(srv.Close)

		previous := http.DefaultTransport
		http.DefaultTransport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest TLS only
		}
		DeferCleanup(func() { http.DefaultTransport = previous })

		srvURL, err := url.Parse(srv.URL)
		Expect(err).NotTo(HaveOccurred())

		client, err := GetClient(bitbucketSecret(), srvURL.Host)
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Repositories.PullRequests.Get(&bitbucket.PullRequestsOptions{Owner: "acme", RepoSlug: "widgets", ID: "7"})
		Expect(err).NotTo(HaveOccurred())

		mu.Lock()
		defer mu.Unlock()
		Expect(gotPath).To(Equal("/2.0/repositories/acme/widgets/pullrequests/7"))
		Expect(gotToken).To(Equal("Bearer bb-token"))
	})
})
