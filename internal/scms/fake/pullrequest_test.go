package fake

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func resetPullRequestStore(t *testing.T) {
	t.Helper()
	mutexPR.Lock()
	pullRequests = nil
	mutexPR.Unlock()
}

func TestFindOpen_returnsStableCreationTime(t *testing.T) {
	t.Parallel()
	resetPullRequestStore(t)

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	gitRepo := &v1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "gr", Namespace: "default"},
		Spec: v1alpha1.GitRepositorySpec{
			Fake: &v1alpha1.FakeRepo{
				Owner: "owner",
				Name:  "repo",
			},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{
				Kind: "ScmProvider",
				Name: "scm",
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build()

	pr := NewFakePullRequestProvider(k8sClient)
	pullRequest := v1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PullRequestSpec{
			SourceBranch: "environment/dev-next",
			TargetBranch: "environment/dev",
			RepositoryReference: v1alpha1.ObjectReference{
				Name: gitRepo.Name,
			},
		},
	}

	id, err := pr.Create(ctx, "title", pullRequest.Spec.SourceBranch, pullRequest.Spec.TargetBranch, "desc", pullRequest)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	found, gotID, firstCreatedAt, err := pr.FindOpen(ctx, pullRequest)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, gotID)
	require.False(t, firstCreatedAt.IsZero())

	time.Sleep(5 * time.Millisecond)

	found, gotID, secondCreatedAt, err := pr.FindOpen(ctx, pullRequest)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, gotID)
	require.Equal(t, firstCreatedAt, secondCreatedAt)
}
