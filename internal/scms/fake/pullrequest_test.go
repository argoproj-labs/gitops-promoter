package fake

import (
	"context"
	"os"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFake(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Fake SCM Suite")
}

var _ = Describe("PullRequest", func() {
	It("creates a pull request and returns its URL", func() {
		ctx := context.Background()
		provider, pullRequest := newTestPullRequestProvider()

		id, err := provider.Create(ctx, "title", "head", "base", "description", pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("1"))

		url, err := provider.GetUrl(ctx, pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(url).To(Equal("http://localhost:5000/test-owner/test-repo/pull/1"))
	})

	It("returns an error when temp dir creation fails during merge", func() {
		ctx := context.Background()
		provider, pullRequest := newTestPullRequestProvider()
		pullRequest.Status.ID = "1"

		tmpDir, err := os.MkdirTemp("", "fake-pr-merge-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		tmpFile := tmpDir + "/not-a-directory"
		Expect(os.WriteFile(tmpFile, []byte("x"), 0o600)).To(Succeed())

		originalTMPDIR, hadTMPDIR := os.LookupEnv("TMPDIR")
		Expect(os.Setenv("TMPDIR", tmpFile)).To(Succeed())
		DeferCleanup(func() {
			if hadTMPDIR {
				Expect(os.Setenv("TMPDIR", originalTMPDIR)).To(Succeed())
				return
			}
			Expect(os.Unsetenv("TMPDIR")).To(Succeed())
		})

		err = provider.Merge(ctx, pullRequest)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("could not make temp dir for repo server"))
	})
})

func newTestPullRequestProvider() (*PullRequest, v1alpha1.PullRequest) {
	GinkgoHelper()

	mutexPR.Lock()
	pullRequests = nil
	mutexPR.Unlock()
	DeferCleanup(func() {
		mutexPR.Lock()
		pullRequests = nil
		mutexPR.Unlock()
	})

	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	const (
		namespace = "default"
		repoName  = "test-repo"
	)

	gitRepo := &v1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
		Spec: v1alpha1.GitRepositorySpec{
			Fake: &v1alpha1.FakeRepo{
				Owner: "test-owner",
				Name:  repoName,
			},
		},
	}

	client := crfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(gitRepo).
		Build()

	pullRequest := v1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: namespace,
		},
		Spec: v1alpha1.PullRequestSpec{
			RepositoryReference: v1alpha1.ObjectReference{
				Name: repoName,
			},
			SourceBranch: "feature",
			TargetBranch: "main",
		},
	}

	return NewFakePullRequestProvider(client), pullRequest
}
