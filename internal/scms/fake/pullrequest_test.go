package fake

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreateAndGetURL(t *testing.T) {
	ctx := context.Background()
	provider, pullRequest := newTestPullRequestProvider(t)

	id, err := provider.Create(ctx, "title", "head", "base", "description", pullRequest)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if id != "1" {
		t.Fatalf("Create() id = %q, want %q", id, "1")
	}

	url, err := provider.GetUrl(ctx, pullRequest)
	if err != nil {
		t.Fatalf("GetUrl() error = %v", err)
	}

	want := "http://localhost:5000/test-owner/test-repo/pull/1"
	if url != want {
		t.Fatalf("GetUrl() = %q, want %q", url, want)
	}
}

func TestMergeReturnsErrorWhenTempDirCreationFails(t *testing.T) {
	ctx := context.Background()
	provider, pullRequest := newTestPullRequestProvider(t)
	pullRequest.Status.ID = "1"

	tmpFile := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(tmpFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("TMPDIR", tmpFile)

	err := provider.Merge(ctx, pullRequest)
	if err == nil {
		t.Fatal("Merge() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "could not make temp dir for repo server") {
		t.Fatalf("Merge() error = %q, want substring %q", err.Error(), "could not make temp dir for repo server")
	}
}

func newTestPullRequestProvider(t *testing.T) (*PullRequest, v1alpha1.PullRequest) {
	t.Helper()

	mutexPR.Lock()
	pullRequests = nil
	mutexPR.Unlock()
	t.Cleanup(func() {
		mutexPR.Lock()
		pullRequests = nil
		mutexPR.Unlock()
	})

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

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
